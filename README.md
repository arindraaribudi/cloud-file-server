# cloud-file-server

Monorepo: COS-backed FTP/FTPS + SFTP server (Go) + admin SPA (Bun + React + TanStack Router).

End users upload and download files against a Tencent COS bucket using FTP/FTPS or SFTP. Administrators manage users, reset passwords, toggle per-protocol access, browse uploaded files, and review the audit trail through the browser UI.

| App | Stack | Port |
|---|---|---|
| `apps/ftp` | Go 1.26, ftpserverlib, go-sftp, pgx, afero, COS SDK | FTP `:2121` + SFTP `:2222` + admin `:8080` |
| `apps/web` | Bun 1.4 + React 19 + TanStack Router/Query/Table + Vite | `:9001` (static + reverse proxy `/api/*` → `:8080`) |
| `apps/workflow` | Go 1.26 + chi + pgx + component registry/engine | `:8081` (internal, proxied through the admin API) |

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

### Per-user storage backend
- Each user is stored on either **Tencent COS** or **local disk**, chosen in the *Storage* field when the user is created. It is shown in the users table and **cannot be changed afterwards** (that would orphan the user's files).
- `STORAGE_ENABLED_BACKENDS` (`cos`, `local`, or `cos,local`) lists what admins can pick; `STORAGE_BACKEND` is the default for users that don't choose. Setting neither keeps the previous single-backend behaviour, and users created before this feature follow `STORAGE_BACKEND`.
- Local users live under `STORAGE_LOCAL_ROOT/<root_folder>`; in Kubernetes that path needs a persistent volume, and with several replicas a shared (ReadWriteMany) one.
- Workflow copies between two users on *different* backends are streamed through the server with a size cap rather than copied server-side; see [docs/cross-backend-copy.md](docs/cross-backend-copy.md).

### Admin file manager
- Browse any user's COS subtree at `/files/:userId?path=...` (`GET /api/v1/files/{userId}`).
- Breadcrumb nav across virtual folders; download via signed link (`GET /api/v1/files/{userId}/download?path=...`).
- Powered by the same COS driver the FTP/SFTP servers use — what admins see is what users see.

### Local administrators
- `/admins` lists and manages **local credential** admin accounts (separate from FTP users).
- Create an admin, choose `admin` or `readonly`, reset its password, or delete it.
- The server prevents deleting your own account, demoting yourself, and removing the last admin.
- SSO administrators are not stored in `admin_users`; they are managed by the IdP/group mapping.
- Local password login is controlled by `ADMIN_LOCAL_LOGIN` (Compose enables it and seeds `localadmin`).

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
- Admin account changes (`ADMIN_CREATE_ADMIN`, `ADMIN_UPDATE_ADMIN`, `ADMIN_RESET_ADMIN_PASSWORD`, `ADMIN_DELETE_ADMIN`) are recorded.
- Retention policy in `audit/retention.go` (env-tunable); `apps/ftp/docs/runbook.md` covers archival/cleanup.

### Workflow automation
- New `apps/workflow` Go service owns a component registry and a sequential execution engine.
- The file server publishes durable upload/remove events to `file_events`; the workflow service claims them with `FOR UPDATE SKIP LOCKED`.
- A workflow is a set of component instances assigned to **trigger**, **process**, or **output**:
  - triggers: `trigger.file.upload`, `trigger.file.remove` with path regex + MIME filters;
  - process: `process.file.rename`, `process.file.remove`, `process.condition`;
  - output: `output.file.copy`, `output.notify.webhook`, `output.notify.email`.
- Exactly one trigger component is allowed per workflow. Process and output components run in order.
- The designer shows the flow as a tree: **Trigger → one or more Condition boxes → actions**. Actions of the same kind sit together in one box ("Copy to folders", "Webhook / API calls", "Email"), so one workflow can copy to folders A/B/C and then call a notification webhook and an API. The grouping is derived from the flat component list; nothing extra is stored.
- Steps in one action box can run **at the same time** (the box's "Run all steps in this box at the same time" option, on by default for new boxes). Stored as `parallel_group` on adjacent output components with the same value; empty means one by one. A parallel box waits for all its steps: if one fails the others still finish, and on a retryable failure only the failed steps run again. At most 8 steps of a box run at once. Use separate destinations for parallel copies, since two copies to the same path can collide.
- `output.notify.webhook` takes an HTTP `method` (POST default; PUT, PATCH, DELETE, GET). GET and DELETE send no body.
- `process.condition` filters on path/MIME. A step belongs to a **route** (`route` on each component; empty is the default route), and a condition that is not met skips only the remaining steps of its own route, so the run still completes successfully and the other routes carry on. The designer shows each route as a **Condition box**: add as many as you need to handle different files differently (e.g. `^invoice_` → copy to folder A and B, `^scan_` → call a webhook). A file is checked against every Condition box, so it can follow more than one route; a box with no filter always runs. Parallel groups (below) never span routes. A route can be **collapsed** to a single node ("Condition: … · 3 steps") with the − / + button on its box, from its editor panel, or with "Collapse all routes"; its steps are kept and still run. Collapsing is a view setting only and is not saved, so a workflow always reopens expanded.
- The admin UI at `/workflows` uses the `sequential-workflow-designer` canvas with a drag-and-drop toolbox, a schema-driven step editor, and `/workflows/:id/runs` for trigger decisions and per-component results.
- All `/workflows*` routes are session-guarded in the router. Unauthenticated deep links redirect to `/login`; the workflow proxy API returns `401` without a session.
- Workflow file operations go through the file server's token-protected internal API, so path validation, audit, and storage semantics stay in one place. Runs can be retried, canceled, or dry-run without side effects.

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
      components/          Stamp, PasswordForm, UserForm, workflow builder
      lib/                 api client (fetch wrapper), password policy, workflow API
      routes/              TanStack Router routes
      styles.css
    e2e/                   Playwright specs
  workflow/
    cmd/workflow-server/   Go entrypoint (HTTP API + engine)
    internal/
      action/              file API client, templates, webhook/email
      api/                 chi routes, proxy auth, CRUD/run endpoints
      components/          component registry, schemas, validation, trigger matching
      db/                  pgx pool + workflow schema migrations
      engine/              dispatcher, executor, run locks, retries
      model/               workflow/component/run types
      store/               workflow + run persistence
docker-compose.yml         postgres + ftp-server + workflow + web for local dev
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

This brings up Postgres on `:15432`, the FTP server on `:2121` (admin `:7000`), the workflow service on `:8081`, and the web UI on `:9001`. Open <http://localhost:9001>.

For local development, Compose enables `ADMIN_LOCAL_LOGIN` and seeds a local admin:

```
username: localadmin
password: ChangeMe123
```

Change `FTP_SEED_PASS` (and `ADMIN_LOCAL_LOGIN=false`) before using a shared host. `ADMIN_LOCAL_LOGIN` defaults to `false` outside Compose.

SSO is also enabled when the `OIDC_*` variables are set. The IdP client must register `PUBLIC_URL + /api/v1/auth/oidc/callback` as a valid redirect URI. A localhost callback must be added to the client explicitly.

If a browser returns `431 Request Header Fields Too Large`, it is sending oversized headers (usually accumulated `localhost` cookies). The web shim accepts up to 64 KB; clear site data for `localhost` or open <http://127.0.0.1:9001> instead.

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
(cd apps/workflow && make build) # produces apps/workflow/bin/workflow-server
```

The container images are produced by each app's `Dockerfile`.

## Test

```sh
bun run typecheck             # SPA typecheck + go vet for both Go services
bun run test:ftp              # go test -race ./... (FTP server)
bun run test:workflow         # go test -race ./... (workflow service)
bun run lint:ftp              # golangci-lint run ./...
bun run lint:workflow         # golangci-lint run ./...
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
| `STORAGE_BACKEND` | `cos.objectstorage.plugin` | Default backend for new users (`local.objectstorage.plugin` for local disk) |
| `STORAGE_ENABLED_BACKENDS` | default backend only | Backends users may be assigned to: `cos`, `local`, or `cos,local` |
| `STORAGE_LOCAL_ROOT` | `./data` | Directory for users on the local backend |
| `COS_BUCKET`, `COS_REGION` | `test-1409486316`, `ap-bangkok` | Default COS target |
| `COS_STATIC_SECRET_ID`, `COS_STATIC_SECRET_KEY` | empty | Fallback AK/SK (required today) |
| `ADMIN_LISTEN` | `:8080` | Admin HTTP bind |
| `PUBLIC_URL` | `http://localhost:9001` | Public origin (used for OIDC redirect URI + cookie secure default) |
| `ADMIN_LOCAL_LOGIN` | `false` | Enable the local admin password login endpoint (Compose sets `true`) |
| `WEB_MAX_HEADER_SIZE` | `65536` | Web shim header limit; raise if a browser sends oversized cookies |
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