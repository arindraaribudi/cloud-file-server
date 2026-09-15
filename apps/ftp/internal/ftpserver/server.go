package ftpserver

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"sync"
	"time"

	ftpserver "github.com/fclairamb/ftpserverlib"

	"github.com/example/cos-ftp-server/internal/audit"
	"github.com/example/cos-ftp-server/internal/db"
)

// Authenticator checks user credentials and returns the authenticated user's record.
type Authenticator interface {
	Authenticate(user, pass string) (*db.FTPUser, error)
}

type TLSConfig struct {
	CertFile string
	KeyFile  string
}

// Server wraps ftpserverlib lifecycle. Implements ftpserver.MainDriver directly.
//
// API DEVIATION from the original T9 plan: ftpserverlib v0.24.1's
// NewFtpServer takes a single MainDriver argument (the plan's
// NewFtpServer(driver, auth, opts) is an older or different API).
// Settings come from MainDriver.GetSettings() rather than as constructor
// args. Authenticator is our own local interface that AuthUser delegates
// to; ftpserverlib v0.24.1 has no separate Authenticator type (auth lives
// inside MainDriver.AuthUser). TLS is configured via MainDriver.GetTLSConfig(),
// not via a Settings field.
type Server struct {
	Addr             string
	Port             int
	PublicHost       string
	PassivePortRange [2]int
	IdleTimeout      time.Duration
	TLS              *TLSConfig
	NewDriver        func(u *db.FTPUser) (ftpserver.ClientDriver, error) // builds the per-user root/bucket driver
	Authenticator    Authenticator
	Audit            *audit.Logger
	Logger           *slog.Logger

	srv     *ftpserver.FtpServer
	serveWG sync.WaitGroup // tracks the Serve goroutine so Stop can drain it
}

// Compile-time check.
var _ ftpserver.MainDriver = (*Server)(nil)

func (s *Server) GetSettings() (*ftpserver.Settings, error) {
	listenAddr := s.Addr
	if listenAddr == "" && s.Port > 0 {
		listenAddr = fmt.Sprintf(":%d", s.Port)
	}
	opts := &ftpserver.Settings{
		ListenAddr: listenAddr,
		PublicHost: s.PublicHost,
	}
	if s.PassivePortRange[1] > 0 {
		opts.PassiveTransferPortRange = &ftpserver.PortRange{
			Start: s.PassivePortRange[0],
			End:   s.PassivePortRange[1],
		}
	}
	if s.IdleTimeout > 0 {
		opts.IdleTimeout = int(s.IdleTimeout.Seconds())
	}
	return opts, nil
}

func (s *Server) ClientConnected(ftpserver.ClientContext) (string, error) {
	return "Welcome", nil
}

// ClientDisconnected fires on every disconnect, authenticated or not. Only
// authenticated sessions (username stashed in AuthUser via cc.SetExtra) get
// a LOGOUT audit event.
func (s *Server) ClientDisconnected(cc ftpserver.ClientContext) {
	if username, ok := cc.Extra().(string); ok && username != "" {
		s.Audit.Log(audit.Event{Username: username, Action: "LOGOUT", Success: true})
	}
}

func (s *Server) AuthUser(cc ftpserver.ClientContext, user, pass string) (ftpserver.ClientDriver, error) {
	if s.Authenticator == nil {
		return nil, fmt.Errorf("ftpserver: auth failed for user %q", user)
	}
	u, err := s.Authenticator.Authenticate(user, pass)
	if err != nil {
		return nil, fmt.Errorf("ftpserver: auth failed for user %q", user)
	}
	if s.NewDriver == nil {
		return nil, fmt.Errorf("ftpserver: no driver factory configured")
	}
	cc.SetExtra(u.Username)
	return s.NewDriver(u)
}

func (s *Server) GetTLSConfig() (*tls.Config, error) {
	if s.TLS == nil {
		return nil, nil
	}
	cert, err := tls.LoadX509KeyPair(s.TLS.CertFile, s.TLS.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("ftpserver: tls: %w", err)
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}}, nil
}

func (s *Server) Start(_ context.Context) error {
	s.srv = ftpserver.NewFtpServer(s)
	if s.Logger != nil {
		s.Logger.Info("ftpserver starting", "addr", s.Addr, "port", s.Port, "passive", s.PassivePortRange)
	}
	// ponytail: split ListenAndServe into Listen + Serve. Listen binds the
	// listener synchronously so Start returns only after the port is bound
	// (or returns the bind error), eliminating the race with Stop. Serve
	// runs in a goroutine and is drained by Stop via serveWG. Upgrade when
	// ftpserverlib exposes a bound signal: replace this with the hook.
	if err := s.srv.Listen(); err != nil {
		return fmt.Errorf("ftpserver: listen: %w", err)
	}
	s.serveWG.Add(1)
	go func() {
		defer s.serveWG.Done()
		if err := s.srv.Serve(); err != nil {
			if s.Logger != nil {
				s.Logger.Error("ftpserver stopped", "err", err)
			}
		}
	}()
	return nil
}

func (s *Server) Stop() error {
	if s.srv == nil {
		return nil
	}
	err := s.srv.Stop()
	s.serveWG.Wait()
	return err
}
