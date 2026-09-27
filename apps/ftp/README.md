# ftp-server

FTP front-end + admin API over pluggable object storage (Tencent COS or local disk).

## Layout

```
cmd/ftp-server/     entrypoint — wires config, storage backend, FTP + admin servers (main.go)
internal/
  admin/            HTTP admin API (chi router): users, folders, files, audit, OIDC login
  audit/            append-only audit log writer (batched inserts) + retention/cleanup job
  auth/             password hashing/validation, login lockout
  config/           env -> Config, one Load()
  core/             shared interfaces (Authenticator, ObjectStorage, Touchpoint) — no impl
  cos/              Tencent COS SDK client wrapper (creds refresh, auth-error detection)
  db/               Postgres pool, migrations (db/migrations), seed data (db/seeds), queries
  fsdriver/         core.ObjectStorage impl: picks COS or local backend (STORAGE_BACKEND)
  ftpserver/        FTP protocol server (ftpserverlib), TLS, session tracking
  telemetry/        Prometheus /metrics handler
docs/               spec.md (requirements), runbook.md (ops)
manifests/          k8s deploy (Deployment, Service, ConfigMap, CronJob, TCPRoute)
scripts/            smoke.sh
```

## Where to look for X

| Task | Package |
|---|---|
| Add/change an admin REST endpoint | `internal/admin` (`*_api.go` by resource, router in `api.go`) |
| Change FTP login / auth checks | `internal/ftpserver` (`AuthUser`) + `internal/auth` |
| Add a storage backend | `internal/fsdriver` (implement `core.ObjectStorage`) |
| Change what gets audited | `internal/audit` |
| New env var / config field | `internal/config` |
| DB schema change | `internal/db/migrations` |
| k8s rollout | `manifests/` |

## Package dependency direction

`cmd/ftp-server` → `ftpserver` + `admin` → `fsdriver` → `cos` / local (afero) → `core` (interfaces only, no deps on the above)
