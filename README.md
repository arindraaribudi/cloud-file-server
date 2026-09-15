# cloud-file-server

Monorepo: COS-backed FTP/FTPS server (Go) + admin SPA (Bun + React + TanStack Router).

End users upload and download files from any standards-compliant FTP client against a Tencent COS bucket. Administrators manage FTP users and review the audit trail through the browser UI.

| App | Stack | Port |
|---|---|---|
| `apps/ftp` | Go 1.26, ftpserverlib, pgx, afero, COS SDK | FTP `:2121` + admin `:8080` |
| `apps/web` | Bun 1.4 + React 19 + TanStack Router/Query/Table + Vite | `:9001` (static + reverse proxy `/api/*` → `:8080`) |

The Bun server is a thin shim: it serves the SPA bundle and forwards `/api/*` to the Go admin API. All business logic lives in Go.

## Repository layout

```
apps/
  ftp/
    cmd/ftp-server/        Go entrypoint
    internal/
      admin/               HTTP admin API (chi) + OIDC + session manager
      audit/               async batched audit logger + retention
      auth/                bcrypt + per-user lockout
      config/              env-driven configuration
      cos/                 COS SDK wrapper + credential chain
      db/                  pgx pool, queries, migrations, seeds
      fsdriver/            afero drivers: local FS, COS (virtual folders), audit wrapper
      ftpserver/           ftpserverlib lifecycle + DB authenticator
      telemetry/           Prometheus metrics
    migrations/, seeds/    SQL migrations + idempotent seeds
    manifests/             Kubernetes manifests (Deployment, Service, TCPRoute, ...)
    docs/                  spec.md (legacy), runbook.md
    scripts/smoke.sh       end-to-end smoke (FTP + admin login)
  web/
    server.js              Bun static + reverse proxy
    src/
      components/          Stamp, PasswordForm, UserForm
      lib/                 api client (fetch wrapper), password policy
      routes/              TanStack Router routes
      styles.css
    e2e/                   Playwright specs
docker-compose.yml         postgres + ftp-server + web for local dev
SPEC.md                    product specification (single source of truth)
```

## Prerequisites

- Bun 1.4 — workspace root + `apps/web`
- Go 1.23+ (Dockerfile uses 1.26) — `apps/ftp`
- Docker / docker-compose — optional, for the local stack
- A Tencent COS bucket and (optionally) static AK/SK for local development

## Install

```sh
bun install                   # installs all workspaces
(cd apps/ftp && go mod download)
```

## Run

### Full local stack (Postgres + FTP server + web UI)

```sh
docker compose up --build
```

This brings up Postgres on `:5432`, the FTP server on `:2121` (admin `:8080`), and the web UI on `:9001`. Open <http://localhost:9001> and sign in.

To create an admin user on first boot, set in `.env`:

```
FTP_SEED=true
FTP_SEED_USER=admin
FTP_SEED_PASS=ChangeMe!1
OIDC_ISSUER_URL=https://your-idp/realms/master
OIDC_CLIENT_ID=cloud-file-server
OIDC_CLIENT_SECRET=...
OIDC_ADMIN_GROUP=cloud-file-server-admin
```

Without OIDC configured, the web UI has no admin login path (the seeded admin can only authenticate to the FTP server). See `SPEC.md` §4.6 / §14.

### Web dev against a host-side FTP server

```sh
(cd apps/ftp && make run)     # runs the Go binary against your local Postgres
bun run dev:web               # vite dev server on :6000, proxies /api → :8080
```

Open <http://localhost:6000>.

### Iterate on the Go service only

```sh
cd apps/ftp
make run                      # uses DATABASE_URL from .env
```

## Build

```sh
bun run build                 # builds the web SPA into apps/web/dist
(cd apps/ftp && make build)   # produces apps/ftp/bin/ftp-server
```

The container images are produced by each app's `Dockerfile`.

## Test

```sh
bun run typecheck             # tsc -b --noEmit for the SPA, go vet for the FTP server
bun run test:ftp              # go test -race ./... (FTP server)
bun run lint:ftp              # golangci-lint run ./...
```

End-to-end tests live in `apps/web/e2e/` (Playwright):

```sh
bun --cwd apps/web run test:e2e
```

## Smoke test

The smoke script exercises an end-to-end login + FTP listing flow against a running stack:

```sh
apps/ftp/scripts/smoke.sh
```

(Requires Postgres, a seeded admin, and `lftp` for the FTP probe.)

## Configuration

Every knob is an environment variable. See `SPEC.md` §10 for the full reference. Most-used:

| Env | Default | Purpose |
|---|---|---|
| `DATABASE_URL` | required | PostgreSQL DSN |
| `FTP_LISTEN` | `:2121` | FTP control bind |
| `FTP_PASSIVE_PORT_RANGE` | `50000-50999` | Passive data ports |
| `FTP_TLS_CERT`, `FTP_TLS_KEY` | empty | FTPS |
| `FTP_ALLOW_PLAIN` | `false` | Permit non-TLS control |
| `COS_BUCKET`, `COS_REGION` | `test-1409486316`, `ap-bangkok` | Default COS target |
| `COS_STATIC_SECRET_ID`, `COS_STATIC_SECRET_KEY` | empty | Fallback AK/SK (required today) |
| `ADMIN_LISTEN` | `:8080` | Admin HTTP bind |
| `PUBLIC_URL` | `http://localhost:9001` | Public origin (used for OIDC redirect URI + cookie secure default) |
| `OIDC_ISSUER_URL`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET` | empty | Enable SSO login |
| `OIDC_ADMIN_GROUP`, `OIDC_READONLY_GROUP` | `admin`, `readonly` | Group → role mapping |
| `FTP_SEED`, `FTP_SEED_USER`, `FTP_SEED_PASS` | `false`, `admin`, empty | Bootstrap admin |

`.env` is git-ignored. See `.env.example` and `apps/ftp/docs/runbook.md`.

## Schema

PostgreSQL schema lives in `apps/ftp/internal/db/migrations/0001_init.up.sql`. Tables: `ftp_users`, `admin_users`, `audit_events` (range-partitioned by month), `audit_sessions`. Seeds under `apps/ftp/internal/db/seeds/` are applied when `FTP_SEED=true`.

See `SPEC.md` §7 for column-by-column details.

## Documentation

- `SPEC.md` — product specification (single source of truth).
- `apps/ftp/docs/spec.md` — earlier draft of the FTP-side spec; superseded by `SPEC.md`.
- `apps/ftp/docs/runbook.md` — deploy + retention + alerting.
- `apps/web/docs/user-crud-oidc-pages.md` — page-by-page UI plan.