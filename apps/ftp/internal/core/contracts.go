package core

import (
	"context"
	"net"

	"github.com/spf13/afero"

	"github.com/example/cos-ftp-server/internal/db"
)

// Authenticator verifies credentials against the user store. clientIP is
// the peer's address as seen by the protocol touchpoint (FTP cc.RemoteAddr,
// SFTP conn.RemoteAddr); connectionType (audit.ConnFTP | audit.ConnSFTP)
// identifies the touchpoint that initiated auth. Both are stamped on every
// LOGIN audit event the authenticator emits.
type Authenticator interface {
	Authenticate(user, pass, connectionType string, clientIP net.IP) (*db.FTPUser, error)
}

// ObjectStorage is the storage-backend contract. A plugin owns both
// authenticating to its backend and mounting a per-user filesystem view.
// COS, S3, GCS, and local disk each implement this independently.
type ObjectStorage interface {
	Init(ctx context.Context) error
	Mount(rootFolder string) (afero.Fs, error)
	// BackendLocation returns a human-readable identifier of where data
	// lives (e.g. "https://bucket.cos.region.myqcloud.com" for COS,
	// "local:///var/data" for local). Surfaced in the audit log so per-row
	// origin is visible without joining config.
	BackendLocation() string
}

// Touchpoint is a protocol frontend (FTP today; SFTP/WebDAV later) that
// core starts/stops as a unit.
type Touchpoint interface {
	Start(ctx context.Context) error
	Stop() error
}
