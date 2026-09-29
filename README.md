# cloud-file-server

Monorepo: COS-backed FTP/FTPS + SFTP server (Go) + admin SPA (Bun + React + TanStack Router).

End users upload and download files against a Tencent COS bucket using FTP/FTPS or SFTP. Administrators manage users, reset passwords, toggle per-protocol access, browse uploaded files, and review the audit trail through the browser UI.

| App | Stack | Port |
|---|---|---|
| `apps/ftp` | Go 1.26, ftpserverlib, go-sftp, pgx, afero, COS SDK | FTP `:2121` + SFTP `:2222` + admin `:8080` |
| `apps/web` | Bun 1.4 + React 19 + TanStack Router/Query/Table + Vite | `:9001` (static + reverse proxy `/api/*` → `:8080`) |

## Features

### FTP / FTPS
- Standards-compliant FTP/FTPS server (`ftpserverlib`) on `:2121` with optional TLS (`FTP_TLS_CERT`, `FTP_TLS_KEY`).
- Non-TLS control channel disabled by default; flip with `FTP_ALLOW_PLAIN=true`.
- Per-user bcrypt-hashed password, lockout on repeated failures (`auth` package).
- Files persisted to a COS bucket via the `cos` afero driver — virtual folders synthesized from object key prefixes.

### SFTP
- Companion SSH file-transfer server (`go-sftp`) on `:2222`, gated by `SFTP_ENABLED=true`.
- Two auth modes: password (same bcrypt creds as FTP) and SSH public key (stored per user).
- Host key from `SFTP_HOST_KEY` (base64-encoded PEM private key) or auto-generated on first boot.
- Public keys managed via `POST /api/v1/users/{username}/sftp-key` or the `/users/:username/sftp-key` UI page.

### SFTP host key

The SFTP server's identity is its SSH host key. Every time the server starts with no
host key configured, it generates an ephemeral ed25519 key — clients see a new
fingerprint on every restart and reject the connection (`REMOTE HOST KEY HAS
CHANGED`). Pin a stable key via the `SFTP_HOST_KEY` env var so the fingerprint
stays constant across pod restarts.

The value is a base64-encoded PEM private key (RSA, ECDSA, or ed25519). The server
base64-decodes it, parses the key block, and uses it as the SSH host identity.

#### 1. Generate a key (once, on an operator machine)

```sh
# ed25519 (smallest, fastest, fine for SSH host keys)
ssh-keygen -t ed25519 -f sftp_host_ed25519 -N ""
```

This produces `sftp_host_ed25519` (private key) and `sftp_host_ed25519.pub` (public
key, for client pinning).

#### 2. Base64-encode the private key

```sh
# Linux / macOS — single-line base64, no line wraps
base64 -w0 sftp_host_ed25519    # GNU coreutils
base64 -i sftp_host_ed25519     # BSD / macOS without -w

# Sanity check: decode back and diff against the source file
base64 -d <<<"<paste-the-base64-here>" > /tmp/decoded.pem
diff sftp_host_ed25519 /tmp/decoded.pem && echo OK
```

#### 3. Store in a Kubernetes Secret

```sh
kubectl -n ftp create secret generic sftp-hostkey \
  --from-literal=SFTP_HOST_KEY="$(base64 -w0 sftp_host_ed25519)"
```

Wire it into the Deployment:

```yaml
env:
  - name: SFTP_HOST_KEY
    valueFrom:
      secretKeyRef:
        name: sftp-hostkey
        key: SFTP_HOST_KEY
```

Or, for local dev, put the base64 string straight in `.env`:

```
SFTP_HOST_KEY=LS0tLS1CRUdJTi...==
```

#### 4. Pin the fingerprint on clients

After the first deploy, every client must trust the key. Pull the public half and
add it to `known_hosts` (preferred — no prompt ever):

```sh
ssh-keyscan -p 2222 sftp.example.com >> ~/.ssh/known_hosts
```

Or pin via `~/.ssh/config` with the host key blob you already have:

```
Host sftp.example.com
  HostName sftp.example.com
  Port 2222
  StrictHostKeyChecking yes
  IdentityFile ~/.ssh/my_client_key
```

The server logs the SHA-256 fingerprint on startup when a key is loaded; check it
matches what your operators expect.

#### Caveats

- **Key lives in a Kubernetes Secret, not in the image.** Rotating means
  regenerating the keypair, re-uploading the Secret, and updating every client's
  `known_hosts` (one prompt per client on first reconnect).
- **Empty `SFTP_HOST_KEY` = ephemeral key.** The server logs a warning at startup
  so this is impossible to miss in CI.
- **One key per environment.** Don't share a dev key with staging or production —
  fingerprint pinning makes rotation painful across the wrong boundary.

### Admin file manager
- Browse any user's COS subtree at `/files/:userId?path=...` (`GET /api/v1/files/{userId}`).
- Breadcrumb nav across virtual folders; download via signed link (`GET /api/v1/files/{userId}/download?path=...`).
- Powered by the same COS driver the FTP/SFTP servers use — what admins see is what users see.

### Password reset
- Admin triggers reset at `POST /api/v1/users/{username}/password` (UI: `/users/:username/password`).
- Generates a one-shot temporary password, prints it once, forces change-on-next-login.
- The user can also self-serve from the profile page (OIDC-linked session).

### Disable / enable
- Each user carries `enabled`, `ftp_enabled`, `sftp_enabled` flags.
- Flip via `PATCH /api/v1/users/{username}` (UI toggles on `/users/:username/edit`).
- Disabled user = login rejected on both FTP and SFTP. Protocol-level disable = that one channel rejects the creds; the other keeps working.

### Audit trail
- Async batched logger (`audit` package) writes to `audit_events` (range-partitioned by month) and `audit_sessions`.
- Every FTP/SFTP login, command, file op, admin action (create/update/delete/reset/key change), and OIDC login is captured with actor IP, target, outcome.
- Browse at `/audit` (`GET /api/v1/audit`) with filters: actor, action type, date range.
- Export to CSV via `GET /api/v1/audit/export`.
- Retention policy in `audit/retention.go` (env-tunable); `apps/ftp/docs/runbook.md` covers archival/cleanup.

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
| `SFTP_ENABLED` | `false` | Enable SFTP server |
| `SFTP_LISTEN` | `:2222` | SFTP control bind |
| `SFTP_HOST_KEY` | empty | Base64 ed25519 host key (auto-generated if empty) |
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