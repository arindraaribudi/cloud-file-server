package core

import (
	"context"
	"net"

	"github.com/spf13/afero"

	"github.com/example/cos-ftp-server/internal/db"
)

// Authenticator verifies credentials against the user store. clientIP is
// the peer's address as seen by the protocol touchpoint (FTP cc.RemoteAddr,
// SFTP conn.RemoteAddr) — included on every LOGIN audit event the
// authenticator emits.
type Authenticator interface {
	Authenticate(user, pass string, clientIP net.IP) (*db.FTPUser, error)
}

// ObjectStorage is the storage-backend contract. A plugin owns both
// authenticating to its backend and mounting a per-user filesystem view.
// COS, S3, GCS, and local disk each implement this independently.
type ObjectStorage interface {
	Init(ctx context.Context) error
	Mount(rootFolder string) (afero.Fs, error)
}

// Touchpoint is a protocol frontend (FTP today; SFTP/WebDAV later) that
// core starts/stops as a unit.
type Touchpoint interface {
	Start(ctx context.Context) error
	Stop() error
}
