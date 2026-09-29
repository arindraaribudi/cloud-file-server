# Design: Generic Touchpoint / Core / Object-Storage Interfaces for apps/ftp

**Date:** 2026-09-25
**Status:** Approved for planning

## 1. Problem

`apps/ftp` hardcodes Tencent COS as the storage backend at two call sites
(`cmd/ftp-server/main.go`'s `NewDriver` closure and
`internal/admin/files.go:userFsFor`), and hardcodes FTP as the only protocol
touchpoint. The product spec (`docs/spec.md` / root `SPEC.md`) already lists
SFTP, WebDAV, and additional storage backends as Future Work. This design
reshapes the internals so those can plug in later without touching core
business logic (auth, audit, user management).

**Note:** `internal/fsdriver.Local` already exists but is dead code in
production today (test-only) — this design makes it a real, selectable
backend.

## 2. Scope

**In scope:** refactor the *existing* FTP touchpoint and COS/local storage
into the new contract shape. Define the interfaces so a future touchpoint
(SFTP/WebDAV) or storage plugin (S3/GCS) requires zero changes to
`internal/core`, `internal/admin`, or `internal/db`.

**Out of scope (explicitly, to prevent scope creep):**
- No SFTP/WebDAV touchpoint implementation.
- No S3/GCS/other storage plugin implementation.
- No change to `admin.API.COSBucket`/`COSRegion` (display-only metadata
  fields, e.g. the `cos_address` shown on the account page) or to the
  user-creation flow.
- No change to audit-wrap behavior: FTP file ops stay audited via
  `AuditFS`; admin file browsing stays unaudited, exactly as today.
- No new Go module. Stays inside `apps/ftp`
  (`github.com/example/cos-ftp-server`), single binary.
- No package renames. `fsdriver`, `ftpserver`, `admin` keep their names;
  only a new `internal/core` package is added.

## 3. Architecture

A new `internal/core` package defines three contracts. Everything else
(touchpoint packages, storage plugins, admin) depends on `core`; `core`
depends only on `internal/db` (for `*db.FTPUser`) and `afero`.

```go
package core

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

Key existing-code finding: `ftpserver.Server` is *already* storage-agnostic
(it only knows `afero.Fs` / `ftpserverlib.ClientDriver`, never COS). The
hardcoding lives entirely in the two call sites that build a COS driver
directly — this design targets those two call sites, not the FTP server
itself.

## 4. Object storage plugins

Plugins live in the existing `internal/fsdriver` package. A plugin owns
*both* its authentication and its per-user mount — nothing else in the
codebase knows how a given backend authenticates.

```go
// COSPlugin — Tencent COS. Owns the credential chain (TKE Pod Identity ->
// static AK/SK -> hard fail) and mounts per-user COS-backed afero.Fs.
// Init absorbs the chain-build/validate/OnRefresh-audit-hook logic
// currently inline in main.go.
type COSPlugin struct {
    Bucket, Region                                      string
    StaticSecretID, StaticSecretKey, StaticSessionToken string
    STSRefreshRatio                                     float64
    Audit  *audit.Logger
    Logger *slog.Logger
    client *cos.Client // set by Init
}
func (p *COSPlugin) Init(ctx context.Context) error            { /* moved from main.go */ }
func (p *COSPlugin) Mount(rootFolder string) (afero.Fs, error) { return NewCOS(rootFolder, p.client), nil }

// LocalPlugin — local disk. For dev/docker-compose use.
type LocalPlugin struct{ Root string }
func (p *LocalPlugin) Init(ctx context.Context) error            { return os.MkdirAll(p.Root, 0o755) }
func (p *LocalPlugin) Mount(rootFolder string) (afero.Fs, error) { return NewLocal(filepath.Join(p.Root, rootFolder)), nil }
```

### Plugin selection (env-driven, namespaced IDs)

Plugins are selected by a namespaced identifier, not a bare word, so the
env value is self-describing and collision-proof as more plugins are added:

```go
package fsdriver

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
        return &COSPlugin{Bucket: cfg.COSBucket, Region: cfg.COSRegion, StaticSecretID: cfg.COSStaticSecretID, StaticSecretKey: cfg.COSStaticSecretKey, StaticSessionToken: cfg.COSStaticSessionToken, STSRefreshRatio: cfg.STSRefreshRatio, Audit: auditLog, Logger: log}, nil
    case PluginLocal:
        return &LocalPlugin{Root: cfg.StorageLocalRoot}, nil
    default:
        return nil, fmt.Errorf("unknown STORAGE_BACKEND plugin %q", id)
    }
}
```

`.env`: `STORAGE_BACKEND=cos.objectstorage.plugin` (default) or
`STORAGE_BACKEND=local.objectstorage.plugin`. Adding S3 later = one new
`S3Plugin` type + one new `const` + one new `case` line. Nothing else in
the codebase references plugin IDs.

## 5. Config changes (`internal/config`)

- `StorageBackend string` — env `STORAGE_BACKEND`, default
  `fsdriver.PluginCOS` value (`"cos.objectstorage.plugin"`).
- `StorageLocalRoot string` — env `STORAGE_LOCAL_ROOT`, default `"./data"`.
  Only consumed by `LocalPlugin`.

`config.Load()` does not validate the plugin id itself (that would
duplicate fsdriver's known-plugin set); `NewObjectStorage` returns the
error, which `main.go` bubbles up before the server starts — same
fail-fast point as today's other config checks.

## 6. `cmd/ftp-server/main.go` changes

Replaces the current ~35 lines of inline COS chain-building/logging with:

```go
storage, err := fsdriver.NewObjectStorage(cfg.StorageBackend, cfg, auditLog, log)
if err != nil {
    return err
}
if err := storage.Init(ctx); err != nil {
    return fmt.Errorf("storage: %w", err)
}
```

The single `storage` value is then used for **both**:
- the FTP `NewDriver` closure: `fsdriver.NewAuditFS(storage.Mount(u.RootFolder), auditLog, u.Username)` — audit-wrap unchanged from today.
- `admin.New(..., storage, ...)`.

This collapses today's duplicate COS-client construction (main.go
currently builds one `*cos.Client` for FTP and a second, separate one for
admin) into a single shared plugin instance — a natural consequence of the
refactor, not a new behavior.

## 7. `internal/admin` changes

- `API.COSClient *cos.Client` **stays** — it's still needed by two
  COS-specific admin helpers that are explicitly out of scope for this
  refactor: `folders_api.go:listFolders` (`a.COSClient.List(...)`, powers
  the folder-suggestion `<datalist>`) and `users_api.go:createUser`
  (`a.COSClient.Put(...)`, creates the root-folder placeholder object).
  Both already degrade gracefully when `COSClient` is nil (503 / skip
  placeholder creation) — unchanged.
- A **new** `API.Storage core.ObjectStorage` field is added, used only by
  `userFsFor` (the file browse/download path this design targets).
- `COSPlugin` gets an accessor so `main.go` can still obtain the raw
  `*cos.Client` for the two helpers above when the COS plugin is active:
  ```go
  func (p *COSPlugin) Client() *cos.Client { return p.client } // nil until Init runs
  ```
  `main.go` type-asserts: `if cp, ok := storage.(*fsdriver.COSPlugin); ok { cosClient = cp.Client() }`.
  With the local plugin (no `*cos.Client` exists), `cosClient` stays nil —
  same degrade-gracefully behavior as today when COS credentials are
  absent.
- `userFsFor` becomes:
  ```go
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
  (`errNoCOS` renamed `errStorageNotConfigured`, same guard behavior.)
- `COSBucket`/`COSRegion` fields and their usages (account page display,
  user-creation metadata) are untouched.

## 8. `internal/ftpserver` changes

- Local `Authenticator` interface removed; imports `core.Authenticator`
  instead (identical method signature, no behavior change).
- `DBAuthenticator` moves to `internal/core` (it's plain DB-backed auth,
  not FTP-protocol-specific — any future touchpoint reuses it unchanged).
- Add compile-time assertion `var _ core.Touchpoint = (*Server)(nil)` —
  `Server.Start`/`Server.Stop` already match the interface; this just
  proves it.

## 9. Data flow

- **`cos.objectstorage.plugin` (default):** byte-for-byte identical to
  today — same COS calls, same audit wrapping on the FTP path, admin
  browse still unaudited.
- **`local.objectstorage.plugin`:** FTP login -> `core.DBAuthenticator` ->
  `LocalPlugin.Mount(rootFolder)` -> `fsdriver.NewLocal(root+"/"+rootFolder)`
  -> wrapped in `AuditFS` -> served to `ftpserverlib`. Admin browsing hits
  the same plugin instance, no audit wrap (matches current COS behavior).

## 10. Testing

- No existing test sets `API.COSClient` directly (`files_test.go` and
  `routes_test.go` build `&API{}` and rely on `memFsForTest`) — those files
  need no changes. Only `internal/admin/api.go` (the `New()` constructor)
  and `cmd/ftp-server/main.go` (the one caller of `New()`) touch the new
  `Storage` field.
- `fsdriver/driver_test.go` (`NewLocal` direct test) is unaffected.
- New: a small test for `fsdriver.NewObjectStorage` covering the
  known-id and unknown-id cases.
- No new integration test strategy needed — this is a structural refactor
  with equivalent runtime behavior on the `cos` path.
