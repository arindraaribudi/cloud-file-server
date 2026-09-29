# Design: SFTP Touchpoint + Per-User Protocol Access

**Date:** 2026-09-26
**Status:** Approved for planning

## 1. Problem

`apps/ftp` only speaks FTP. `internal/core.Touchpoint` was designed
(2026-09-25 refactor) to let a second protocol frontend plug in without
touching auth, audit, or storage — but nothing implements it yet. Separately,
`apps/web` only exposes one account-level `enabled` toggle per user; there is
no way to grant a user FTP access without SFTP or vice versa, and no way to
register an SSH public key for a user.

This design adds an SFTP touchpoint and the per-protocol access controls
needed to manage it from the web UI.

## 2. Scope

**In scope:**
- `internal/sftpserver` package implementing `core.Touchpoint`.
- Password auth (reusing `core.DBAuthenticator`, same bcrypt hash as FTP)
  and optional per-user SSH public-key auth.
- `ftp_enabled` / `sftp_enabled` per-user columns, checked by each
  touchpoint in addition to the existing `enabled` master switch.
- Web UI: two access switches in `UserForm`, and a dedicated
  "set SFTP public key" action (new route + admin endpoint).
- Server SSH host key sourced from an env var (base64 PEM), with an
  ephemeral auto-generated fallback for unset.

**Out of scope:** WebDAV, SFTP equivalents of `AllowActiveMode` /
`RefuseOverwrite` / `MaxSessions` (FTP-protocol-specific, no SFTP
analogue requested), multiple public keys per user, key-type restrictions
beyond what `ssh.ParseAuthorizedKey` accepts.

## 3. Data model

Migration `0003_add_protocol_access`:

```sql
-- up
ALTER TABLE ftp_users ADD COLUMN ftp_enabled    boolean NOT NULL DEFAULT true;
ALTER TABLE ftp_users ADD COLUMN sftp_enabled   boolean NOT NULL DEFAULT false;
ALTER TABLE ftp_users ADD COLUMN sftp_public_key text;

-- down
ALTER TABLE ftp_users DROP COLUMN sftp_public_key;
ALTER TABLE ftp_users DROP COLUMN sftp_enabled;
ALTER TABLE ftp_users DROP COLUMN ftp_enabled;
```

`ftp_enabled` defaults `true` so every existing user keeps working over FTP
unchanged. `sftp_enabled` defaults `false` — SFTP access is opt-in per user.
`sftp_public_key` holds one `authorized_keys`-format line; `NULL`/empty means
no key registered (password-only for that user).

`db.FTPUser` gains three fields:

```go
type FTPUser struct {
	// ...existing fields...
	FTPEnabled    bool   `json:"ftp_enabled"`
	SFTPEnabled   bool   `json:"sftp_enabled"`
	SFTPPublicKey string `json:"-"` // never serialized to the user list/detail JSON
}
```

`ftpUserColumns`, and every `Scan(...)` call site (`CreateFTPUser`,
`GetFTPUserByUsername`, `GetFTPUserByID`, `UpdateFTPUser`, `ListFTPUsers`),
gain the two booleans. `sftp_public_key` is fetched by a new narrow query
(see below) rather than added to the general column list, since it must
never leak into the `/api/v1/users` list response.

New query functions:

```go
// GetFTPUserPublicKey returns the stored key (possibly "") for pubkey auth
// lookups. Does not require a password.
func GetFTPUserPublicKey(ctx context.Context, pool *pgxpool.Pool, username string) (*FTPUser, error)

// SetFTPUserSFTPKey stores (or clears, if key == "") the user's SFTP
// public key.
func SetFTPUserSFTPKey(ctx context.Context, pool *pgxpool.Pool, id int64, key string) error
```

`GetFTPUserPublicKey` returns the same `FTPUser` shape (minus password hash
usage) so the SFTP touchpoint's pubkey callback has `Enabled`/`SFTPEnabled`/
`RootFolder` available without a second round trip.

## 4. Core contract usage (no interface changes)

`core.Authenticator`, `core.ObjectStorage`, `core.Touchpoint` are unchanged.
Per-protocol gating is each touchpoint's own responsibility, applied *after*
`Authenticator.Authenticate` returns a user:

- `ftpserver.Server.AuthUser`: add `if !u.FTPEnabled { deny }` after the
  existing `Authenticate` call, audited as reason `"ftp_disabled"`.
- `sftpserver`'s password callback: same shape, checks `u.SFTPEnabled`,
  reason `"sftp_disabled"`.

This keeps `DBAuthenticator` protocol-agnostic (it only ever checks the
master `Enabled` flag, as today) and keeps the new logic local to the code
that already knows which protocol it is.

## 5. `internal/sftpserver` package

Mirrors `internal/ftpserver`'s shape:

```go
package sftpserver

type Server struct {
	Addr          string
	HostKeySigner ssh.Signer
	Authenticator core.Authenticator
	LookupUser    func(ctx context.Context, username string) (*db.FTPUser, error)
	Storage       core.ObjectStorage
	Audit         *audit.Logger
	Logger        *slog.Logger

	listener net.Listener
	wg       sync.WaitGroup
}

var _ core.Touchpoint = (*Server)(nil)

func (s *Server) Start(ctx context.Context) error
func (s *Server) Stop() error
```

`Start` builds an `ssh.ServerConfig`:

- `PasswordCallback(conn, pass)`: calls `Authenticator.Authenticate(conn.User(), string(pass))`.
  On success, checks `u.SFTPEnabled`; denies with a generic SSH auth error
  otherwise (mirrors FTP's non-distinguishing failure message — no
  username enumeration). Stashes the resolved `*db.FTPUser` on
  `ssh.Permissions.Extensions["ftp_user_id"]` (string-encoded ID) for the
  session handler to re-fetch, since `ssh.Permissions` values must be
  strings.
- `PublicKeyCallback(conn, key)`: calls `LookupUser(ctx, conn.User())`.
  Denies if not found, `!Enabled`, `!SFTPEnabled`, or `SFTPPublicKey == ""`.
  Otherwise parses the stored key with `ssh.ParseAuthorizedKey` and compares
  `.Marshal()` bytes against the offered key. Same success/failure audit
  shape as the password path (`reason: "pubkey_mismatch"` etc.).

Per accepted `net.Conn`: `ssh.NewServerConn`, then for each `NewChannel` of
type `"session"`, accept it and watch for a `"subsystem"` request with
payload `"sftp"`. On match: re-fetch the `*db.FTPUser` (by the ID stashed in
`Permissions`), `Storage.Mount(u.RootFolder)`, wrap with
`fsdriver.NewAuditFS(fs, audit, u.Username)`, adapt to `sftp.Handlers` (new
`fsdriver.NewSFTPHandlers(afero.Fs)`, see below), and run
`sftp.NewRequestServer(channel, handlers)` until the channel closes. Any
other subsystem/request is rejected — this touchpoint is SFTP-only, not a
general SSH shell/exec server.

`Stop`: close the listener; the accept loop's `Serve`-equivalent goroutine
exits on the resulting error, same shutdown shape as `ftpserver.Server.Stop`.

### Host key loading

New helper (in `sftpserver` or a small `internal/sshkey` package — leaning
`sftpserver` since it's the only consumer):

```go
func LoadOrGenerateHostKey(b64PEM string, log *slog.Logger) (ssh.Signer, error)
```

- `b64PEM != ""`: base64-decode, `ssh.ParsePrivateKey`. Decode/parse error
  returns an error (caller treats as fatal startup failure, same posture as
  a bad `FTP_TLS_CERT`/`FTP_TLS_KEY` pair).
- `b64PEM == ""`: generate an ed25519 keypair, wrap with
  `ssh.NewSignerFromSigner`, log a `Warn` with the key's fingerprint
  (`ssh.FingerprintSHA256`) only — never the key material — noting it is
  ephemeral and will change on restart.

### `fsdriver.NewSFTPHandlers`

New file `internal/fsdriver/sftp_handlers.go`. Adapts an `afero.Fs` to
`github.com/pkg/sftp`'s `sftp.Handlers` (`FileReader`, `FileWriter`,
`FileCmder`, `FileLister` — the four interfaces `sftp.Handlers` bundles):
open-for-read/write via `afero.Fs.OpenFile`, `Stat`/`Lstat` via `afero.Fs`,
`Mkdir`/`Remove`/`Rename`/`RemoveAll` via `afero.Fs`, directory listing via
`afero.ReadDir`. This is the same `afero.Fs` interface `ftpserverlib`
already drives through `NewDriver`/`ClientDriver` — no changes to `cos.go`
or `local.go` needed, only a new consumer of the existing `afero.Fs` mount.

## 6. Config additions

```go
type Config struct {
	// ...existing fields...
	SFTPEnabled  bool
	SFTPListen   string // default ":2222"
	SFTPHostKey  string // base64-encoded PEM private key; empty = ephemeral
}
```

Env vars: `SFTP_ENABLED` (default `false`), `SFTP_LISTEN` (default
`:2222`), `SFTP_HOST_KEY` (no default). No passive-port-range equivalent —
SFTP is a single TCP port, no NAT/passive-mode concerns.

## 7. `main.go` wiring

After the existing `srv := &ftpserver.Server{...}; srv.Start(ctx)` block:

```go
var sftpSrv *sftpserver.Server
if cfg.SFTPEnabled {
	hostKey, err := sftpserver.LoadOrGenerateHostKey(cfg.SFTPHostKey, log)
	if err != nil {
		return fmt.Errorf("sftp: host key: %w", err)
	}
	sftpSrv = &sftpserver.Server{
		Addr:          cfg.SFTPListen,
		HostKeySigner: hostKey,
		Authenticator: srv.Authenticator, // same *core.DBAuthenticator instance
		LookupUser: func(ctx context.Context, u string) (*db.FTPUser, error) {
			return db.GetFTPUserPublicKey(ctx, pool, u)
		},
		Storage: storage,
		Audit:   auditLog,
		Logger:  log,
	}
	if err := sftpSrv.Start(ctx); err != nil {
		return fmt.Errorf("sftp: %w", err)
	}
}
```

Shutdown: `if sftpSrv != nil { _ = sftpSrv.Stop() }` alongside the existing
`srv.Stop()` call. Two named touchpoint variables, not a generalized
`[]core.Touchpoint` slice — only two touchpoints exist; a slice adds a loop
for no present benefit (YAGNI, revisit if a third touchpoint shows up).

## 8. Admin API

New handler in `internal/admin/users_api.go`:

```go
func (a *API) setUserSFTPKey(w http.ResponseWriter, r *http.Request) {
	// decode {public_key string}, trim
	// "" clears the key
	// non-empty: ssh.ParseAuthorizedKey to validate, 400 on parse error
	// db.SetFTPUserSFTPKey(ctx, pool, existing.ID, key)
	// audit ADMIN_SET_SFTP_KEY
}
```

Route (admin-only group, alongside `resetUserPassword`):
`POST /api/v1/users/{username}/sftp-key`

`createUser`/`updateUser` request bodies gain `ftp_enabled`/`sftp_enabled`
bool fields, defaulting to `true`/`false` respectively when omitted (so
existing API callers/tests that don't send them keep today's behavior).

## 9. Web UI

**`UserForm.tsx`:** two more switches next to the existing "Enabled" one —
"FTP access" (`ftp_enabled`, default `true`) and "SFTP access"
(`sftp_enabled`, default `false`). Wired into the same `onSubmit` payload.

**`lib/api.ts`:** `FTPUser` type gains `ftp_enabled`/`sftp_enabled`;
`createUser`/`updateUser` payload types gain the same; new
`setUserSFTPKey(username, publicKey)` calling the new endpoint.

**New action — "Set SFTP public key":**
- `routes/users.tsx`: new `KeyIcon` row-action button next to the existing
  reset-password button, navigating to `/users/$username/sftp-key`.
- New `routes/user-sftp-key.tsx`: small dedicated form (textarea for the
  `authorized_keys` line, Save/Cancel) — not routed through the existing
  `PasswordForm` component, since the input shape (multi-line key text, no
  generate/show-hide affordances) doesn't fit it. Same page structure/style
  as `UserPassword`.
- `router.tsx`: register the new route under `authedRoute`.

## 10. Error handling

- `SFTP_ENABLED=true` + unparseable `SFTP_HOST_KEY` → fail startup (same
  posture as a broken FTP TLS cert pair).
- `SFTP_HOST_KEY` unset → ephemeral key, `Warn` log with fingerprint only.
- SFTP auth failure (bad password, disabled protocol, key mismatch, unknown
  user) → single generic SSH auth error to the client; specifics only in
  the audit log (`reason` field), matching FTP's existing
  no-enumeration posture.
- Invalid public key submitted via the admin endpoint → `400` with the
  `ssh.ParseAuthorizedKey` error message (same pattern as
  `auth.ValidatePassword`'s 400s).
- `Lockout` is the same shared instance FTP uses — repeated failures on a
  username lock out both protocols together.

## 11. Testing

- `internal/sftpserver/server_test.go` (new): `PasswordCallback`/
  `PublicKeyCallback` behavior against a fake `Authenticator`/`LookupUser`
  — enabled/disabled/wrong-password/key-mismatch cases — without a real
  DB or network listener, mirroring how `ftpserver/server_test.go` tests
  `AuthUser` today.
- `internal/sftpserver`: `LoadOrGenerateHostKey` test — valid base64 PEM
  parses to a signer; empty input generates one; garbage input errors.
- `internal/fsdriver/sftp_handlers_test.go` (new): the `afero.Fs` adapter
  against the package's existing in-memory test fixtures (read, write,
  mkdir, list, rename).
- `internal/db/queries_test.go`: extend for `ftp_enabled`/`sftp_enabled`
  defaults on create, and `GetFTPUserPublicKey`/`SetFTPUserSFTPKey`
  round-trip.
- `internal/config/config_test.go`: new field defaults
  (`SFTPEnabled=false`, `SFTPListen=":2222"`).
- `internal/admin`: extend `users_api`-adjacent tests for
  `setUserSFTPKey` (valid key, invalid key → 400, empty clears).
- Frontend: no existing component-level test suite beyond Playwright e2e;
  no new frontend tests added (matches current project convention).
