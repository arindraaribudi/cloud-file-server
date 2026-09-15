package ftpserver

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/example/cos-ftp-server/internal/audit"
	"github.com/example/cos-ftp-server/internal/auth"
	"github.com/example/cos-ftp-server/internal/db"
)

// errAuthFailed is returned for every authentication failure so callers
// can't distinguish "wrong password" from "unknown user" (username
// enumeration). Specific reasons stay in the audit Detail.
var errAuthFailed = errors.New("login failed")

// DBAuthenticator looks up ftp_users in PostgreSQL and verifies bcrypt passwords.
// Implements the local Authenticator interface from server.go (T9).
type DBAuthenticator struct {
	Pool    *pgxpool.Pool
	Lockout *auth.Lockout
	Audit   *audit.Logger
}

func (a *DBAuthenticator) Authenticate(user, pass string) (*db.FTPUser, error) {
	ctx := context.Background()
	if !a.Lockout.Allow(user) {
		a.Audit.Log(audit.Event{
			Username: user, Action: "LOGIN", Success: false,
			Detail: map[string]any{"reason": "locked_out"},
		})
		return nil, errAuthFailed
	}
	dbCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	u, err := db.GetFTPUserByUsername(dbCtx, a.Pool, user)
	if err != nil {
		a.Lockout.RecordFailure(user)
		a.Audit.Log(audit.Event{
			Username: user, Action: "LOGIN", Success: false,
			Detail: map[string]any{"reason": "db_error", "err": err.Error()},
		})
		return nil, errAuthFailed
	}
	if !u.Enabled {
		a.Audit.Log(audit.Event{
			Username: user, Action: "LOGIN", Success: false,
			Detail: map[string]any{"reason": "disabled"},
		})
		return nil, errAuthFailed
	}
	if !auth.VerifyPassword(u.PasswordHash, pass) {
		a.Lockout.RecordFailure(user)
		a.Audit.Log(audit.Event{
			Username: user, Action: "LOGIN", Success: false,
			Detail: map[string]any{"reason": "bad_password"},
		})
		return nil, errAuthFailed
	}
	a.Lockout.Reset(user)
	a.Audit.Log(audit.Event{
		Username: user, Action: "LOGIN", Success: true,
	})
	return u, nil
}
