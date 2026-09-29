# SFTP Touchpoint (Backend) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an SFTP touchpoint to `apps/ftp` (password + optional per-user SSH public key auth) and the per-protocol (`ftp_enabled`/`sftp_enabled`) access columns it needs, without changing `core.Authenticator`/`core.ObjectStorage`/`core.Touchpoint`.

**Architecture:** New `internal/sftpserver` package implements `core.Touchpoint`, sharing `core.DBAuthenticator`, `core.ObjectStorage`, `audit.Logger`, and `auth.Lockout` with the existing `internal/ftpserver`. A new `internal/fsdriver` adapter bridges `afero.Fs` (the same mount FTP uses) to `github.com/pkg/sftp`'s handler interfaces. Per-protocol gating lives in each touchpoint, not in the shared authenticator.

**Tech Stack:** Go 1.26, `golang.org/x/crypto/ssh` (already a dependency), `github.com/pkg/sftp` (new), `github.com/spf13/afero`, `github.com/jackc/pgx/v5`.

**Design doc:** `docs/superpowers/specs/2026-09-26-sftp-touchpoint-design.md`

**Companion plan:** `docs/superpowers/plans/2026-09-26-sftp-touchpoint-frontend.md` (web UI, depends on this plan's admin API changes).

---

## Task 1: Data model — `ftp_enabled`, `sftp_enabled`, `sftp_public_key`

**Files:**
- Create: `apps/ftp/internal/db/migrations/0003_add_protocol_access.up.sql`
- Create: `apps/ftp/internal/db/migrations/0003_add_protocol_access.down.sql`
- Modify: `apps/ftp/internal/db/queries.go`
- Test: `apps/ftp/internal/db/queries_test.go`

- [ ] **Step 1: Write the migration**

`apps/ftp/internal/db/migrations/0003_add_protocol_access.up.sql`:

```sql
ALTER TABLE ftp_users ADD COLUMN ftp_enabled boolean NOT NULL DEFAULT true;
ALTER TABLE ftp_users ADD COLUMN sftp_enabled boolean NOT NULL DEFAULT false;
ALTER TABLE ftp_users ADD COLUMN sftp_public_key text;
```

`apps/ftp/internal/db/migrations/0003_add_protocol_access.down.sql`:

```sql
ALTER TABLE ftp_users DROP COLUMN sftp_public_key;
ALTER TABLE ftp_users DROP COLUMN sftp_enabled;
ALTER TABLE ftp_users DROP COLUMN ftp_enabled;
```

`internal/db/migrate.go` embeds `migrations/*.sql` via `go:embed` — no code change needed, the new files are picked up automatically.

- [ ] **Step 2: Write the failing tests**

Append to `apps/ftp/internal/db/queries_test.go`:

```go
func TestFTPUserProtocolFlagsDefaults(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `DELETE FROM ftp_users WHERE username = 'protocol-flags-test'`)

	u := &FTPUser{
		Username:     "protocol-flags-test",
		PasswordHash: "x",
		RootFolder:   "/protocol-flags-test",
		COSBucket:    "b",
		Enabled:      true,
		FTPEnabled:   true,
	}
	id, err := CreateFTPUser(ctx, pool, u)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = SoftDeleteFTPUser(ctx, pool, id) }()

	got, err := GetFTPUserByUsername(ctx, pool, "protocol-flags-test")
	if err != nil {
		t.Fatal(err)
	}
	if !got.FTPEnabled {
		t.Error("expected FTPEnabled=true")
	}
	if got.SFTPEnabled {
		t.Error("expected SFTPEnabled=false by default")
	}
}

func TestSFTPPublicKeyRoundTrip(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `DELETE FROM ftp_users WHERE username = 'sftp-key-test'`)

	u := &FTPUser{
		Username:     "sftp-key-test",
		PasswordHash: "x",
		RootFolder:   "/sftp-key-test",
		COSBucket:    "b",
		Enabled:      true,
		SFTPEnabled:  true,
	}
	id, err := CreateFTPUser(ctx, pool, u)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = SoftDeleteFTPUser(ctx, pool, id) }()

	got, err := GetFTPUserPublicKey(ctx, pool, "sftp-key-test")
	if err != nil {
		t.Fatal(err)
	}
	if got.SFTPPublicKey != "" {
		t.Errorf("expected empty key initially, got %q", got.SFTPPublicKey)
	}

	const key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleNotARealKeyPadding0000 test@example"
	if err := SetFTPUserSFTPKey(ctx, pool, id, key); err != nil {
		t.Fatal(err)
	}
	got2, err := GetFTPUserPublicKey(ctx, pool, "sftp-key-test")
	if err != nil {
		t.Fatal(err)
	}
	if got2.SFTPPublicKey != key {
		t.Errorf("SFTPPublicKey=%q, want %q", got2.SFTPPublicKey, key)
	}

	if err := SetFTPUserSFTPKey(ctx, pool, id, ""); err != nil {
		t.Fatal(err)
	}
	got3, err := GetFTPUserPublicKey(ctx, pool, "sftp-key-test")
	if err != nil {
		t.Fatal(err)
	}
	if got3.SFTPPublicKey != "" {
		t.Errorf("expected cleared key, got %q", got3.SFTPPublicKey)
	}
}
```

- [ ] **Step 3: Run to verify it fails to compile**

Run: `cd apps/ftp && go test ./internal/db/...`
Expected: FAIL to compile — `u.FTPEnabled undefined`, `undefined: GetFTPUserPublicKey`, `undefined: SetFTPUserSFTPKey`.

- [ ] **Step 4: Update `FTPUser` struct and `ftpUserColumns`**

In `apps/ftp/internal/db/queries.go`, replace:

```go
const ftpUserColumns = "id, username, password_hash, root_folder, cos_bucket, COALESCE(cos_region, ''), enabled, allow_active_mode, refuse_overwrite, max_sessions, created_at, updated_at, deleted_at, last_login"

type FTPUser struct {
	ID              int64      `json:"id"`
	Username        string     `json:"username"`
	PasswordHash    string     `json:"-"`
	RootFolder      string     `json:"root_folder"`
	COSBucket       string     `json:"cos_bucket"`
	COSRegion       string     `json:"cos_region"`
	Enabled         bool       `json:"enabled"`
	AllowActiveMode bool       `json:"allow_active_mode"`
	RefuseOverwrite bool       `json:"refuse_overwrite"`
	MaxSessions     int        `json:"max_sessions"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	DeletedAt       *time.Time `json:"deleted_at,omitempty"`
	LastLogin       *time.Time `json:"last_login"`
}
```

with:

```go
const ftpUserColumns = "id, username, password_hash, root_folder, cos_bucket, COALESCE(cos_region, ''), enabled, allow_active_mode, refuse_overwrite, max_sessions, ftp_enabled, sftp_enabled, created_at, updated_at, deleted_at, last_login"

type FTPUser struct {
	ID              int64      `json:"id"`
	Username        string     `json:"username"`
	PasswordHash    string     `json:"-"`
	RootFolder      string     `json:"root_folder"`
	COSBucket       string     `json:"cos_bucket"`
	COSRegion       string     `json:"cos_region"`
	Enabled         bool       `json:"enabled"`
	AllowActiveMode bool       `json:"allow_active_mode"`
	RefuseOverwrite bool       `json:"refuse_overwrite"`
	MaxSessions     int        `json:"max_sessions"`
	FTPEnabled      bool       `json:"ftp_enabled"`
	SFTPEnabled     bool       `json:"sftp_enabled"`
	// SFTPPublicKey is never included in ftpUserColumns/the general JSON
	// response — fetched only via GetFTPUserPublicKey, which the SFTP
	// touchpoint uses for its pubkey-auth lookup.
	SFTPPublicKey string     `json:"-"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	DeletedAt     *time.Time `json:"deleted_at,omitempty"`
	LastLogin     *time.Time `json:"last_login"`
}
```

- [ ] **Step 5: Update `CreateFTPUser`, `UpdateFTPUser`, and the two `Scan` call sites**

Replace `CreateFTPUser`:

```go
func CreateFTPUser(ctx context.Context, pool *pgxpool.Pool, u *FTPUser) (int64, error) {
	var id int64
	err := pool.QueryRow(ctx, `
		INSERT INTO ftp_users (username, password_hash, root_folder, cos_bucket, cos_region, enabled, allow_active_mode, refuse_overwrite, max_sessions, ftp_enabled, sftp_enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`,
		u.Username, u.PasswordHash, u.RootFolder, u.COSBucket, nilIfEmpty(u.COSRegion),
		u.Enabled, u.AllowActiveMode, u.RefuseOverwrite, u.MaxSessions, u.FTPEnabled, u.SFTPEnabled).Scan(&id)
	return id, err
}
```

Replace `GetFTPUserByUsername`'s `Scan` call:

```go
	err := pool.QueryRow(ctx, `
		SELECT `+ftpUserColumns+`
		FROM ftp_users WHERE username=$1 AND deleted_at IS NULL`, name).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.RootFolder, &u.COSBucket, &u.COSRegion,
			&u.Enabled, &u.AllowActiveMode, &u.RefuseOverwrite, &u.MaxSessions,
			&u.FTPEnabled, &u.SFTPEnabled,
			&u.CreatedAt, &u.UpdatedAt, &u.DeletedAt, &u.LastLogin)
```

Replace `GetFTPUserByID`'s `Scan` call:

```go
	err := pool.QueryRow(ctx,
		`SELECT `+ftpUserColumns+` FROM ftp_users WHERE id=$1 AND deleted_at IS NULL`, id).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.RootFolder, &u.COSBucket,
			&u.COSRegion, &u.Enabled, &u.AllowActiveMode, &u.RefuseOverwrite,
			&u.MaxSessions, &u.FTPEnabled, &u.SFTPEnabled,
			&u.CreatedAt, &u.UpdatedAt, &u.DeletedAt, &u.LastLogin)
```

Replace `UpdateFTPUser`:

```go
func UpdateFTPUser(ctx context.Context, pool *pgxpool.Pool, u *FTPUser) error {
	_, err := pool.Exec(ctx, `
		UPDATE ftp_users SET root_folder=$2, cos_bucket=$3, cos_region=$4, enabled=$5,
			allow_active_mode=$6, refuse_overwrite=$7, max_sessions=$8,
			ftp_enabled=$9, sftp_enabled=$10, updated_at=now()
		WHERE id=$1 AND deleted_at IS NULL`,
		u.ID, u.RootFolder, u.COSBucket, nilIfEmpty(u.COSRegion), u.Enabled,
		u.AllowActiveMode, u.RefuseOverwrite, u.MaxSessions, u.FTPEnabled, u.SFTPEnabled)
	return err
}
```

Replace `ListFTPUsers`'s `Scan` call:

```go
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.RootFolder, &u.COSBucket, &u.COSRegion,
			&u.Enabled, &u.AllowActiveMode, &u.RefuseOverwrite, &u.MaxSessions,
			&u.FTPEnabled, &u.SFTPEnabled,
			&u.CreatedAt, &u.UpdatedAt, &u.DeletedAt, &u.LastLogin); err != nil {
```

- [ ] **Step 6: Add `GetFTPUserPublicKey` and `SetFTPUserSFTPKey`**

Add to `apps/ftp/internal/db/queries.go`, near `GetFTPUserByUsername`:

```go
// GetFTPUserPublicKey fetches the minimal set of fields the SFTP touchpoint's
// public-key auth path needs: identity, access flags, mount root, and the
// stored key. Does not require a password. Returns ErrNotFound if the user
// doesn't exist or is soft-deleted.
func GetFTPUserPublicKey(ctx context.Context, pool *pgxpool.Pool, username string) (*FTPUser, error) {
	u := &FTPUser{}
	err := pool.QueryRow(ctx, `
		SELECT id, username, root_folder, enabled, sftp_enabled, COALESCE(sftp_public_key, '')
		FROM ftp_users WHERE username=$1 AND deleted_at IS NULL`, username).
		Scan(&u.ID, &u.Username, &u.RootFolder, &u.Enabled, &u.SFTPEnabled, &u.SFTPPublicKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

// SetFTPUserSFTPKey stores u's SFTP public key. An empty key clears it
// (falls back to password-only auth for that user).
func SetFTPUserSFTPKey(ctx context.Context, pool *pgxpool.Pool, id int64, key string) error {
	_, err := pool.Exec(ctx, `UPDATE ftp_users SET sftp_public_key=$2, updated_at=now() WHERE id=$1`, id, nilIfEmpty(key))
	return err
}
```

- [ ] **Step 7: Run tests to verify they pass**

Requires a real Postgres. If you have one running locally:

Run: `cd apps/ftp && TEST_DATABASE_URL=postgres://user:pass@localhost:5432/ftp_test?sslmode=disable go test ./internal/db/... -run 'TestFTPUserProtocolFlagsDefaults|TestSFTPPublicKeyRoundTrip|TestFTPUserCRUD|TestGetFTPUserByID' -v`
Expected: PASS (or `SKIP` for all if `TEST_DATABASE_URL` is unset — that's fine, it means the compile step already caught wiring bugs).

Run: `cd apps/ftp && go build ./...`
Expected: builds cleanly regardless of DB availability.

- [ ] **Step 8: Commit**

```bash
git add apps/ftp/internal/db/migrations/0003_add_protocol_access.up.sql \
        apps/ftp/internal/db/migrations/0003_add_protocol_access.down.sql \
        apps/ftp/internal/db/queries.go apps/ftp/internal/db/queries_test.go
git commit -m "feat(ftp): add ftp_enabled/sftp_enabled/sftp_public_key to ftp_users"
```

---

## Task 2: Config — `SFTP_ENABLED`, `SFTP_LISTEN`, `SFTP_HOST_KEY`

**Files:**
- Modify: `apps/ftp/internal/config/config.go`
- Test: `apps/ftp/internal/config/config_test.go`

- [ ] **Step 1: Write the failing test**

Append to `apps/ftp/internal/config/config_test.go`:

```go
func TestLoadSFTPDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@h:5432/d")
	t.Setenv("SFTP_ENABLED", "")
	t.Setenv("SFTP_LISTEN", "")
	t.Setenv("SFTP_HOST_KEY", "")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.SFTPEnabled {
		t.Error("expected SFTPEnabled=false by default")
	}
	if c.SFTPListen != ":2222" {
		t.Errorf("SFTPListen=%q", c.SFTPListen)
	}
	if c.SFTPHostKey != "" {
		t.Errorf("SFTPHostKey=%q, want empty", c.SFTPHostKey)
	}
}

func TestLoadSFTPEnvOverrides(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@h:5432/d")
	t.Setenv("SFTP_ENABLED", "true")
	t.Setenv("SFTP_LISTEN", ":3333")
	t.Setenv("SFTP_HOST_KEY", "c29tZS1iYXNlNjQtdmFsdWU=")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !c.SFTPEnabled {
		t.Error("expected SFTPEnabled=true")
	}
	if c.SFTPListen != ":3333" {
		t.Errorf("SFTPListen=%q", c.SFTPListen)
	}
	if c.SFTPHostKey != "c29tZS1iYXNlNjQtdmFsdWU=" {
		t.Errorf("SFTPHostKey=%q", c.SFTPHostKey)
	}
}
```

- [ ] **Step 2: Run to verify it fails to compile**

Run: `cd apps/ftp && go test ./internal/config/...`
Expected: FAIL to compile — `c.SFTPEnabled undefined (type *Config has no field or method SFTPEnabled)`.

- [ ] **Step 3: Add the fields and env parsing**

In `apps/ftp/internal/config/config.go`, add to the `Config` struct (after `FTPProxyProtocol`):

```go
	SFTPEnabled  bool
	SFTPListen   string
	SFTPHostKey  string
```

Add to the `c := &Config{...}` literal in `Load()` (after `FTPProxyProtocol`):

```go
		SFTPEnabled:               getenv("SFTP_ENABLED", "false") == "true",
		SFTPListen:                getenv("SFTP_LISTEN", ":2222"),
		SFTPHostKey:               os.Getenv("SFTP_HOST_KEY"),
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd apps/ftp && go test ./internal/config/... -v`
Expected: PASS, all `TestLoad*` tests including the two new ones.

- [ ] **Step 5: Commit**

```bash
git add apps/ftp/internal/config/config.go apps/ftp/internal/config/config_test.go
git commit -m "feat(ftp): add SFTP_ENABLED/SFTP_LISTEN/SFTP_HOST_KEY config"
```

---

## Task 3: FTP touchpoint — gate on `ftp_enabled`

**Files:**
- Modify: `apps/ftp/internal/ftpserver/server.go`
- Test: `apps/ftp/internal/ftpserver/server_test.go`

- [ ] **Step 1: Write the failing test**

Append to `apps/ftp/internal/ftpserver/server_test.go`:

```go
func TestFtpAccessDenied(t *testing.T) {
	if ftpAccessDenied(&db.FTPUser{FTPEnabled: true}) {
		t.Error("expected access allowed when FTPEnabled=true")
	}
	if !ftpAccessDenied(&db.FTPUser{FTPEnabled: false}) {
		t.Error("expected access denied when FTPEnabled=false")
	}
}
```

Add `"github.com/example/cos-ftp-server/internal/db"` to this test file's imports (it isn't imported there yet).

- [ ] **Step 2: Run to verify it fails to compile**

Run: `cd apps/ftp && go test ./internal/ftpserver/...`
Expected: FAIL to compile — `undefined: ftpAccessDenied`.

- [ ] **Step 3: Add the guard**

In `apps/ftp/internal/ftpserver/server.go`, add this helper near `AuthUser`:

```go
// ftpAccessDenied reports whether u is blocked from the FTP touchpoint
// specifically. The account-wide kill switch (u.Enabled) is already
// enforced by core.DBAuthenticator before AuthUser sees u at all.
func ftpAccessDenied(u *db.FTPUser) bool {
	return !u.FTPEnabled
}
```

Modify `AuthUser` (currently at `server.go:138-152`) — insert the gate right after the `Authenticate` error check:

```go
func (s *Server) AuthUser(cc ftpserver.ClientContext, user, pass string) (ftpserver.ClientDriver, error) {
	if s.Authenticator == nil {
		return nil, fmt.Errorf("ftpserver: auth failed for user %q", user)
	}
	u, err := s.Authenticator.Authenticate(user, pass)
	if err != nil {
		return nil, fmt.Errorf("ftpserver: auth failed for user %q", user)
	}
	if ftpAccessDenied(u) {
		s.Audit.Log(audit.Event{
			Username: user, Action: "LOGIN", Success: false,
			Detail: map[string]any{"reason": "ftp_disabled"},
		})
		return nil, fmt.Errorf("ftpserver: auth failed for user %q", user)
	}
	if s.NewDriver == nil {
		return nil, fmt.Errorf("ftpserver: no driver factory configured")
	}
	cc.SetExtra(u.Username)
	clientIP, _, _ := net.SplitHostPort(cc.RemoteAddr().String())
	return s.NewDriver(u, clientIP)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd apps/ftp && go test ./internal/ftpserver/... -v`
Expected: PASS, including the existing `TestServerStartsAndStops` and `TestAuthTLSRejectedWhenNotConfigured`.

- [ ] **Step 5: Commit**

```bash
git add apps/ftp/internal/ftpserver/server.go apps/ftp/internal/ftpserver/server_test.go
git commit -m "feat(ftp): deny FTP login when ftp_enabled is false"
```

---

## Task 4: Add `github.com/pkg/sftp` dependency

**Files:**
- Modify: `apps/ftp/go.mod`, `apps/ftp/go.sum`

- [ ] **Step 1: Add the dependency**

Run:
```bash
cd apps/ftp && go get github.com/pkg/sftp
```

- [ ] **Step 2: Verify the module builds**

Run: `cd apps/ftp && go build ./...`
Expected: succeeds (no source uses the new import yet, this just confirms `go.mod`/`go.sum` resolved cleanly).

- [ ] **Step 3: Commit**

```bash
git add apps/ftp/go.mod apps/ftp/go.sum
git commit -m "chore(ftp): add github.com/pkg/sftp dependency"
```

---

## Task 5: SSH host key loader

**Files:**
- Create: `apps/ftp/internal/sftpserver/hostkey.go`
- Test: `apps/ftp/internal/sftpserver/hostkey_test.go`

- [ ] **Step 1: Write the failing test**

Create `apps/ftp/internal/sftpserver/hostkey_test.go`:

```go
package sftpserver

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"testing"
)

func TestLoadOrGenerateHostKey_Empty(t *testing.T) {
	signer, err := LoadOrGenerateHostKey("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if signer == nil {
		t.Fatal("expected a non-nil signer")
	}
}

func TestLoadOrGenerateHostKey_InvalidBase64(t *testing.T) {
	if _, err := LoadOrGenerateHostKey("not-valid-base64!!!", nil); err == nil {
		t.Fatal("expected error for invalid base64")
	}
}

func TestLoadOrGenerateHostKey_InvalidPEM(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString([]byte("not a pem block"))
	if _, err := LoadOrGenerateHostKey(b64, nil); err == nil {
		t.Fatal("expected error for undecodable PEM")
	}
}

func TestLoadOrGenerateHostKey_ValidPEM(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	b64 := base64.StdEncoding.EncodeToString(pem.EncodeToMemory(block))

	signer, err := LoadOrGenerateHostKey(b64, nil)
	if err != nil {
		t.Fatal(err)
	}
	if signer == nil {
		t.Fatal("expected a non-nil signer")
	}
}
```

- [ ] **Step 2: Run to verify it fails to compile**

Run: `cd apps/ftp && go test ./internal/sftpserver/...`
Expected: FAIL — `undefined: LoadOrGenerateHostKey` (package `sftpserver` doesn't exist yet, so this creates it).

- [ ] **Step 3: Implement**

Create `apps/ftp/internal/sftpserver/hostkey.go`:

```go
package sftpserver

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"

	"golang.org/x/crypto/ssh"
)

// LoadOrGenerateHostKey builds the SFTP server's SSH identity from a
// base64-encoded PEM private key (the SFTP_HOST_KEY env var). If b64PEM is
// empty, an ephemeral ed25519 key is generated instead — only its
// fingerprint is logged (never key material), since it changes on every
// restart and operators should notice.
func LoadOrGenerateHostKey(b64PEM string, log *slog.Logger) (ssh.Signer, error) {
	if b64PEM == "" {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("sftpserver: generate host key: %w", err)
		}
		signer, err := ssh.NewSignerFromSigner(priv)
		if err != nil {
			return nil, fmt.Errorf("sftpserver: wrap generated host key: %w", err)
		}
		if log != nil {
			log.Warn("sftp: SFTP_HOST_KEY not set, using an ephemeral host key (fingerprint changes on every restart)",
				"fingerprint", ssh.FingerprintSHA256(signer.PublicKey()))
		}
		return signer, nil
	}
	pemBytes, err := base64.StdEncoding.DecodeString(b64PEM)
	if err != nil {
		return nil, fmt.Errorf("sftpserver: SFTP_HOST_KEY: invalid base64: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(pemBytes)
	if err != nil {
		return nil, fmt.Errorf("sftpserver: SFTP_HOST_KEY: parse: %w", err)
	}
	return signer, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd apps/ftp && go test ./internal/sftpserver/... -v`
Expected: PASS — all four `TestLoadOrGenerateHostKey_*` tests.

- [ ] **Step 5: Commit**

```bash
git add apps/ftp/internal/sftpserver/hostkey.go apps/ftp/internal/sftpserver/hostkey_test.go
git commit -m "feat(ftp): add SFTP host key loader (env base64 PEM or ephemeral)"
```

---

## Task 6: `afero.Fs` → `sftp.Handlers` adapter

**Files:**
- Create: `apps/ftp/internal/fsdriver/sftp_handlers.go`
- Test: `apps/ftp/internal/fsdriver/sftp_handlers_test.go`

- [ ] **Step 1: Write the failing test**

Create `apps/ftp/internal/fsdriver/sftp_handlers_test.go`:

```go
package fsdriver

import (
	"io"
	"os"
	"testing"

	"github.com/pkg/sftp"
	"github.com/spf13/afero"
)

func TestSFTPHandlers_WriteReadRoundtrip(t *testing.T) {
	fs := afero.NewMemMapFs()
	h := NewSFTPHandlers(fs)

	w, err := h.FilePut.Filewrite(&sftp.Request{Method: "Put", Filepath: "/hello.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteAt([]byte("hello"), 0); err != nil {
		t.Fatal(err)
	}
	if closer, ok := w.(io.Closer); ok {
		if err := closer.Close(); err != nil {
			t.Fatal(err)
		}
	}

	r, err := h.FileGet.Fileread(&sftp.Request{Method: "Get", Filepath: "/hello.txt"})
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := r.ReadAt(buf, 0); err != nil && err != io.EOF {
		t.Fatal(err)
	}
	if string(buf) != "hello" {
		t.Fatalf("got %q, want %q", buf, "hello")
	}
	if closer, ok := r.(io.Closer); ok {
		_ = closer.Close()
	}
}

func TestSFTPHandlers_MkdirRenameRemove(t *testing.T) {
	fs := afero.NewMemMapFs()
	h := NewSFTPHandlers(fs)

	if err := h.FileCmd.Filecmd(&sftp.Request{Method: "Mkdir", Filepath: "/dir"}); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Stat("/dir"); err != nil {
		t.Fatalf("expected /dir to exist: %v", err)
	}

	if err := afero.WriteFile(fs, "/dir/a.txt", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.FileCmd.Filecmd(&sftp.Request{Method: "Rename", Filepath: "/dir/a.txt", Target: "/dir/b.txt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Stat("/dir/b.txt"); err != nil {
		t.Fatalf("expected renamed file: %v", err)
	}

	if err := h.FileCmd.Filecmd(&sftp.Request{Method: "Remove", Filepath: "/dir/b.txt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Stat("/dir/b.txt"); err == nil {
		t.Fatal("expected file to be removed")
	}
}

func TestSFTPHandlers_ListDirectory(t *testing.T) {
	fs := afero.NewMemMapFs()
	h := NewSFTPHandlers(fs)
	if err := afero.WriteFile(fs, "/a.txt", []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fs, "/b.txt", []byte("2"), 0o644); err != nil {
		t.Fatal(err)
	}

	lister, err := h.FileList.Filelist(&sftp.Request{Method: "List", Filepath: "/"})
	if err != nil {
		t.Fatal(err)
	}
	dst := make([]os.FileInfo, 10)
	n, err := lister.ListAt(dst, 0)
	if err != nil && err != io.EOF {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 entries, got %d", n)
	}
}
```

- [ ] **Step 2: Run to verify it fails to compile**

Run: `cd apps/ftp && go test ./internal/fsdriver/...`
Expected: FAIL to compile — `undefined: NewSFTPHandlers`.

- [ ] **Step 3: Implement**

Create `apps/ftp/internal/fsdriver/sftp_handlers.go`:

```go
package fsdriver

import (
	"io"
	"os"

	"github.com/pkg/sftp"
	"github.com/spf13/afero"
)

// SFTPHandlers adapts an afero.Fs — the same per-user mount FTP uses (COS,
// local, or the AuditFS wrapper around either) — to github.com/pkg/sftp's
// request-server handler interfaces, so one mount can serve both protocols.
type SFTPHandlers struct {
	fs afero.Fs
}

var (
	_ sftp.FileReader = (*SFTPHandlers)(nil)
	_ sftp.FileWriter = (*SFTPHandlers)(nil)
	_ sftp.FileCmder  = (*SFTPHandlers)(nil)
	_ sftp.FileLister = (*SFTPHandlers)(nil)
)

// NewSFTPHandlers returns an sftp.Handlers bundle backed by fs, for
// sftp.NewRequestServer.
func NewSFTPHandlers(fs afero.Fs) sftp.Handlers {
	h := &SFTPHandlers{fs: fs}
	return sftp.Handlers{
		FileGet:  h,
		FilePut:  h,
		FileCmd:  h,
		FileList: h,
	}
}

func (h *SFTPHandlers) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	f, err := h.fs.Open(r.Filepath)
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (h *SFTPHandlers) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	f, err := h.fs.OpenFile(r.Filepath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (h *SFTPHandlers) Filecmd(r *sftp.Request) error {
	switch r.Method {
	case "Setstat":
		return nil
	case "Rename":
		return h.fs.Rename(r.Filepath, r.Target)
	case "Rmdir":
		return h.fs.RemoveAll(r.Filepath)
	case "Mkdir":
		return h.fs.MkdirAll(r.Filepath, 0o755)
	case "Remove":
		return h.fs.Remove(r.Filepath)
	default:
		return sftp.ErrSSHFxOpUnsupported
	}
}

func (h *SFTPHandlers) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	switch r.Method {
	case "List":
		infos, err := afero.ReadDir(h.fs, r.Filepath)
		if err != nil {
			return nil, err
		}
		return listerAt(infos), nil
	case "Stat", "Lstat":
		info, err := h.fs.Stat(r.Filepath)
		if err != nil {
			return nil, err
		}
		return listerAt([]os.FileInfo{info}), nil
	default:
		return nil, sftp.ErrSSHFxOpUnsupported
	}
}

// listerAt implements sftp.ListerAt over an already-fetched slice.
type listerAt []os.FileInfo

func (l listerAt) ListAt(dst []os.FileInfo, offset int64) (int, error) {
	if offset >= int64(len(l)) {
		return 0, io.EOF
	}
	n := copy(dst, l[offset:])
	if n < len(dst) {
		return n, io.EOF
	}
	return n, nil
}
```

If `go build` reports a different name for the "unsupported operation" sentinel
error in the resolved `github.com/pkg/sftp` version (constant naming has
varied across releases, e.g. `ErrSshFxOpUnsupported` vs `ErrSSHFxOpUnsupported`),
use `go doc github.com/pkg/sftp` to find the exact exported name and update
both occurrences in `Filecmd`/`Filelist` accordingly.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd apps/ftp && go test ./internal/fsdriver/... -v`
Expected: PASS — all `TestSFTPHandlers_*` tests, and the existing `fsdriver` tests still pass.

- [ ] **Step 5: Commit**

```bash
git add apps/ftp/internal/fsdriver/sftp_handlers.go apps/ftp/internal/fsdriver/sftp_handlers_test.go
git commit -m "feat(ftp): adapt afero.Fs to pkg/sftp request-server handlers"
```

---

## Task 7: `sftpserver.Server` (the touchpoint)

**Files:**
- Create: `apps/ftp/internal/sftpserver/server.go`
- Test: `apps/ftp/internal/sftpserver/server_test.go`

- [ ] **Step 1: Write the failing tests**

Create `apps/ftp/internal/sftpserver/server_test.go`:

```go
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

func (f *fakeAuthenticator) Authenticate(user, pass string) (*db.FTPUser, error) {
	return f.user, f.err
}

type fakeConnMetadata struct{ user string }

func (f fakeConnMetadata) User() string          { return f.user }
func (f fakeConnMetadata) SessionID() []byte     { return nil }
func (f fakeConnMetadata) ClientVersion() []byte { return nil }
func (f fakeConnMetadata) ServerVersion() []byte { return nil }
func (f fakeConnMetadata) RemoteAddr() net.Addr  { return nil }
func (f fakeConnMetadata) LocalAddr() net.Addr   { return nil }

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
```

- [ ] **Step 2: Run to verify it fails to compile**

Run: `cd apps/ftp && go test ./internal/sftpserver/...`
Expected: FAIL to compile — `undefined: Server`, `s.passwordCallback undefined`, `undefined: errAuthFailed`.

- [ ] **Step 3: Implement**

Create `apps/ftp/internal/sftpserver/server.go`:

```go
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
	u, err := s.Authenticator.Authenticate(conn.User(), string(pass))
	if err != nil {
		return nil, errAuthFailed
	}
	if !u.SFTPEnabled {
		s.Audit.Log(audit.Event{
			Username: conn.User(), Action: "LOGIN", Success: false,
			Detail: map[string]any{"reason": "sftp_disabled", "protocol": "sftp"},
		})
		return nil, errAuthFailed
	}
	return &ssh.Permissions{}, nil
}

func (s *Server) publicKeyCallback(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	if s.LookupUser == nil {
		return nil, errAuthFailed
	}
	u, err := s.LookupUser(context.Background(), conn.User())
	if err != nil || u == nil {
		return nil, errAuthFailed
	}
	if !u.Enabled || !u.SFTPEnabled || u.SFTPPublicKey == "" {
		return nil, errAuthFailed
	}
	stored, _, _, _, err := ssh.ParseAuthorizedKey([]byte(u.SFTPPublicKey))
	if err != nil {
		return nil, errAuthFailed
	}
	if !bytes.Equal(stored.Marshal(), key.Marshal()) {
		s.Audit.Log(audit.Event{
			Username: conn.User(), Action: "LOGIN", Success: false,
			Detail: map[string]any{"reason": "pubkey_mismatch", "protocol": "sftp"},
		})
		return nil, errAuthFailed
	}
	s.Audit.Log(audit.Event{
		Username: conn.User(), Action: "LOGIN", Success: true,
		Detail: map[string]any{"protocol": "sftp", "method": "publickey"},
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
	auditedFS := fsdriver.NewAuditFS(fs, s.Audit, u.Username)
	handlers := fsdriver.NewSFTPHandlers(auditedFS)
	reqServer := sftp.NewRequestServer(ch, handlers)
	_ = reqServer.Serve()
	_ = reqServer.Close()
	s.Audit.Log(audit.Event{Username: u.Username, Action: "LOGOUT", Success: true})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd apps/ftp && go test ./internal/sftpserver/... -v`
Expected: PASS — all `TestPasswordCallback_*`, `TestPublicKeyCallback_*`, and `TestServerStartsAndStops`.

- [ ] **Step 5: Run the full backend test suite**

Run: `cd apps/ftp && go build ./... && go test ./... -race`
Expected: builds and all tests pass (DB-gated tests `SKIP` without `TEST_DATABASE_URL`, as before).

- [ ] **Step 6: Commit**

```bash
git add apps/ftp/internal/sftpserver/server.go apps/ftp/internal/sftpserver/server_test.go
git commit -m "feat(ftp): add SFTP touchpoint (password + public-key auth)"
```

---

## Task 8: Wire the SFTP touchpoint into `main.go`

**Files:**
- Modify: `apps/ftp/cmd/ftp-server/main.go`

- [ ] **Step 1: Add the SFTP server alongside the FTP server**

In `apps/ftp/cmd/ftp-server/main.go`, add to the import block:

```go
	"github.com/example/cos-ftp-server/internal/sftpserver"
```

After the existing block that starts `srv` (the `if err := srv.Start(ctx); err != nil { return err }` and its following TLS-cert-expiry log, i.e. right before the `// Admin API + metrics...` comment), add:

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
			// Same *core.DBAuthenticator instance srv.Authenticator uses
			// (constructed above, in the ftpserver.Server{} literal) —
			// per the design, brute-force lockout is shared across both
			// protocols for the same username.
			Authenticator: srv.Authenticator,
			LookupUser: func(ctx context.Context, username string) (*db.FTPUser, error) {
				return db.GetFTPUserPublicKey(ctx, pool, username)
			},
			Storage: storage,
			Audit:   auditLog,
			Logger:  log,
		}
		if err := sftpSrv.Start(ctx); err != nil {
			return fmt.Errorf("sftp: %w", err)
		}
		log.Info("sftp: listening", "addr", cfg.SFTPListen)
	}
```

Update the shutdown sequence — replace:

```go
	<-ctx.Done()
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	_ = adminSrv.Shutdown(shutdownCtx)
	select {
	case err := <-adminErrCh:
		if err != nil {
			return fmt.Errorf("admin: %w", err)
		}
	default:
	}
	return srv.Stop()
```

with:

```go
	<-ctx.Done()
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	_ = adminSrv.Shutdown(shutdownCtx)
	select {
	case err := <-adminErrCh:
		if err != nil {
			return fmt.Errorf("admin: %w", err)
		}
	default:
	}
	if sftpSrv != nil {
		_ = sftpSrv.Stop()
	}
	return srv.Stop()
```

- [ ] **Step 2: Build**

Run: `cd apps/ftp && go build ./...`
Expected: builds cleanly.

- [ ] **Step 3: Manual smoke test**

Run (requires a reachable Postgres, adjust `DATABASE_URL`):
```bash
cd apps/ftp && SFTP_ENABLED=true DATABASE_URL=postgres://user:pass@localhost:5432/ftp go run ./cmd/ftp-server
```
Expected: log line `sftp: listening addr=:2222` alongside the existing FTP/admin startup logs, and a `sftp: SFTP_HOST_KEY not set, using an ephemeral host key...` warning. `Ctrl+C` shuts down cleanly (no panics, no goroutine-leak warnings under `-race` if you run the equivalent through `go run -race`).

- [ ] **Step 4: Commit**

```bash
git add apps/ftp/cmd/ftp-server/main.go
git commit -m "feat(ftp): start the SFTP touchpoint when SFTP_ENABLED is true"
```

---

## Task 9: Admin API — per-protocol flags on create/update, SFTP key endpoint

**Files:**
- Modify: `apps/ftp/internal/admin/users_api.go`
- Modify: `apps/ftp/internal/admin/api.go`
- Test: Create `apps/ftp/internal/admin/users_api_test.go`

- [ ] **Step 1: Write the failing tests**

Create `apps/ftp/internal/admin/users_api_test.go`:

```go
package admin

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/ssh"

	"github.com/example/cos-ftp-server/internal/db"
)

func TestSetUserSFTPKey(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := db.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `DELETE FROM ftp_users WHERE username = 'sftp-key-api-test'`)

	u := &db.FTPUser{Username: "sftp-key-api-test", PasswordHash: "x", RootFolder: "/sftp-key-api-test", COSBucket: "b", Enabled: true}
	id, err := db.CreateFTPUser(ctx, pool, u)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	defer func() { _ = db.SoftDeleteFTPUser(ctx, pool, id) }()

	api := &API{Pool: pool}
	r := chi.NewRouter()
	r.Post("/api/v1/users/{username}/sftp-key", api.setUserSFTPKey)

	_, pub, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	validKey := string(ssh.MarshalAuthorizedKey(sshPub))

	body, _ := json.Marshal(map[string]string{"public_key": validKey})
	req := httptest.NewRequest("POST", "/api/v1/users/sftp-key-api-test/sftp-key", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("valid key: status %d body %s", w.Code, w.Body.String())
	}
	got, err := db.GetFTPUserPublicKey(ctx, pool, "sftp-key-api-test")
	if err != nil {
		t.Fatal(err)
	}
	if got.SFTPPublicKey == "" {
		t.Fatal("expected key to be stored")
	}

	body, _ = json.Marshal(map[string]string{"public_key": "not-a-key"})
	req = httptest.NewRequest("POST", "/api/v1/users/sftp-key-api-test/sftp-key", bytes.NewReader(body))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid key: expected 400, got %d body %s", w.Code, w.Body.String())
	}

	body, _ = json.Marshal(map[string]string{"public_key": ""})
	req = httptest.NewRequest("POST", "/api/v1/users/sftp-key-api-test/sftp-key", bytes.NewReader(body))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("clear key: status %d body %s", w.Code, w.Body.String())
	}
	got2, err := db.GetFTPUserPublicKey(ctx, pool, "sftp-key-api-test")
	if err != nil {
		t.Fatal(err)
	}
	if got2.SFTPPublicKey != "" {
		t.Errorf("expected cleared key, got %q", got2.SFTPPublicKey)
	}
}

func TestCreateUser_ProtocolFlagsDefaults(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := db.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `DELETE FROM ftp_users WHERE username = 'create-flags-test'`)

	api := &API{Pool: pool}
	r := chi.NewRouter()
	r.Post("/api/v1/users", api.createUser)

	body, _ := json.Marshal(map[string]any{
		"username": "create-flags-test", "root_folder": "/create-flags-test",
		"password": "Passw0rd1", "enabled": true,
	})
	req := httptest.NewRequest("POST", "/api/v1/users", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var created db.FTPUser
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.SoftDeleteFTPUser(ctx, pool, created.ID) }()
	if !created.FTPEnabled {
		t.Error("expected FTPEnabled default true when omitted")
	}
	if created.SFTPEnabled {
		t.Error("expected SFTPEnabled default false when omitted")
	}
}

func TestUpdateUser_ProtocolFlagsPreservedWhenOmitted(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := db.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `DELETE FROM ftp_users WHERE username = 'update-flags-test'`)

	u := &db.FTPUser{Username: "update-flags-test", PasswordHash: "x", RootFolder: "/update-flags-test",
		COSBucket: "b", Enabled: true, FTPEnabled: true, SFTPEnabled: true}
	id, err := db.CreateFTPUser(ctx, pool, u)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.SoftDeleteFTPUser(ctx, pool, id) }()

	api := &API{Pool: pool}
	r := chi.NewRouter()
	r.Patch("/api/v1/users/{username}", api.updateUser)

	body, _ := json.Marshal(map[string]any{"root_folder": "/update-flags-test", "enabled": true})
	req := httptest.NewRequest("PATCH", "/api/v1/users/update-flags-test", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var updated db.FTPUser
	if err := json.Unmarshal(w.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if !updated.SFTPEnabled {
		t.Error("expected SFTPEnabled preserved as true when omitted from the PATCH body")
	}
}
```

Note: `api := &API{Pool: pool}` leaves `Audit` nil — `audit.Logger.Log` is nil-receiver-safe (see `internal/audit/audit.go`), so handler code that calls `a.Audit.Log(...)` doesn't need a real audit sink in these tests.

- [ ] **Step 2: Run to verify it fails**

Run: `cd apps/ftp && go test ./internal/admin/...`
Expected: FAIL to compile — `api.setUserSFTPKey undefined`.

- [ ] **Step 3: Implement `setUserSFTPKey` and update create/update**

In `apps/ftp/internal/admin/users_api.go`, add `"golang.org/x/crypto/ssh"` to the imports, then add:

```go
func (a *API) setUserSFTPKey(w http.ResponseWriter, r *http.Request) {
	username := chi.URLParam(r, "username")
	var body struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	body.PublicKey = strings.TrimSpace(body.PublicKey)
	if body.PublicKey != "" {
		if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(body.PublicKey)); err != nil {
			http.Error(w, "invalid public key: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	existing, err := db.GetFTPUserByUsername(r.Context(), a.Pool, username)
	if errors.Is(err, db.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	if err := db.SetFTPUserSFTPKey(r.Context(), a.Pool, existing.ID, body.PublicKey); err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	a.Audit.Log(audit.Event{Username: CurrentUser(r), Action: "ADMIN_SET_SFTP_KEY", Path: username, Success: true})
	w.WriteHeader(http.StatusNoContent)
}
```

Replace `createUser`'s body struct and the `u := &db.FTPUser{...}` construction:

```go
func (a *API) createUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username    string `json:"username"`
		RootFolder  string `json:"root_folder"`
		Password    string `json:"password"`
		Enabled     bool   `json:"enabled"`
		FTPEnabled  *bool  `json:"ftp_enabled"`
		SFTPEnabled *bool  `json:"sftp_enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	body.RootFolder = strings.TrimSpace(body.RootFolder)
	if body.Username == "" || body.RootFolder == "" {
		http.Error(w, "username and root_folder are required", http.StatusBadRequest)
		return
	}
	body.RootFolder = normalizeRootFolder(body.RootFolder)
	if err := auth.ValidatePassword(body.Password); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if a.COSClient != nil {
		key := strings.TrimPrefix(body.RootFolder, "/") + "/"
		if err := a.COSClient.Put(r.Context(), key, bytes.NewReader(nil), 0); err != nil {
			http.Error(w, friendlyCOSErr("create root folder", err), http.StatusBadGateway)
			return
		}
	}

	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	ftpEnabled := true
	if body.FTPEnabled != nil {
		ftpEnabled = *body.FTPEnabled
	}
	sftpEnabled := false
	if body.SFTPEnabled != nil {
		sftpEnabled = *body.SFTPEnabled
	}
	u := &db.FTPUser{
		Username:     body.Username,
		PasswordHash: hash,
		RootFolder:   body.RootFolder,
		COSBucket:    a.COSBucket,
		COSRegion:    a.COSRegion,
		Enabled:      body.Enabled,
		MaxSessions:  5,
		FTPEnabled:   ftpEnabled,
		SFTPEnabled:  sftpEnabled,
	}
	if _, err := db.CreateFTPUser(r.Context(), a.Pool, u); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			http.Error(w, "username already exists", http.StatusConflict)
			return
		}
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	created, err := db.GetFTPUserByUsername(r.Context(), a.Pool, body.Username)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	a.Audit.Log(audit.Event{Username: CurrentUser(r), Action: "ADMIN_CREATE_USER", Path: created.Username, Success: true})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(created)
}
```

Replace `updateUser`'s body struct and the fields it applies:

```go
func (a *API) updateUser(w http.ResponseWriter, r *http.Request) {
	username := chi.URLParam(r, "username")
	var body struct {
		RootFolder  string `json:"root_folder"`
		Enabled     bool   `json:"enabled"`
		FTPEnabled  *bool  `json:"ftp_enabled"`
		SFTPEnabled *bool  `json:"sftp_enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	body.RootFolder = strings.TrimSpace(body.RootFolder)
	if body.RootFolder == "" {
		http.Error(w, "root_folder is required", http.StatusBadRequest)
		return
	}
	body.RootFolder = normalizeRootFolder(body.RootFolder)
	existing, err := db.GetFTPUserByUsername(r.Context(), a.Pool, username)
	if errors.Is(err, db.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	existing.RootFolder = body.RootFolder
	existing.Enabled = body.Enabled
	if body.FTPEnabled != nil {
		existing.FTPEnabled = *body.FTPEnabled
	}
	if body.SFTPEnabled != nil {
		existing.SFTPEnabled = *body.SFTPEnabled
	}
	if err := db.UpdateFTPUser(r.Context(), a.Pool, existing); err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	updated, err := db.GetFTPUserByUsername(r.Context(), a.Pool, username)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	a.Audit.Log(audit.Event{Username: CurrentUser(r), Action: "ADMIN_UPDATE_USER", Path: username, Success: true})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(updated)
}
```

- [ ] **Step 4: Register the route**

In `apps/ftp/internal/admin/api.go`, add inside the admin-only route group in `Routes()` (alongside `resetUserPassword`):

```go
			r.Post("/api/v1/users/{username}/sftp-key", a.setUserSFTPKey)
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd apps/ftp && go test ./internal/admin/... -v -run 'SFTPKey|ProtocolFlags'`
Expected: PASS (or `SKIP` without `TEST_DATABASE_URL`).

Run: `cd apps/ftp && go build ./... && go vet ./...`
Expected: clean.

- [ ] **Step 6: Full test suite + lint**

Run: `cd apps/ftp && make test`
Expected: PASS (DB-gated tests skip without `TEST_DATABASE_URL`; everything else runs).

If `golangci-lint` is installed, also run: `cd apps/ftp && make lint`

- [ ] **Step 7: Commit**

```bash
git add apps/ftp/internal/admin/users_api.go apps/ftp/internal/admin/api.go apps/ftp/internal/admin/users_api_test.go
git commit -m "feat(ftp): admin API for per-protocol access flags and SFTP public key"
```

---

## Done criteria

- `go build ./...` and `go test ./... -race` pass from `apps/ftp/`.
- With `TEST_DATABASE_URL` set against a real Postgres, all new DB/API tests pass (not just skip).
- `SFTP_ENABLED=true` starts a second listener on `SFTP_LISTEN` (default `:2222`) alongside the FTP listener, using an existing FTP user's username/password.
- Setting a user's `sftp_public_key` via `POST /api/v1/users/{username}/sftp-key` lets that user authenticate over SFTP with the matching private key, with no password prompt.
- Flipping `ftp_enabled`/`sftp_enabled` to `false` for a user blocks that protocol for that user while the other protocol (and the account's `enabled` master switch) is unaffected.
