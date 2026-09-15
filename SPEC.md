# Cloud File Server — Product Specification

**Document Version:** 1.0
**Status:** Active (as-built)
**Last Updated:** 2026-09-16
**Source of truth:** this document describes the code in `apps/ftp` and `apps/web` as shipped.

---

## 1. Executive Summary

Cloud File Server is a single-binary FTP/FTPS service backed by Tencent Cloud Object Storage (COS), with a built-in admin HTTP API and a separately deployed React SPA for user management.

End users upload and download files using any standards-compliant FTP client (FileZilla, lftp, curl, rclone). Administrators manage FTP users and review the audit trail through a browser UI.

The service persists configuration, credentials, and audit history in PostgreSQL. Authentication to COS uses a credential chain — Pod Identity (STS) preferred, static AK/SK as fallback — so the service can run in TKE without hardcoded secrets.

The architecture is a monorepo with two deployable units:

| Unit | Stack | Purpose |
|---|---|---|
| `apps/ftp` | Go 1.26, ftpserverlib, pgx, afero, COS SDK | FTP/FTPS control + data plane, admin HTTP API, Prometheus metrics |
| `apps/web` | Bun 1.4 + React 19 + TanStack Router/Query/Table + Vite | Admin SPA |

A Bun static-and-proxy server (`apps/web/server.js`) ships the SPA and reverse-proxies `/api/*` to the Go service.

---

## 2. Goals & Non-Goals

### 2.1 Goals

- Provide a standards-compliant FTP/FTPS interface to COS.
- Store no file content on local disk; all payloads live in COS.
- Manage FTP users, passwords, and root folders through a web UI without redeploys.
- Produce a tamper-evident, queryable audit trail of every authentication and file operation.
- Support OIDC SSO and bootstrap-local-admin login for the admin UI.
- Support horizontal scaling; be deployable as a single binary + sidecar Postgres.

### 2.2 Non-Goals

- SFTP (SSH).
- WebDAV.
- Multi-tenant billing or quota enforcement.
- Cross-region COS replication logic (delegated to COS).
- Helm chart and full deployment automation.
- Public exposure of the admin UI (deployed behind an internal proxy).
- Per-user IP allowlists.
- Fine-grained RBAC beyond `admin` and `readonly`.

---

## 3. Personas

| Persona | Description | Primary interactions |
|---|---|---|
| End User | External partner or internal application uploading/downloading files. | FTP client (FileZilla, lftp, curl, rclone). |
| FTP Admin | Operations engineer managing FTP users. | Admin web UI / API. |
| Security Auditor | Compliance reviewer investigating access patterns. | Admin UI audit viewer, CSV export. |
| Platform Engineer | Deploys and operates the service on TKE. | Applies K8s manifests, chooses Gateway, manages secrets. |

---

## 4. Functional Requirements

### 4.1 FTP Protocol

| ID | Requirement | Priority | Status |
|---|---|---|---|
| FR-FTP-01 | FTP over explicit TLS (FTPS, `AUTH TLS`). TLS 1.2+. | Must | Implemented via `ftpserverlib`; TLS optional via `FTP_TLS_CERT`/`FTP_TLS_KEY`. |
| FR-FTP-02 | Plain FTP support (configurable, default disabled). | Should | `FTP_ALLOW_PLAIN`. |
| FR-FTP-03 | Passive mode with port range `50000–50999` (1000 ports). | Must | `FTP_PASSIVE_PORT_RANGE`. |
| FR-FTP-04 | Active mode supported but disabled by default; per-user flag. | Should | Field exists (`allow_active_mode`); not enforced in the current `NewDriver` path. |
| FR-FTP-05 | Commands: `USER`, `PASS`, `PWD`, `CWD`, `CDUP`, `LIST`, `NLST`, `MLSD`, `MLST`, `STOR`, `STOU`, `APPE`, `RETR`, `DELE`, `MKD`, `RMD`, `RNFR`, `RNTO`, `SIZE`, `MDTM`, `REST`, `FEAT`, `OPTS`, `TYPE`, `PASV`, `EPSV`, `QUIT`, `STAT`, `NOOP`. | Must | Delegated to `ftpserverlib`. |
| FR-FTP-06 | Per-user root folder; reject path traversal. | Must | `COS` driver prefixes all keys with the user's root. |
| FR-FTP-07 | UTF-8 filenames enforced. | Must | Delegated to `ftpserverlib`. |
| FR-FTP-08 | Idle timeout configurable (default 300s). | Must | `FTP_IDLE_TIMEOUT` / `IdleTimeout`. |
| FR-FTP-09 | Per-user session cap (default 5). | Should | Field exists; not enforced in code. |
| FR-FTP-10 | Total concurrent session cap. | Should | Not enforced. |
| FR-FTP-11 | `STOR` default = overwrite; per-user `refuse_overwrite=true` makes server return `550`. | Must | Field exists; enforcement depends on driver. |
| FR-FTP-12 | `MLST`/`MLSD` facts `type`, `size`, `modify`, `perm`, `unique`. | Must | Delegated to `ftpserverlib`. |
| FR-FTP-13 | `REST` for `RETR` (range) and `STOR`/`APPE` (resume). | Should | `Client.Get` honors range; PUT currently single-shot. |

### 4.2 User Management (Admin)

| ID | Requirement | Priority | Status |
|---|---|---|---|
| FR-UI-01 | Admin login via session cookie (8h TTL) or OIDC SSO. Bootstrap admin seeded by env. | Must | Implemented. |
| FR-UI-02 | Create user with username, password, root folder. | Must | Implemented. |
| FR-UI-03 | Edit user (root folder, enabled toggle). | Must | Implemented. |
| FR-UI-04 | Reset user password. | Must | Implemented. |
| FR-UI-05 | Disable / delete user. | Must | Soft-delete (`deleted_at`) + `enabled=false`. |
| FR-UI-06 | List users with search. | Must | Implemented (in-memory filter on the SPA, server-side search via `?search=`). |
| FR-UI-07 | Folder-suggestion combobox fed by COS prefix listing. | Should | Implemented (`/api/v1/folders`). |
| FR-UI-08 | View active sessions; force-terminate. | Should | DB table exists; no API/UI endpoint shipped yet. |
| FR-UI-09 | Password policy (8+ chars, upper, lower, digit). | Must | `auth.ValidatePassword`. |
| FR-UI-10 | RBAC for admin UI (`admin`, `readonly`). | Could | Server-side enforcement on `/api/v1/users` mutations; UI not yet honoring `readonly`. |
| FR-UI-11 | Self-service password change (`/account/password`). | Could | Server endpoint not shipped; SPA route not registered. |

### 4.3 Audit Trail

| ID | Requirement | Priority | Status |
|---|---|---|---|
| FR-AUD-01 | Log every authentication attempt (success and failure). | Must | Implemented (`LOGIN` action). |
| FR-AUD-02 | Log every file op: upload, download, delete, rename, mkdir, rmdir, list. | Must | Implemented via `AuditFS` wrapping the driver. |
| FR-AUD-03 | `event_time` (UTC), `username`, `client_ip`, `action`, `path`, `bytes`, `success`. | Must | Implemented. |
| FR-AUD-04 | Contextual detail as JSONB (`reason`, `err`, etc.). | Must | Implemented. |
| FR-AUD-05 | Append-only. No update/delete API. | Must | Schema enforces it; admin API has no update/delete on audit. |
| FR-AUD-06 | Filter by `username`, `action`, `success`, date range. | Must | Implemented (`/api/v1/audit`). UI filters by user/path only. |
| FR-AUD-07 | CSV export. | Must | Implemented (`/api/v1/audit/export`); UI not wired. |
| FR-AUD-08 | Retention 365 days, monthly partition drop. | Must | Schema partitioned; `audit.RunRetention` exists; cron wiring exists in manifest but not invoked from main. |
| FR-AUD-09 | `CRED_REFRESH` events with source (`STS`/`AKSK`) and outcome. | Must | Schema supports it; chain not yet calling the audit logger. |
| FR-AUD-10 | External sink (Syslog/Kafka/COS). | Could | Not implemented. |

### 4.4 COS Backend

| ID | Requirement | Priority | Status |
|---|---|---|---|
| FR-COS-01 | COS access via credential chain. | Must | Chain implemented (`internal/cos/creds.go`); STS path is currently a stub (`newOIDCSTS` returns an error). Main wires static AK/SK directly. |
| FR-COS-02 | Per-user COS bucket + root prefix. | Should | Single global bucket/region in config today; per-user override stored in DB. |
| FR-COS-03 | Stream uploads/downloads; never buffer full object. | Must | `Get` uses range + `io.ReadCloser`. `Put` is single-shot; `Create` buffers in memory (`memWriteFile`). Multipart deferred. |
| FR-COS-04 | Multipart upload for files > 20 MB. | Should | Not implemented. |
| FR-COS-05 | `CWD`/`LIST` mapped to COS prefix listing. | Must | `Client.List` with `Delimiter=/`. |
| FR-COS-06 | Virtual folders only. `MKD`/`RMD` no-ops on the server side. | Must | `Mkdir` writes a 0-byte placeholder; placeholder creation is idempotent. |
| FR-COS-07 | `RNFR`/`RNTO`: `Copy` then `Delete`. | Must | `COS.Rename`. |
| FR-COS-08 | Preserve object metadata; use COS `Last-Modified`/`Content-Length`. | Should | `parseTime` handles multiple layouts; `Stat` falls back to `<key>/` for folder placeholders. |
| FR-COS-09 | Per-user COS region/bucket override. | Should | DB columns exist; `NewDriver` currently uses global config. |
| FR-COS-10 | `MLST`/`MLSD` from `HeadObject`/`ListObjectsV2`. | Must | Delegated to `ftpserverlib`. |

### 4.5 Pod Identity & Credential Chain

| ID | Requirement | Priority | Status |
|---|---|---|---|
| FR-PID-01 | On every COS-credentialed call: try (1) Pod Identity, (2) static AK/SK from env, (3) hard fail with logged error. | Must | `cos.Chain` exists; main currently bypasses the chain and uses static creds directly. |
| FR-PID-02 | Refresh at 80% TTL; refresh failures logged + `CRED_REFRESH` audit event. | Must | Refresh ratio configurable (`STS_REFRESH_RATIO`); audit event not yet emitted. |
| FR-PID-03 | STS error with no env fallback → request returns 5xx, service does not crash. | Must | `Chain.Get` returns error; SDK surfaces it. |
| FR-PID-04 | If no credential source available at startup, exit non-zero. | Must | `main.go` currently hard-fails when static creds are empty. STS path is a stub. |
| FR-PID-05 | `CRED_REFRESH` events include source, outcome, expiry, error. | Must | Schema supports it. |
| FR-PID-06 | Static AK/SK never logged. | Must | `envStatic` returns them only through the chain. |

### 4.6 Admin Authentication

| ID | Requirement | Priority | Status |
|---|---|---|---|
| FR-AUTH-01 | Session cookie (`HttpOnly`, `Secure` when over HTTPS, `SameSite=Lax`), 8h sliding TTL. | Must | `SessionManager` in `admin/middleware.go`. |
| FR-AUTH-02 | OIDC SSO login with state cookie + group-based role (`admin`/`readonly`). | Must | `oidc_api.go`; configured via `OIDC_*` env vars. |
| FR-AUTH-03 | Bootstrap admin via `FTP_SEED=true` + `FTP_SEED_USER`/`FTP_SEED_PASS`. | Should | `main.go`. |
| FR-AUTH-04 | Login lockout: 5 failures in 15 min per username. | Must | `auth.Lockout` enforces for FTP users; not enforced for admin (local password path absent). |

---

## 5. Non-Functional Requirements

### 5.1 Performance

| Metric | Target |
|---|---|
| Login latency (p95) | < 300 ms |
| Directory listing (p95, 1000 entries) | < 800 ms |
| Single-stream upload/download | ≥ 100 MB/s |
| Concurrent sessions per pod | ≥ 250 |
| Audit write latency (async batched) | p99 < 50 ms |

### 5.2 Availability

| Metric | Target |
|---|---|
| Service availability (monthly) | 99.9% |
| RTO | < 5 min |
| RPO | 0 (stateless pods, DB HA) |

### 5.3 Security

- TLS 1.2+ for FTPS and the admin UI.
- bcrypt (cost 12) for FTP and admin passwords.
- All database connections TLS (DSN `sslmode`).
- Session cookies `HttpOnly`, `SameSite=Lax`, `Secure` when `PUBLIC_URL` is `https://` or `ADMIN_COOKIE_SECURE=true`.
- COS static AK/SK never logged or written to disk.
- Login lockout (FTP users): 5 failures per 15 min → block. Lockout state is per-pod in memory, so it does not survive restart and is not shared across pods.

### 5.4 Scalability

- Stateless pods; HPA on CPU + active sessions.
- DB connection pool per pod: max 20, min 2 (`db.New`).
- Client-IP session affinity required at the Gateway layer for passive mode to work across pods (deployment responsibility).

### 5.5 Observability

- Prometheus metrics on `GET /metrics` (admin port):
  - `ftp_sessions_active` (gauge)
  - `ftp_bytes_in_total`, `ftp_bytes_out_total` (counters)
  - `ftp_auth_failures_total{reason}` (counter)
  - `cos_request_duration_seconds{op}` (histogram)
  - `cos_request_errors_total{op}` (counter)
  - `cred_refresh_total{source, outcome}` (counter)
- Structured JSON logs to stdout (`slog.NewJSONHandler`).

---

## 6. System Architecture

```
                    ┌───────────────────────────────┐
   FTP Clients ───▶ │  Gateway / TCPRoute :2121     │
                    │  Passive 50000-50999          │
                    │  ClientIP session affinity    │
                    └───────────────┬───────────────┘
                                    │
                    ┌───────────────▼───────────────┐
                    │  apps/ftp (Go, single binary) │
                    │  ┌─────────────────────────┐  │
   Admins ────────▶│  │  admin :7000 HTTP API   │  │
   (browser)       │  │  (chi router, sessions) │  │
                    │  └───────────┬─────────────┘  │
                    │  ┌───────────▼─────────────┐  │
                    │  │ ftpserverlib  :2121     │  │
                    │  │   Auth → DBAuthenticator│  │
                    │  │   Driver → AuditFS →    │  │
                    │  │           COS / Local   │  │
                    │  └───────────┬─────────────┘  │
                    │  ┌───────────▼─────────────┐  │
                    │  │  audit.Logger (async)   │  │
                    │  │  /metrics (Prom)        │  │
                    │  └─────────────────────────┘  │
                    └──────┬──────────────────┬─────┘
                           │                  │
                           ▼                  ▼
                    ┌────────────┐    ┌─────────────────┐
                    │ PostgreSQL │    │ Tencent COS     │
                    │ ftp_users, │    │ via Chain       │
                    │ admin_users│    │ (STS → AK/SK)   │
                    │ audit_events│   └─────────────────┘
                    │ (partitioned)
                    └────────────┘
                           ▲
                           │
                    ┌──────┴───────────────┐
   Admins ────────▶ │  apps/web (SPA + BFF)│
   (browser)       │  Bun server.js       │
                    │   static / + proxy  │
                    │   /api/* → :7000     │
                    └──────────────────────┘
```

The Bun server in `apps/web/server.js` is **not** a BFF. It only serves the SPA bundle from `dist/` and reverse-proxies `/api/*` to the Go admin API. All business logic lives in Go.

---

## 7. Data Model

### 7.1 `ftp_users`

| Column | Type | Notes |
|---|---|---|
| `id` | `BIGSERIAL` PK | |
| `username` | `VARCHAR(64)` UNIQUE NOT NULL | Login name. |
| `password_hash` | `VARCHAR(255)` NOT NULL | bcrypt cost 12. |
| `root_folder` | `VARCHAR(512)` NOT NULL | COS prefix, e.g. `/alice`. |
| `cos_bucket` | `VARCHAR(256)` NOT NULL | Defaulted to global config at create time. |
| `cos_region` | `VARCHAR(64)` NULL | Per-user override. |
| `enabled` | `BOOLEAN` NOT NULL DEFAULT TRUE | |
| `allow_active_mode` | `BOOLEAN` NOT NULL DEFAULT FALSE | Stored; not enforced. |
| `refuse_overwrite` | `BOOLEAN` NOT NULL DEFAULT FALSE | Stored; not enforced. |
| `max_sessions` | `INT` NOT NULL DEFAULT 5 | Stored; not enforced. |
| `created_at` | `TIMESTAMPTZ` NOT NULL DEFAULT now() | |
| `updated_at` | `TIMESTAMPTZ` NOT NULL DEFAULT now() | |
| `deleted_at` | `TIMESTAMPTZ` NULL | Soft delete. |

### 7.2 `admin_users`

| Column | Type | Notes |
|---|---|---|
| `id` | `BIGSERIAL` PK | |
| `username` | `VARCHAR(64)` UNIQUE NOT NULL | |
| `password_hash` | `VARCHAR(255)` NOT NULL | bcrypt cost 12. |
| `role` | `VARCHAR(32)` NOT NULL | `SuperAdmin`, `Operator`, `ReadOnly`. |
| `created_at` | `TIMESTAMPTZ` NOT NULL DEFAULT now() | |

### 7.3 `audit_events` (range-partitioned by `event_time` month)

| Column | Type | Notes |
|---|---|---|
| `id` | `BIGSERIAL` | Partition-local. |
| `event_time` | `TIMESTAMPTZ` NOT NULL | Partition key. Indexed per partition. |
| `username` | `VARCHAR(64)` NOT NULL | Indexed. |
| `client_ip` | `INET` | Indexed. |
| `session_id` | `UUID` | Correlates events from one FTP connection. |
| `action` | `VARCHAR(32)` NOT NULL | `LOGIN`, `LOGOUT`, `UPLOAD`, `DOWNLOAD`, `DELETE`, `RENAME`, `MKDIR`, `RMDIR`, `LIST`, `ADMIN_LOGIN`, `ADMIN_LOGOUT`, `ADMIN_CREATE_USER`, `ADMIN_UPDATE_USER`, `ADMIN_RESET_PASSWORD`, `ADMIN_DELETE_USER`, `CRED_REFRESH`. Indexed. |
| `path` | `TEXT` | |
| `bytes` | `BIGINT` | Populated on upload/download completion. |
| `success` | `BOOLEAN` NOT NULL | Indexed. |
| `source` | `VARCHAR(8)` | `STS` or `AKSK` for `CRED_REFRESH` events only. |
| `detail` | `JSONB` | Free-form context (reason, error, expiry, etc.). |

**Partitioning:** PostgreSQL declarative range partitioning on `event_time`. A `DEFAULT` partition catches out-of-range writes. Retention = monthly `DROP PARTITION` for any month older than `AUDIT_RETENTION_DAYS` (default 365). See §11 for the retention runbook.

**Indexes (per partition):** `(username, event_time DESC)`, `(action, event_time DESC)`, `(success, event_time DESC)`, `(client_ip, event_time DESC)`.

### 7.4 `audit_sessions`

| Column | Type | Notes |
|---|---|---|
| `id` | `UUID` PK | |
| `username` | `VARCHAR(64)` NOT NULL | Indexed (partial: `terminated = FALSE`). |
| `client_ip` | `INET` NOT NULL | |
| `started_at` | `TIMESTAMPTZ` NOT NULL DEFAULT now() | |
| `last_seen` | `TIMESTAMPTZ` NOT NULL DEFAULT now() | |
| `terminated` | `BOOLEAN` NOT NULL DEFAULT FALSE | |

Table is provisioned by migrations; the API to list/force-terminate sessions is not yet shipped.

---

## 8. API Specification

### 8.1 Admin REST API (mounted on `cfg.AdminListen`, default `:8080`)

All endpoints under `/api/v1`. Authenticated routes require a valid `session` cookie. The Bun proxy at `:9001` forwards `/api/*` to this port.

| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/metrics` | none | Prometheus exposition. |
| POST | `/api/v1/auth/logout` | session | Revoke the caller's session cookie. |
| GET | `/api/v1/auth/oidc/authorize` | none | Redirect to OIDC IdP with state cookie. 501 if SSO not configured. |
| GET | `/api/v1/auth/oidc/callback` | state cookie | Exchange code, verify ID token, issue session, redirect to `OIDC_FRONTEND_REDIRECT_URL` (default `/auth/callback`). |
| GET | `/api/v1/auth/session` | session | Returns `{username, role, email, first_name, last_name}`. |
| GET | `/api/v1/users` | session | List users. Query: `?search=&limit=&offset=` (default limit 50, max 200). |
| POST | `/api/v1/users` | `admin` | Create user. Body: `{username, root_folder, password, enabled}`. Validates password, creates COS placeholder, inserts row. |
| PATCH | `/api/v1/users/{username}` | `admin` | Update `root_folder` and `enabled`. |
| POST | `/api/v1/users/{username}/password` | `admin` | Reset password. Body: `{password}`. |
| DELETE | `/api/v1/users/{username}` | `admin` | Soft-delete (`deleted_at=now()`, `enabled=FALSE`). |
| GET | `/api/v1/folders` | session | Suggest child folders under `?prefix=`. Falls back to `FTP_DEFAULT_ROOT_PREFIX`. 503 if no COS client. |
| GET | `/api/v1/audit` | session | Filter audit events. Query: `?username=&action=&success=&from=&to=&limit=&offset=`. Default range = last 30 days. Default limit 100, max 1000. |
| GET | `/api/v1/audit/export` | session | CSV export. Same filters minus pagination. |

There is **no** local password login endpoint for admins in the current binary. Admin login is via OIDC; the bootstrap admin created by `FTP_SEED=true` cannot log in to the web UI.

### 8.2 Example: Create User

**Request**

```http
POST /api/v1/users
Content-Type: application/json
Cookie: session=...

{
  "username": "alice",
  "root_folder": "alice/team-a",
  "password": "S3cret!Pass",
  "enabled": true
}
```

**Response**

```json
{
  "id": 42,
  "username": "alice",
  "root_folder": "/alice/team-a",
  "cos_bucket": "test-1409486316",
  "cos_region": "",
  "enabled": true,
  "allow_active_mode": false,
  "refuse_overwrite": false,
  "max_sessions": 5,
  "created_at": "2026-09-16T10:30:00Z",
  "updated_at": "2026-09-16T10:30:00Z"
}
```

Errors: `400` (validation), `403` (not admin), `409` (username exists), `502` (COS error with friendly message).

---

## 9. Deployment

### 9.1 Local (Docker Compose)

`docker compose up --build` brings up `postgres`, `ftp-server`, and `web`. Defaults are tuned for local development (`ADMIN_COOKIE_SECURE=false`, plain FTP allowed, `FTP_SEED=false`).

To bootstrap the local admin user, set `FTP_SEED=true`, `FTP_SEED_USER=admin`, `FTP_SEED_PASS=ChangeMe!1` in `.env`. With `OIDC_ISSUER_URL` set, that admin is created in `admin_users` but can only log in to the FTP server (there is no password login endpoint for admins); for the web UI, configure OIDC and sign in with a member of `OIDC_ADMIN_GROUP`.

### 9.2 Container Images

- `apps/ftp/Dockerfile`: multi-stage `golang:1.26-alpine` → `gcr.io/distroless/static-debian12:nonroot`. Binary listens on `:2121` and exposes the configured admin port. The passive range is declared with `EXPOSE 50000-50999`.
- `apps/web/Dockerfile`: Bun build of the SPA + `server.js` listening on `:9001`.

### 9.3 Kubernetes (manifests in `apps/ftp/manifests/`)

| Resource | Purpose |
|---|---|
| `namespace.yaml` | `ftp` namespace. |
| `deployment.yaml` | FTP + admin pods (2+ replicas). |
| `service.yaml` | ClusterIP. Exposes FTP control `:2121` and admin `:8080`. |
| `serviceaccount.yaml` | Annotated for TKE Pod Identity. |
| `secret.yaml.example` | Template for DB credentials, COS static AK/SK, OIDC client secret. |
| `configmap.yaml` | Non-secret env vars. |
| `cronjob-audit-retention.yaml` | Drops expired monthly partitions. |
| `tcproute.yaml` | Public FTP via Gateway API. |

### 9.4 TCPRoute Caveat

`TCPRoute` matches single ports, not ranges. The current manifest exposes `:2121` only; passive ports require one of:

1. A Gateway implementation supporting `TcpListener` with multiple routes per port range (Envoy Gateway recommended).
2. A TCP multiplexer proxy (HAProxy `tproxy`) in front, distributing by source IP to the pod's control port.
3. One `TCPRoute` per passive port (1000 objects — operational cost).

### 9.5 Admin UI Exposure

The admin port is exposed only via the `Service` ClusterIP. Reach it through `kubectl port-forward`, a bastion, or an out-of-scope internal ingress. The web SPA at `:9001` reverse-proxies `/api/*` to the admin service.

---

## 10. Configuration Reference

| Env Var | Default | Description |
|---|---|---|
| `DATABASE_URL` | required | PostgreSQL DSN. |
| `FTP_LISTEN` | `:2121` | FTP control bind address. |
| `FTP_PUBLIC_IP` | empty | Public IP advertised in `PASV`/`EPSV`. |
| `FTP_PASSIVE_PORT_RANGE` | `50000-50999` | Passive data ports. |
| `FTP_TLS_CERT` | empty | Path to TLS certificate. |
| `FTP_TLS_KEY` | empty | Path to TLS key. |
| `FTP_IDLE_TIMEOUT` | `300s` | Idle session timeout. |
| `FTP_ALLOW_PLAIN` | `false` | Permit non-TLS control connections. |
| `FTP_DEFAULT_ALLOW_ACTIVE` | `false` | Default for new users' `allow_active_mode`. |
| `FTP_DEFAULT_REFUSE_OVERWRITE` | `false` | Default for new users' `refuse_overwrite`. |
| `FTP_DEFAULT_ROOT_PREFIX` | `/t/t/` | Prefix the folder-suggestion endpoint lists from. |
| `FTP_LOCAL_ROOT` | `/tmp/ftp-m1` | Root for the local-FS driver (M1 path; not wired in main today). |
| `COS_USE_POD_IDENTITY` | `true` | Attempt STS first. Currently a stub. |
| `COS_STATIC_SECRET_ID` | empty | Fallback AK. Required today. |
| `COS_STATIC_SECRET_KEY` | empty | Fallback SK. Required today. Never logged. |
| `COS_STATIC_SESSION_TOKEN` | empty | Optional session token for the static AK/SK. |
| `COS_BUCKET` | `test-1409486316` | Bucket name. |
| `COS_REGION` | `ap-bangkok` | COS region. |
| `STS_REFRESH_RATIO` | `0.8` | Refresh STS creds at this fraction of TTL. |
| `AUDIT_RETENTION_DAYS` | `365` | Drop partitions older than this. |
| `AUTH_LOCKOUT_LIMIT` | `5` | Failed logins per window per FTP username. |
| `AUTH_LOCKOUT_WINDOW` | `15m` | Window. |
| `ADMIN_LISTEN` | `:8080` | Admin HTTP bind address. |
| `ADMIN_COOKIE_SECURE` | derived from `PUBLIC_URL` | Override `Secure` flag on session cookie. |
| `PUBLIC_URL` | `http://localhost:9001` | Public origin (used for OIDC redirect URI and cookie secure default). |
| `FTP_SEED` | `false` | Apply seed SQL + bootstrap admin on startup. |
| `FTP_SEED_USER` | `admin` | Bootstrap admin username. |
| `FTP_SEED_PASS` | empty | Bootstrap admin password (required if `FTP_SEED=true`). |
| `OIDC_ISSUER_URL` | empty | Enable SSO if set. |
| `OIDC_CLIENT_ID` | empty | |
| `OIDC_CLIENT_SECRET` | empty | |
| `OIDC_ADMIN_GROUP` | `admin` | Group → `admin` role. |
| `OIDC_READONLY_GROUP` | `readonly` | Group → `readonly` role. |
| `OIDC_FRONTEND_REDIRECT_URL` | `/auth/callback` | Browser destination after OIDC login. |
| `LOG_LEVEL` | `info` | |
| `LOG_FORMAT` | `json` | `json` or `text` (informational). |

---

## 11. Operational Runbook

### 11.1 Migrations

Run automatically on startup (`db.Migrate`). Migration files live under `internal/db/migrations/`. To run as a one-shot Job in-cluster, follow `apps/ftp/docs/runbook.md`.

### 11.2 Seed

`FTP_SEED=true` plus `FTP_SEED_USER`/`FTP_SEED_PASS` inserts a `SuperAdmin` row on startup. Subsequent starts log "may already exist" and continue. SQL seeds under `internal/db/seeds/` are also applied in lexical order; they must be self-idempotent (use `WHERE NOT EXISTS`).

### 11.3 Audit Retention

`audit.RunRetention(pool, retentionDays)` issues `DROP TABLE IF EXISTS audit_events_YYYY_MM` for the cutoff month. Wire it into the `CronJob` in `manifests/cronjob-audit-retention.yaml`. Today the function exists but no process invokes it on a schedule.

### 11.4 Credential Chain Diagnostics

When `Chain.Get` is fully wired, this query surfaces refresh failures:

```sql
SELECT event_time, source, success, detail FROM audit_events
WHERE action='CRED_REFRESH' ORDER BY event_time DESC LIMIT 50;
```

Until the chain is wired, COS calls go through static creds and `CRED_REFRESH` events will not appear.

### 11.5 Backup

`pg_dump` / `pg_basebackup` covers users, admins, and audit. COS data durability is COS's responsibility.

### 11.6 Suggested Alerts

- `ftp_auth_failures_total` rate > 10/min sustained 5 min
- `cred_refresh_total{outcome="failure"}` rate > 0
- `cos_request_errors_total` rate > 1% of requests
- `ftp_sessions_active` > 80% of per-pod target for 10 min

---

## 12. Milestones (as built)

| Phase | Scope | Status |
|---|---|---|
| M1 | Core FTP server (local FS), DB auth, basic audit, partitioned table. | Done (driver exists; main wires COS instead). |
| M2 | COS backend + credential chain (STS → AK/SK → fail). | Partially done — COS backend wired; Chain implemented but STS path is a stub; main bypasses the chain. |
| M3 | Admin UI: user CRUD, audit viewer, CSV export. | Mostly done — see gaps below. |
| M4 | FTPS, full command set, STOR overwrite, virtual folders, REST/resume. | Done (delegated to `ftpserverlib`). |
| M5 | TCPRoute manifest, affinity verification, load tests, partition retention. | Partial — manifest exists; retention cronjob exists but not invoked. |
| M6 | Runbook, deploy guide. | Partial — `apps/ftp/docs/runbook.md`. |

### Known Gaps (vs. §4)

- **Local admin password login** is not exposed as an endpoint; only OIDC works for the web UI.
- **Active-mode**, **refuse-overwrite**, **max_sessions**, **per-user bucket/region** are stored on `ftp_users` but not enforced or honored by the driver.
- **Multipart upload** for files > 20 MB is not implemented; large PUTs buffer in memory.
- **STS path** of the credential chain is a stub (`newOIDCSTS` returns an error); main uses static creds directly.
- **`CRED_REFRESH` audit events** are not emitted because the chain is not invoked from main.
- **Active session list / force-terminate** UI/API not shipped; `audit_sessions` table is unused.
- **Self-service admin password change** (`/account/password`) is described in `apps/web/docs/user-crud-oidc-pages.md` but not in `router.tsx`.
- **CSV export** endpoint exists but the SPA does not call it.
- **Audit-retention cron** manifest exists but no process invokes `audit.RunRetention` on a schedule.

---

## 13. Future Work

- Wire `cos.Chain` into `cos.NewClient` (replace the direct static-cred path in `main.go`).
- Implement `newOIDCSTS` against the Tencent STS SDK; emit `CRED_REFRESH` audit events.
- Per-user `cos_bucket` / `cos_region` honored by `NewDriver`.
- Multipart upload (`PUT > 20MB`) with `REST`-based resume.
- Active session list + force-terminate endpoint.
- Local admin password login endpoint + self-service change route.
- CSV export button in the SPA.
- Wire `audit.RunRetention` into the existing `CronJob` (or move it into a ticker inside the binary).
- Helm chart; Prometheus alerting rules; Grafana dashboards.

---

## 14. Resolved Decisions

| Question | Resolution |
|---|---|
| Admin auth strategy | OIDC SSO only; bootstrap admin seeded via env but cannot log in to the web UI. |
| Folder representation | Virtual folders, plus a 0-byte placeholder at `<key>/` so `Stat` works. |
| Credential fallback | Chain: STS → env-var AK/SK → hard fail. STS path is a stub today. |
| Passive port range | `50000-50999` (1000 ports) — sized to ≥250 concurrent sessions per pod. |
| TCPRoute vs port range | Manifest exposes `:2121` only; passive ports need Gateway/multiplexer/1000 TCPRoutes (deployer's call). |
| Audit retention | 365 days via monthly partition drop. |
| STOR overwrite | Default overwrite; `refuse_overwrite` flag stored (enforcement deferred). |
| Active mode | Default off; `allow_active_mode` flag stored (enforcement deferred). |
| Per-user IP allowlist | Out of scope v1. |
| External audit sink | Deferred to Future Work. |

---

## 15. Library & Dependency Choices

| Concern | Library | Rationale |
|---|---|---|
| FTP protocol | `github.com/fclairamb/ftpserverlib` | Mature, active, supports FTPS + passive + custom drivers. |
| COS SDK | `github.com/tencentyun/cos-go-sdk-v5` | Official Tencent SDK. |
| DB driver | `github.com/jackc/pgx/v5` | Best-in-class PostgreSQL driver. |
| HTTP router | `github.com/go-chi/chi/v5` | Lightweight, idiomatic. |
| FS interface | `github.com/spf13/afero` | Required by `ftpserverlib.ClientDriver`. |
| Password hashing | `golang.org/x/crypto/bcrypt` | Standard; cost 12. |
| OIDC | `github.com/coreos/go-oidc/v3`, `golang.org/x/oauth2` | Stable, well-tested. |
| Metrics | `github.com/prometheus/client_golang` | Standard. |
| Web framework | Bun + React 19 + TanStack Router/Query/Table + Vite | Type-safe routing, file-based code splitting, React Query for server state. |
| SPA runtime | `apps/web/server.js` (Bun `Bun.serve`) | Static serving + reverse proxy in one Bun process; no business logic. |

---

*End of document.*