package ftpserver

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"os"
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
		// Control conn arrives via the gateway pod IP; the passive data
		// connection comes from a different IP (different SNAT / pod). With
		// the lib default (IPMatchRequired) we 425 every passive transfer.
		// Disable the peer-IP check — TLS on the control channel is the
		// auth boundary.
		PasvConnectionsCheck:   ftpserver.IPMatchDisabled,
		ActiveConnectionsCheck: ftpserver.IPMatchDisabled,
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
	// ftpserverlib v0.24.1 handleAUTH treats (nil, nil) as a valid empty TLS
	// config: it calls tls.Server(conn, nil) and the next read panics in
	// readClientHello dereferencing c.config. Returning a non-nil error here
	// makes handleAUTH write 500 and skip the wrap. Reject AUTH TLS when
	// certs aren't configured rather than crashing the connection.
	if s.TLS == nil {
		return nil, errors.New("ftpserver: TLS not configured")
	}
	cert, err := loadCertChain(s.TLS.CertFile, s.TLS.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("ftpserver: tls: %w", err)
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}}, nil
}

// loadCertChain reads certFile (PEM: first CERTIFICATE block is leaf, rest are
// intermediates) and keyFile, returns a tls.Certificate whose Certificate slice
// is [leaf, intermediates...]. Go's tls.LoadX509KeyPair drops everything past
// the first block, which makes clients without cached intermediates fail
// chain verification — FileZilla then warns "Unknown certificate".
func loadCertChain(certFile, keyFile string) (tls.Certificate, error) {
	pemBytes, err := os.ReadFile(certFile)
	if err != nil {
		return tls.Certificate{}, err
	}
	var leafDER []byte
	var chain [][]byte
	for {
		var block *pem.Block
		block, pemBytes = pem.Decode(pemBytes)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		if leafDER == nil {
			leafDER = block.Bytes
		} else {
			chain = append(chain, block.Bytes)
		}
	}
	if leafDER == nil {
		return tls.Certificate{}, errors.New("no certificate in " + certFile)
	}
	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	keyPEM, err := os.ReadFile(keyFile)
	if err != nil {
		return tls.Certificate{}, err
	}
	cert, err := tls.X509KeyPair(leafPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, err
	}
	cert.Certificate = append([][]byte{leafDER}, chain...)
	return cert, nil
}

func (s *Server) Start(_ context.Context) error {
	s.srv = ftpserver.NewFtpServer(s)
	if s.Logger != nil {
		s.Logger.Info("ftpserver starting", "addr", s.Addr, "port", s.Port, "passive", s.PassivePortRange, "public_host", s.PublicHost)
		s.logTLSState()
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

// BoundAddr returns the bound control-channel address (e.g. "127.0.0.1:51234")
// when Server.Addr=":0" was used to let the OS pick the port. Empty before Start.
func (s *Server) BoundAddr() string {
	if s.srv == nil {
		return ""
	}
	return s.srv.Addr()
}

// logTLSState reports whether FTPS is enabled, where the cert/key live, and
// the leaf cert's subject / issuer / validity. Runs once at startup so
// misconfiguration (missing file, parse error, expired cert) shows up in
// stdout before the first client connects. Does NOT fail Start: control
// channel still works without TLS, and GetTLSConfig will surface the error
// to AUTH-TLS clients anyway.
func (s *Server) logTLSState() {
	if s.TLS == nil {
		s.Logger.Info("ftpserver TLS disabled (plaintext only)")
		return
	}
	s.Logger.Info("ftpserver TLS enabled",
		"cert", s.TLS.CertFile,
		"key", s.TLS.KeyFile,
	)
	cert, err := tls.LoadX509KeyPair(s.TLS.CertFile, s.TLS.KeyFile)
	if err != nil {
		s.Logger.Error("ftpserver TLS load failed", "err", err)
		return
	}
	if len(cert.Certificate) == 0 {
		s.Logger.Error("ftpserver TLS cert has no leaf")
		return
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		s.Logger.Error("ftpserver TLS cert parse failed", "err", err)
		return
	}
	daysLeft := int(time.Until(leaf.NotAfter).Hours() / 24)
	attrs := []any{
		"subject", leaf.Subject.String(),
		"issuer", leaf.Issuer.String(),
		"not_before", leaf.NotBefore.UTC().Format(time.RFC3339),
		"not_after", leaf.NotAfter.UTC().Format(time.RFC3339),
		"days_until_expiry", daysLeft,
	}
	if daysLeft < 0 {
		s.Logger.Error("ftpserver TLS cert EXPIRED", attrs...)
		return
	}
	if daysLeft < 30 {
		s.Logger.Warn("ftpserver TLS cert expiring soon", attrs...)
		return
	}
	s.Logger.Info("ftpserver TLS cert valid", attrs...)
}
