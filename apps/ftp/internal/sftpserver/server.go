package sftpserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/example/cos-ftp-server/internal/audit"
	"github.com/example/cos-ftp-server/internal/core"
	"github.com/example/cos-ftp-server/internal/db"
	"github.com/example/cos-ftp-server/internal/fsdriver"
)

// errAuthFailed is returned for every SFTP authentication failure so
// callers can't distinguish "wrong password" from "unknown user" —
// same posture as core.DBAuthenticator. Specific reasons stay in the
// audit Detail.
var errAuthFailed = errors.New("sftpserver: login failed")

// ConnType is the value stamped on every audit event this touchpoint emits.
// Owns its own protocol name — audit is a sink, it doesn't define the set.
const ConnType = "sftp"

// clientIPFromAddr extracts the host portion of a "host:port" RemoteAddr
// and parses it as an IP. Falls back to the IPv4 loopback when parsing
// fails or addr is empty (e.g. nil RemoteAddr in tests) so the audit row
// always has an IP rather than NULL.
func clientIPFromAddr(addr string) net.IP {
	if addr == "" {
		return net.IPv4(127, 0, 0, 1)
	}
	if host, _, err := net.SplitHostPort(addr); err == nil && host != "" {
		if ip := net.ParseIP(host); ip != nil {
			return ip
		}
	}
	return net.IPv4(127, 0, 0, 1)
}

// Server is the SFTP protocol touchpoint. Implements core.Touchpoint.
type Server struct {
	Addr          string
	HostKeySigner ssh.Signer
	Authenticator core.Authenticator
	// LookupUser resolves a username without a password, for the
	// public-key auth path and for mounting storage once a session is
	// authenticated. Set to db.GetFTPUserPublicKey in production.
	LookupUser func(ctx context.Context, username string) (*db.FTPUser, error)
	Storage    core.ObjectStorage
	Audit      *audit.Logger
	Logger     *slog.Logger

	listener net.Listener
	wg       sync.WaitGroup
}

var _ core.Touchpoint = (*Server)(nil)

func (s *Server) sshConfig() *ssh.ServerConfig {
	cfg := &ssh.ServerConfig{
		PasswordCallback:  s.passwordCallback,
		PublicKeyCallback: s.publicKeyCallback,
	}
	cfg.AddHostKey(s.HostKeySigner)
	return cfg
}

func (s *Server) passwordCallback(conn ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
	if s.Authenticator == nil {
		return nil, errAuthFailed
	}
	clientIP := clientIPFromAddr(conn.RemoteAddr().String())
	u, err := s.Authenticator.Authenticate(conn.User(), string(pass), ConnType, clientIP)
	if err != nil {
		return nil, errAuthFailed
	}
	if !u.SFTPEnabled {
		s.Audit.Log(audit.Event{
			Username: conn.User(), ClientIP: clientIP, Action: "LOGIN", Success: false,
			ConnectionType: ConnType,
			Detail:         map[string]any{"reason": "sftp_disabled"},
		})
		return nil, errAuthFailed
	}
	return &ssh.Permissions{}, nil
}

func (s *Server) publicKeyCallback(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	if s.LookupUser == nil {
		return nil, errAuthFailed
	}
	clientIP := clientIPFromAddr(conn.RemoteAddr().String())
	u, err := s.LookupUser(context.Background(), conn.User())
	if err != nil || u == nil {
		return nil, errAuthFailed
	}
	if !u.Enabled || !u.SFTPEnabled || u.SFTPPublicKey == "" {
		s.Audit.Log(audit.Event{
			Username: conn.User(), ClientIP: clientIP, Action: "LOGIN", Success: false,
			ConnectionType: ConnType,
			Detail:         map[string]any{"reason": "sftp_pubkey_denied"},
		})
		return nil, errAuthFailed
	}
	stored, _, _, _, err := ssh.ParseAuthorizedKey([]byte(u.SFTPPublicKey))
	if err != nil {
		return nil, errAuthFailed
	}
	if !bytes.Equal(stored.Marshal(), key.Marshal()) {
		s.Audit.Log(audit.Event{
			Username: conn.User(), ClientIP: clientIP, Action: "LOGIN", Success: false,
			ConnectionType: ConnType,
			Detail:         map[string]any{"reason": "pubkey_mismatch"},
		})
		return nil, errAuthFailed
	}
	s.Audit.Log(audit.Event{
		Username: conn.User(), ClientIP: clientIP, Action: "LOGIN", Success: true,
		ConnectionType: ConnType,
		Detail:         map[string]any{"method": "publickey"},
	})
	return &ssh.Permissions{}, nil
}

// Start listens on s.Addr and serves SSH/SFTP connections until Stop is
// called. Matches ftpserver.Server.Start's fire-and-forget accept loop.
func (s *Server) Start(_ context.Context) error {
	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return fmt.Errorf("sftpserver: listen: %w", err)
	}
	s.listener = ln
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handleConn(conn)
		}
	}()
	return nil
}

func (s *Server) Stop() error {
	if s.listener == nil {
		return nil
	}
	err := s.listener.Close()
	s.wg.Wait()
	return err
}

func (s *Server) handleConn(nc net.Conn) {
	sconn, chans, reqs, err := ssh.NewServerConn(nc, s.sshConfig())
	if err != nil {
		_ = nc.Close()
		return
	}
	defer func() { _ = sconn.Close() }()
	go ssh.DiscardRequests(reqs)
	for newCh := range chans {
		if newCh.ChannelType() != "session" {
			_ = newCh.Reject(ssh.UnknownChannelType, "unsupported channel type")
			continue
		}
		ch, chReqs, err := newCh.Accept()
		if err != nil {
			continue
		}
		go s.handleSession(sconn, ch, chReqs)
	}
}

func (s *Server) handleSession(sconn *ssh.ServerConn, ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer func() { _ = ch.Close() }()
	for req := range reqs {
		isSFTPSubsystem := req.Type == "subsystem" && len(req.Payload) >= 4 && string(req.Payload[4:]) == "sftp"
		if !isSFTPSubsystem {
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
			continue
		}
		if req.WantReply {
			_ = req.Reply(true, nil)
		}
		s.serveSFTP(sconn, ch)
		return
	}
}

func (s *Server) serveSFTP(sconn *ssh.ServerConn, ch ssh.Channel) {
	if s.LookupUser == nil || s.Storage == nil {
		return
	}
	u, err := s.LookupUser(context.Background(), sconn.User())
	if err != nil || u == nil {
		return
	}
	fs, err := s.Storage.Mount(u.RootFolder)
	if err != nil {
		if s.Logger != nil {
			s.Logger.Error("sftp: mount failed", "username", u.Username, "err", err)
		}
		return
	}
	clientIP := clientIPFromAddr(sconn.RemoteAddr().String())
	backend := s.Storage.BackendLocation()
	auditedFS := fsdriver.NewAuditFS(fs, s.Audit, u.Username, clientIP, ConnType, backend, u.RootFolder)
	handlers := fsdriver.NewSFTPHandlers(auditedFS)
	reqServer := sftp.NewRequestServer(ch, handlers)
	_ = reqServer.Serve()
	_ = reqServer.Close()
	s.Audit.Log(audit.Event{Username: u.Username, ClientIP: clientIP, Action: "LOGOUT", Success: true, ConnectionType: ConnType, BackendLocation: backend, RootFolder: u.RootFolder})
}
