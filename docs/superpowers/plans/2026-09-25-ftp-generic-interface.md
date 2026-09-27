# Generic Touchpoint / Core / Object-Storage Interfaces Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refactor `apps/ftp` so the FTP touchpoint and COS/local storage backends sit behind three explicit contracts (`core.Authenticator`, `core.ObjectStorage`, `core.Touchpoint`), so a future touchpoint (SFTP/WebDAV) or storage plugin (S3/GCS) needs zero changes to `internal/core`, `internal/admin`, or `internal/db`.

**Architecture:** New `internal/core` package defines the three contracts. `internal/fsdriver` gains a plugin layer (`COSPlugin`, `LocalPlugin`) selected at startup by a namespaced env value (`STORAGE_BACKEND=cos.objectstorage.plugin` / `local.objectstorage.plugin`). `cmd/ftp-server/main.go` and `internal/admin` are rewired to go through the plugin instead of constructing a COS driver directly.

**Tech Stack:** Go 1.26, `github.com/spf13/afero`, `github.com/fclairamb/ftpserverlib`, `github.com/tencentyun/cos-go-sdk-v5`.

**Design doc:** `docs/superpowers/specs/2026-09-25-ftp-generic-interface-design.md`

---

## Task 1: Core contracts package

**Files:**
- Create: `apps/ftp/internal/core/contracts.go`

- [ ] **Step 1: Write the contracts file**

```go
package core

import (
	"context"

	"github.com/spf13/afero"

	"github.com/example/cos-ftp-server/internal/db"
)

// Authenticator verifies credentials against the user store.
type Authenticator interface {
	Authenticate(user, pass string) (*db.FTPUser, error)
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
```

No test for this step: these are bare interface declarations with no
runtime behavior to assert. Compile-time assertions that concrete types
satisfy them are written in Tasks 2 and 4.

- [ ] **Step 2: Build to confirm the new package compiles**

Run: `cd apps/ftp && go build ./...`
Expected: exits 0, no output.

- [ ] **Step 3: Commit**

```bash
git add apps/ftp/internal/core/contracts.go
git commit -m "feat(ftp): add core.Authenticator/ObjectStorage/Touchpoint contracts"
```

---

## Task 2: Move DBAuthenticator into core; wire Server and main.go to it

**Files:**
- Create: `apps/ftp/internal/core/authenticator.go`
- Delete: `apps/ftp/internal/ftpserver/auth_driver.go`
- Modify: `apps/ftp/internal/ftpserver/server.go` (lines 23-26, 43-77)
- Modify: `apps/ftp/cmd/ftp-server/main.go` (imports, `Authenticator:` field)

- [ ] **Step 1: Create `internal/core/authenticator.go` with the moved `DBAuthenticator`**

```go
package core

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

// DBAuthenticator looks up ftp_users in PostgreSQL and verifies bcrypt
// passwords. Implements Authenticator.
type DBAuthenticator struct {
	Pool    *pgxpool.Pool
	Lockout *auth.Lockout
	Audit   *audit.Logger
}

var _ Authenticator = (*DBAuthenticator)(nil)

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
	if err := db.SetFTPUserLastLogin(dbCtx, a.Pool, u.ID); err != nil {
		a.Audit.Log(audit.Event{
			Username: user, Action: "LOGIN", Success: false,
			Detail: map[string]any{"reason": "last_login_update_failed", "err": err.Error()},
		})
	}
	a.Audit.Log(audit.Event{
		Username: user, Action: "LOGIN", Success: true,
	})
	return u, nil
}
```

- [ ] **Step 2: Delete the old file**

```bash
rm apps/ftp/internal/ftpserver/auth_driver.go
```

- [ ] **Step 3: Update `internal/ftpserver/server.go`**

Remove the local `Authenticator` interface (current lines 23-26):

```go
// Authenticator checks user credentials and returns the authenticated user's record.
type Authenticator interface {
	Authenticate(user, pass string) (*db.FTPUser, error)
}
```

Add `"github.com/example/cos-ftp-server/internal/core"` to the import
block (alongside the existing `audit` and `db` imports).

Change the `Server` struct's `Authenticator` field type from the removed
local interface to `core.Authenticator`:

```go
	Authenticator    core.Authenticator
```

Add a compile-time assertion right after the existing two (`var _
ftpserver.MainDriver = ...` / `var _
ftpserver.MainDriverExtensionPassiveWrapper = ...`):

```go
var _ core.Touchpoint = (*Server)(nil)
```

(`Server.Start`/`Server.Stop` already match `core.Touchpoint` — this just
proves it at compile time.)

- [ ] **Step 4: Update `cmd/ftp-server/main.go`**

Add `"github.com/example/cos-ftp-server/internal/core"` to the import
block. Change:

```go
		Authenticator: &ftpserver.DBAuthenticator{
```

to:

```go
		Authenticator: &core.DBAuthenticator{
```

- [ ] **Step 5: Build and run the full existing test suite (regression check — no new behavior in this task)**

Run: `cd apps/ftp && go build ./... && go test ./...`
Expected: builds clean; all tests PASS (no test references the old
`ftpserver.Authenticator`/`ftpserver.DBAuthenticator` names — confirmed by
grep before writing this plan).

- [ ] **Step 6: Commit**

```bash
git add apps/ftp/internal/core/authenticator.go apps/ftp/internal/ftpserver/server.go apps/ftp/cmd/ftp-server/main.go
git rm apps/ftp/internal/ftpserver/auth_driver.go
git commit -m "refactor(ftp): move DBAuthenticator into internal/core, adopt core.Authenticator"
```

---

## Task 3: Config — `STORAGE_BACKEND` / `STORAGE_LOCAL_ROOT`

**Files:**
- Modify: `apps/ftp/internal/config/config.go` (Config struct ~line 31, `Load()` literal ~line 71)
- Modify: `apps/ftp/internal/config/config_test.go` (`TestLoadDefaults`, `TestLoadEnvOverrides`)

- [ ] **Step 1: Add assertions to `TestLoadDefaults` (fails to compile first — fields don't exist yet)**

In `TestLoadDefaults`, after the existing `STSRefreshRatio` check, add:

```go
	if c.StorageBackend != "cos.objectstorage.plugin" {
		t.Errorf("StorageBackend=%q", c.StorageBackend)
	}
	if c.StorageLocalRoot != "./data" {
		t.Errorf("StorageLocalRoot=%q", c.StorageLocalRoot)
	}
```

In `TestLoadEnvOverrides`, add two `t.Setenv` calls after the existing
`t.Setenv("AUDIT_RETENTION_DAYS", "180")` line:

```go
	t.Setenv("STORAGE_BACKEND", "local.objectstorage.plugin")
	t.Setenv("STORAGE_LOCAL_ROOT", "/tmp/ftp-storage-test")
```

and add assertions after the existing `AuditRetentionDays` check:

```go
	if c.StorageBackend != "local.objectstorage.plugin" {
		t.Errorf("StorageBackend=%q", c.StorageBackend)
	}
	if c.StorageLocalRoot != "/tmp/ftp-storage-test" {
		t.Errorf("StorageLocalRoot=%q", c.StorageLocalRoot)
	}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd apps/ftp && go test ./internal/config/... -run TestLoad -v`
Expected: FAIL to compile — `c.StorageBackend undefined (type *Config has no field or method StorageBackend)`.

- [ ] **Step 3: Add the fields to `Config` and `Load()`**

In the `Config` struct, insert after the `COSRegion string` field (before
`STSRefreshRatio float64`):

```go
	StorageBackend         string
	StorageLocalRoot       string
```

In `Load()`'s struct literal, insert after the `COSRegion:` line (before
`STSRefreshRatio:`):

```go
		StorageBackend:             getenv("STORAGE_BACKEND", "cos.objectstorage.plugin"), // must match fsdriver.PluginCOS
		StorageLocalRoot:           getenv("STORAGE_LOCAL_ROOT", "./data"),
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd apps/ftp && go test ./internal/config/... -run TestLoad -v`
Expected: PASS.

- [ ] **Step 5: Run the full config package test suite**

Run: `cd apps/ftp && go test ./internal/config/...`
Expected: PASS (all 5 existing test functions, unaffected by this change).

- [ ] **Step 6: Commit**

```bash
git add apps/ftp/internal/config/config.go apps/ftp/internal/config/config_test.go
git commit -m "feat(ftp): add STORAGE_BACKEND/STORAGE_LOCAL_ROOT config"
```

---

## Task 4: Object storage plugins in `internal/fsdriver`

**Files:**
- Create: `apps/ftp/internal/fsdriver/plugin.go`
- Test: `apps/ftp/internal/fsdriver/plugin_test.go`

This task depends on Task 3's `config.StorageBackend`/`config.StorageLocalRoot`
fields — do not reorder.

- [ ] **Step 1: Write the failing test**

```go
package fsdriver

import (
	"testing"

	"github.com/example/cos-ftp-server/internal/config"
)

func TestNewObjectStorage_UnknownPlugin(t *testing.T) {
	_, err := NewObjectStorage("bogus.objectstorage.plugin", &config.Config{}, nil, nil)
	if err == nil {
		t.Fatal("expected error for unknown plugin id")
	}
}

func TestNewObjectStorage_Local(t *testing.T) {
	dir := t.TempDir()
	storage, err := NewObjectStorage(PluginLocal, &config.Config{StorageLocalRoot: dir}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := storage.(*LocalPlugin); !ok {
		t.Fatalf("got %T, want *LocalPlugin", storage)
	}
}

func TestNewObjectStorage_COS(t *testing.T) {
	storage, err := NewObjectStorage(PluginCOS, &config.Config{COSBucket: "b", COSRegion: "r"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := storage.(*COSPlugin); !ok {
		t.Fatalf("got %T, want *COSPlugin", storage)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd apps/ftp && go test ./internal/fsdriver/... -run TestNewObjectStorage -v`
Expected: FAIL to compile — `undefined: NewObjectStorage` (and `PluginLocal`, `PluginCOS`, `LocalPlugin`, `COSPlugin`).

- [ ] **Step 3: Write `internal/fsdriver/plugin.go`**

```go
package fsdriver

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/spf13/afero"

	"github.com/example/cos-ftp-server/internal/audit"
	"github.com/example/cos-ftp-server/internal/config"
	"github.com/example/cos-ftp-server/internal/core"
	"github.com/example/cos-ftp-server/internal/cos"
)

// Plugin identifiers — value of STORAGE_BACKEND. Each maps to one
// core.ObjectStorage implementation.
const (
	PluginCOS   = "cos.objectstorage.plugin"
	PluginLocal = "local.objectstorage.plugin"
	// future: "aws-s3.objectstorage.plugin", "gcs.objectstorage.plugin"
)

// NewObjectStorage builds the plugin named by id, or an error if id
// doesn't match a known plugin. fsdriver owns the known-plugin set.
func NewObjectStorage(id string, cfg *config.Config, auditLog *audit.Logger, log *slog.Logger) (core.ObjectStorage, error) {
	switch id {
	case PluginCOS:
		return &COSPlugin{
			Bucket:             cfg.COSBucket,
			Region:             cfg.COSRegion,
			StaticSecretID:     cfg.COSStaticSecretID,
			StaticSecretKey:    cfg.COSStaticSecretKey,
			StaticSessionToken: cfg.COSStaticSessionToken,
			STSRefreshRatio:    cfg.STSRefreshRatio,
			Audit:              auditLog,
			Logger:             log,
		}, nil
	case PluginLocal:
		return &LocalPlugin{Root: cfg.StorageLocalRoot}, nil
	default:
		return nil, fmt.Errorf("unknown STORAGE_BACKEND plugin %q", id)
	}
}

// COSPlugin is the Object Storage plugin for Tencent COS. It owns the
// credential chain (TKE Pod Identity -> static AK/SK -> hard fail) and
// mounts a per-user COS-backed afero.Fs.
type COSPlugin struct {
	Bucket, Region                                      string
	StaticSecretID, StaticSecretKey, StaticSessionToken string
	STSRefreshRatio                                     float64
	Audit                                               *audit.Logger
	Logger                                              *slog.Logger

	client *cos.Client
}

var _ core.ObjectStorage = (*COSPlugin)(nil)

// Init builds and validates the credential chain, wires the audit hook for
// credential refresh events, and connects the COS client. Moved here from
// cmd/ftp-server/main.go verbatim.
func (p *COSPlugin) Init(ctx context.Context) error {
	chain, err := cos.NewChainFromEnv(ctx, p.StaticSecretID, p.StaticSecretKey, p.StaticSessionToken, p.STSRefreshRatio)
	if err != nil {
		return err
	}
	chain.OnRefresh = func(src string, ok bool, refreshErr error) {
		attrs := []any{"bucket", p.Bucket, "region", p.Region, "source", src}
		if ok {
			p.Logger.Info("cred refresh ok", attrs...)
		} else {
			p.Logger.Warn("cred refresh failed", append(attrs, "err", refreshErr)...)
		}
		detail := map[string]any{"bucket": p.Bucket, "region": p.Region}
		if refreshErr != nil {
			detail["err"] = refreshErr.Error()
		}
		p.Audit.Log(audit.Event{
			Action:  "CRED_REFRESH",
			Source:  src,
			Success: ok,
			Detail:  detail,
		})
	}
	if _, src, err := chain.Get(ctx); err != nil {
		return fmt.Errorf("cos: credential validation failed at startup: %w", err)
	} else {
		p.Logger.Info("cos: credential chain validated", "source", src)
	}
	if claims, err := cos.WebIdentityClaims(); err != nil {
		p.Logger.Warn("cos: could not decode web-identity token claims", "err", err)
	} else {
		p.Logger.Info("cos: web-identity token claims", "sub", claims["sub"], "iss", claims["iss"], "role_arn", os.Getenv("TKE_ROLE_ARN"))
	}
	p.Logger.Info("cos: credential chain ready", "tke_pod_identity", cos.HasTKEPodIdentity(), "static_fallback", chain.HasStatic())
	p.client = cos.NewClientWithChain(p.Bucket, p.Region, chain)
	p.Logger.Info("cos: client connected", "bucket", p.Bucket, "region", p.Region)
	return nil
}

func (p *COSPlugin) Mount(rootFolder string) (afero.Fs, error) {
	return NewCOS(rootFolder, p.client), nil
}

// Client exposes the underlying *cos.Client for the two admin endpoints
// that are COS-specific by design (folder-suggestion listing, root-folder
// placeholder creation on user creation) and are out of scope for this
// refactor. Returns nil until Init has run.
func (p *COSPlugin) Client() *cos.Client { return p.client }

// LocalPlugin is the Object Storage plugin for local disk (dev/test use).
type LocalPlugin struct{ Root string }

var _ core.ObjectStorage = (*LocalPlugin)(nil)

func (p *LocalPlugin) Init(ctx context.Context) error {
	return os.MkdirAll(p.Root, 0o755)
}

func (p *LocalPlugin) Mount(rootFolder string) (afero.Fs, error) {
	return NewLocal(filepath.Join(p.Root, rootFolder)), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd apps/ftp && go test ./internal/fsdriver/... -run TestNewObjectStorage -v`
Expected: PASS (3 subtests).

- [ ] **Step 5: Run the full fsdriver package test suite**

Run: `cd apps/ftp && go test ./internal/fsdriver/...`
Expected: PASS (includes the pre-existing `TestLocalDriverListsFiles`).

- [ ] **Step 6: Commit**

```bash
git add apps/ftp/internal/fsdriver/plugin.go apps/ftp/internal/fsdriver/plugin_test.go
git commit -m "feat(ftp): add COSPlugin/LocalPlugin object-storage plugins + selection factory"
```

---

## Task 5: Wire `cmd/ftp-server/main.go` to the storage plugin

**Files:**
- Modify: `apps/ftp/cmd/ftp-server/main.go`

This task keeps `admin.New(...)`'s signature untouched (Task 6 changes
it) — `cosClient` is derived here via type-assertion and passed through
exactly as before, so this task compiles and passes tests standalone.

- [ ] **Step 1: Replace the inline COS chain-building block**

Replace (the block right after `auditLog := audit.New(pool, log)` /
`defer func() { _ = auditLog.Close(context.Background()) }()`, through the
end of the `client := cos.NewClientWithChain(...)` / `log.Info("cos: client connected", ...)` lines):

```go
	chain, err := cos.NewChainFromEnv(ctx, cfg.COSStaticSecretID, cfg.COSStaticSecretKey, cfg.COSStaticSessionToken, cfg.STSRefreshRatio)
	if err != nil {
		return err
	}
	chain.OnRefresh = func(src string, ok bool, refreshErr error) {
		attrs := []any{"bucket", cfg.COSBucket, "region", cfg.COSRegion, "source", src}
		if ok {
			log.Info("cred refresh ok", attrs...)
		} else {
			log.Warn("cred refresh failed", append(attrs, "err", refreshErr)...)
		}
		detail := map[string]any{"bucket": cfg.COSBucket, "region": cfg.COSRegion}
		if refreshErr != nil {
			detail["err"] = refreshErr.Error()
		}
		auditLog.Log(audit.Event{
			Action: "CRED_REFRESH",
			Source: src,
			Success: ok,
			Detail: detail,
		})
	}
	if _, src, err := chain.Get(ctx); err != nil {
		return fmt.Errorf("cos: credential validation failed at startup: %w", err)
	} else {
		log.Info("cos: credential chain validated", "source", src)
	}
	if claims, err := cos.WebIdentityClaims(); err != nil {
		log.Warn("cos: could not decode web-identity token claims", "err", err)
	} else {
		log.Info("cos: web-identity token claims", "sub", claims["sub"], "iss", claims["iss"], "role_arn", os.Getenv("TKE_ROLE_ARN"))
	}
	log.Info("cos: credential chain ready", "tke_pod_identity", cos.HasTKEPodIdentity(), "static_fallback", chain.HasStatic())
	client := cos.NewClientWithChain(cfg.COSBucket, cfg.COSRegion, chain)
	log.Info("cos: client connected", "bucket", cfg.COSBucket, "region", cfg.COSRegion)
```

with:

```go
	storage, err := fsdriver.NewObjectStorage(cfg.StorageBackend, cfg, auditLog, log)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	if err := storage.Init(ctx); err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	var cosClient *cos.Client
	if cp, ok := storage.(*fsdriver.COSPlugin); ok {
		cosClient = cp.Client()
	}
```

- [ ] **Step 2: Update the `NewDriver` closure to mount through `storage`**

Replace:

```go
		NewDriver: func(u *db.FTPUser, clientIP string) (ftpserverlib.ClientDriver, error) {
			log.Info("ftp: user connected", "username", u.Username, "client_ip", clientIP, "bucket", cfg.COSBucket, "region", cfg.COSRegion, "root_prefix", u.RootFolder)
			auditLog.Log(audit.Event{
				Username: u.Username,
				ClientIP: net.ParseIP(clientIP),
				Action:   "LOGIN",
				Success:  true,
				Detail:   map[string]any{"bucket": cfg.COSBucket, "region": cfg.COSRegion, "root_prefix": u.RootFolder},
			})
			return fsdriver.NewAuditFS(fsdriver.NewCOS(u.RootFolder, client), auditLog, u.Username), nil
		},
```

with:

```go
		NewDriver: func(u *db.FTPUser, clientIP string) (ftpserverlib.ClientDriver, error) {
			fs, err := storage.Mount(u.RootFolder)
			if err != nil {
				return nil, fmt.Errorf("mount: %w", err)
			}
			log.Info("ftp: user connected", "username", u.Username, "client_ip", clientIP, "bucket", cfg.COSBucket, "region", cfg.COSRegion, "root_prefix", u.RootFolder)
			auditLog.Log(audit.Event{
				Username: u.Username,
				ClientIP: net.ParseIP(clientIP),
				Action:   "LOGIN",
				Success:  true,
				Detail:   map[string]any{"bucket": cfg.COSBucket, "region": cfg.COSRegion, "root_prefix": u.RootFolder},
			})
			return fsdriver.NewAuditFS(fs, auditLog, u.Username), nil
		},
```

- [ ] **Step 3: Collapse the duplicate admin COS client**

Replace:

```go
	adminClient := cos.NewClientWithChain(cfg.COSBucket, cfg.COSRegion, chain)
	adminAPI := admin.New(pool, cfg.AdminCookieSecure, cfg.COSBucket, cfg.COSRegion, adminClient, cfg.FTPDefaultRootPrefix, auditLog)
```

with:

```go
	adminAPI := admin.New(pool, cfg.AdminCookieSecure, cfg.COSBucket, cfg.COSRegion, cosClient, cfg.FTPDefaultRootPrefix, auditLog)
```

(`admin.New`'s signature is unchanged in this task — Task 6 adds the
`storage` parameter.)

- [ ] **Step 4: Build and run the full test suite**

Run: `cd apps/ftp && go build ./... && go test ./...`
Expected: builds clean; all tests PASS. `main.go` has no direct unit
tests — this step is a regression check that nothing else in the module
broke.

- [ ] **Step 5: Commit**

```bash
git add apps/ftp/cmd/ftp-server/main.go
git commit -m "refactor(ftp): wire main.go through fsdriver.NewObjectStorage instead of inline COS setup"
```

---

## Task 6: `internal/admin` — add `Storage`, finalize wiring

**Files:**
- Modify: `apps/ftp/internal/admin/api.go` (`API` struct, `New()`)
- Modify: `apps/ftp/internal/admin/files.go` (`errNoCOS`, `userFsFor`, imports)
- Modify: `apps/ftp/cmd/ftp-server/main.go` (`admin.New(...)` call site)

- [ ] **Step 1: Add `Storage` to `API` and thread it through `New()`**

In `internal/admin/api.go`, add `"github.com/example/cos-ftp-server/internal/core"`
to the import block. Add a field to the `API` struct, after `COSClient`:

```go
	Storage            core.ObjectStorage
```

Change `New()`'s signature and body:

```go
// New wires the admin API. cosBucket/cosRegion are the storage location
// assigned to newly provisioned FTP users (per-user bucket selection is not
// yet implemented — see main.go's M2 wiring note). cosClient is used to create
// each new user's root folder placeholder; pass nil to skip (e.g. in tests).
// storage backs the file browse/download endpoints (userFsFor) and is
// backend-agnostic; pass nil to disable file browsing (e.g. in tests using
// memFsForTest). defaultRootPrefix is the bucket-relative prefix the
// folder-suggestion endpoint lists from (e.g. "/t/t/"). Call ConfigureOIDC
// afterwards to enable SSO login.
func New(pool *pgxpool.Pool, cookieSecure bool, cosBucket, cosRegion string, cosClient *cos.Client, storage core.ObjectStorage, defaultRootPrefix string, auditLog *audit.Logger) *API {
	if defaultRootPrefix == "" {
		defaultRootPrefix = "/t/t/"
	}
	return &API{
		Pool:              pool,
		Sessions:          NewSessionManager(8*time.Hour, cookieSecure),
		Audit:             auditLog,
		COSBucket:         cosBucket,
		COSRegion:         cosRegion,
		COSClient:         cosClient,
		Storage:           storage,
		DefaultRootPrefix: defaultRootPrefix,
	}
}
```

- [ ] **Step 2: Rewire `userFsFor` in `internal/admin/files.go`**

Replace:

```go
// errNoCOS reports that the server has no COS client configured.
var errNoCOS = errors.New("COS client not configured (server missing COS credentials)")

// userFsFor returns an afero.Fs rooted at the user's COS folder.
func (a *API) userFsFor(u *db.FTPUser) (afero.Fs, error) {
	if memFsForTest != nil {
		return memFsForTest, nil
	}
	if a.COSClient == nil {
		return nil, errNoCOS
	}
	return fsdriver.NewCOS(u.RootFolder, a.COSClient), nil
}
```

with:

```go
// errStorageNotConfigured reports that the server has no storage backend configured.
var errStorageNotConfigured = errors.New("storage backend not configured")

// userFsFor returns an afero.Fs rooted at the user's storage folder.
func (a *API) userFsFor(u *db.FTPUser) (afero.Fs, error) {
	if memFsForTest != nil {
		return memFsForTest, nil
	}
	if a.Storage == nil {
		return nil, errStorageNotConfigured
	}
	return a.Storage.Mount(u.RootFolder)
}
```

Remove the now-unused `"github.com/example/cos-ftp-server/internal/fsdriver"`
import from `internal/admin/files.go` (it was only used by the
`fsdriver.NewCOS` call just removed — `folders_api.go` and `users_api.go`
still use `a.COSClient` directly and are untouched by this plan).

- [ ] **Step 3: Update the `admin.New(...)` call site in `cmd/ftp-server/main.go`**

Replace:

```go
	adminAPI := admin.New(pool, cfg.AdminCookieSecure, cfg.COSBucket, cfg.COSRegion, cosClient, cfg.FTPDefaultRootPrefix, auditLog)
```

with:

```go
	adminAPI := admin.New(pool, cfg.AdminCookieSecure, cfg.COSBucket, cfg.COSRegion, cosClient, storage, cfg.FTPDefaultRootPrefix, auditLog)
```

- [ ] **Step 4: Build and run the full test suite**

Run: `cd apps/ftp && go build ./... && go test ./...`
Expected: builds clean; all tests PASS, including
`TestListFiles_NoCOSClient` and `TestDownloadFile_NoCOSClient` in
`internal/admin/files_test.go` (they construct `&API{}` with no `Storage`
set, so `userFsFor` still returns a 503 via `errStorageNotConfigured` —
same status code, only the error message text changed, which those tests
don't assert on).

- [ ] **Step 5: Commit**

```bash
git add apps/ftp/internal/admin/api.go apps/ftp/internal/admin/files.go apps/ftp/cmd/ftp-server/main.go
git commit -m "refactor(ftp): route admin file browsing through core.ObjectStorage"
```

---

## Task 7: Final verification

- [ ] **Step 1: Full build**

Run: `cd apps/ftp && go build ./...`
Expected: exits 0.

- [ ] **Step 2: Full test suite**

Run: `cd apps/ftp && go test ./... -count=1`
Expected: all packages PASS. (Tests requiring `TEST_DATABASE_URL`, e.g.
`internal/admin/routes_test.go` and `internal/db/*_test.go`, `t.Skip` when
unset — that's pre-existing behavior, not something this plan changes.)

- [ ] **Step 3: `go vet`**

Run: `cd apps/ftp && go vet ./...`
Expected: no output.

- [ ] **Step 4: Confirm the plugin default preserves current production behavior**

Run: `cd apps/ftp && grep -n 'STORAGE_BACKEND' internal/config/config.go`
Expected: default is `"cos.objectstorage.plugin"` — an unset
`STORAGE_BACKEND` env var in any existing deployment (see
`apps/ftp/manifests/configmap.yaml`, `apps/ftp/manifests/secret.yaml.example`)
continues to run the COS backend exactly as before this plan.

No commit for this task — it's verification only.
