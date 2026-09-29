package sftpserver

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/example/cos-ftp-server/internal/db"
)

type fakeAuthenticator struct {
	user *db.FTPUser
	err  error
}

func (f *fakeAuthenticator) Authenticate(user, pass, _ string, _ net.IP) (*db.FTPUser, error) {
	return f.user, f.err
}

type fakeConnMetadata struct{ user string }

func (f fakeConnMetadata) User() string          { return f.user }
func (f fakeConnMetadata) SessionID() []byte     { return nil }
func (f fakeConnMetadata) ClientVersion() []byte { return nil }
func (f fakeConnMetadata) ServerVersion() []byte { return nil }
func (f fakeConnMetadata) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}
}
func (f fakeConnMetadata) LocalAddr() net.Addr { return nil }

func TestPasswordCallback_Allows(t *testing.T) {
	s := &Server{
		Authenticator: &fakeAuthenticator{user: &db.FTPUser{ID: 1, Username: "bob", SFTPEnabled: true}},
		Audit:         nil,
	}
	if _, err := s.passwordCallback(fakeConnMetadata{user: "bob"}, []byte("pw")); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

func TestPasswordCallback_DeniesWhenSFTPDisabled(t *testing.T) {
	s := &Server{
		Authenticator: &fakeAuthenticator{user: &db.FTPUser{ID: 1, Username: "bob", SFTPEnabled: false}},
		Audit:         nil,
	}
	if _, err := s.passwordCallback(fakeConnMetadata{user: "bob"}, []byte("pw")); err == nil {
		t.Fatal("expected denial when SFTPEnabled=false")
	}
}

func TestPasswordCallback_DeniesOnAuthenticatorError(t *testing.T) {
	s := &Server{
		Authenticator: &fakeAuthenticator{err: errAuthFailed},
		Audit:         nil,
	}
	if _, err := s.passwordCallback(fakeConnMetadata{user: "bob"}, []byte("pw")); err == nil {
		t.Fatal("expected denial on authenticator error")
	}
}

func TestPublicKeyCallback_Allows(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromSigner(priv)
	if err != nil {
		t.Fatal(err)
	}
	authorizedLine := string(ssh.MarshalAuthorizedKey(signer.PublicKey()))

	s := &Server{
		LookupUser: func(ctx context.Context, username string) (*db.FTPUser, error) {
			return &db.FTPUser{ID: 1, Username: "bob", Enabled: true, SFTPEnabled: true, SFTPPublicKey: authorizedLine}, nil
		},
		Audit: nil,
	}
	if _, err := s.publicKeyCallback(fakeConnMetadata{user: "bob"}, signer.PublicKey()); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

func TestPublicKeyCallback_DeniesOnMismatch(t *testing.T) {
	_, stored, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	storedSigner, err := ssh.NewSignerFromSigner(stored)
	if err != nil {
		t.Fatal(err)
	}
	_, offered, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	offeredSigner, err := ssh.NewSignerFromSigner(offered)
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{
		LookupUser: func(ctx context.Context, username string) (*db.FTPUser, error) {
			return &db.FTPUser{ID: 1, Username: "bob", Enabled: true, SFTPEnabled: true,
				SFTPPublicKey: string(ssh.MarshalAuthorizedKey(storedSigner.PublicKey()))}, nil
		},
		Audit: nil,
	}
	if _, err := s.publicKeyCallback(fakeConnMetadata{user: "bob"}, offeredSigner.PublicKey()); err == nil {
		t.Fatal("expected denial on key mismatch")
	}
}

func TestPublicKeyCallback_DeniesWhenNoKeyRegistered(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromSigner(priv)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		LookupUser: func(ctx context.Context, username string) (*db.FTPUser, error) {
			return &db.FTPUser{ID: 1, Username: "bob", Enabled: true, SFTPEnabled: true, SFTPPublicKey: ""}, nil
		},
		Audit: nil,
	}
	if _, err := s.publicKeyCallback(fakeConnMetadata{user: "bob"}, signer.PublicKey()); err == nil {
		t.Fatal("expected denial when no key is registered")
	}
}

func TestPublicKeyCallback_DeniesWhenUserDisabled(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromSigner(priv)
	if err != nil {
		t.Fatal(err)
	}
	authorizedLine := string(ssh.MarshalAuthorizedKey(signer.PublicKey()))

	s := &Server{
		LookupUser: func(ctx context.Context, username string) (*db.FTPUser, error) {
			return &db.FTPUser{ID: 1, Username: "bob", Enabled: false, SFTPEnabled: true, SFTPPublicKey: authorizedLine}, nil
		},
		Audit: nil,
	}
	if _, err := s.publicKeyCallback(fakeConnMetadata{user: "bob"}, signer.PublicKey()); err == nil {
		t.Fatal("expected denial when user is disabled")
	}
}

func TestPublicKeyCallback_DeniesWhenSFTPDisabled(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromSigner(priv)
	if err != nil {
		t.Fatal(err)
	}
	authorizedLine := string(ssh.MarshalAuthorizedKey(signer.PublicKey()))

	s := &Server{
		LookupUser: func(ctx context.Context, username string) (*db.FTPUser, error) {
			return &db.FTPUser{ID: 1, Username: "bob", Enabled: true, SFTPEnabled: false, SFTPPublicKey: authorizedLine}, nil
		},
		Audit: nil,
	}
	if _, err := s.publicKeyCallback(fakeConnMetadata{user: "bob"}, signer.PublicKey()); err == nil {
		t.Fatal("expected denial when SFTP is disabled")
	}
}

func TestServerStartsAndStops(t *testing.T) {
	signer, err := LoadOrGenerateHostKey("", nil)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		Addr:          "127.0.0.1:0",
		HostKeySigner: signer,
		Authenticator: &fakeAuthenticator{err: errAuthFailed},
		LookupUser:    func(ctx context.Context, username string) (*db.FTPUser, error) { return nil, db.ErrNotFound },
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
}
