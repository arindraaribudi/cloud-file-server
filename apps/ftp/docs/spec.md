# Product Specification: COS-Backed FTP Service

**Document Version:** 1.1
**Status:** Draft
**Last Updated:** 2026-09-14

---

## 1. Executive Summary

A Go service that exposes a standards-compliant FTP/FTPS interface backed entirely by Tencent Cloud Object Storage (COS). The service manages FTP users and per-user root folders through an HTTP admin API/UI, persists configuration in PostgreSQL, produces an append-only audit trail, and authenticates to COS via a three-tier credential chain (TKE Pod Identity → static AK/SK → hard fail). The service is exposed to FTP clients through a Kubernetes Gateway API `TCPRoute`. **Scope of this spec is limited to the service binary and the TCPRoute manifest. Helm chart, public admin UI exposure, and full deployment automation are explicitly out of scope.**

---

## 2. Goals & Non-Goals

### 2.1 Goals

- Provide a standards-compliant FTP/FTPS interface for external and internal clients (humans via FileZilla, scripts via curl/lftp/rclone).
- Store no file content on local disk; all payloads live in Tencent COS.
- Allow administrators to manage users, passwords, and per-user root folders without redeploying.
- Produce a tamper-evident, queryable audit trail of every authentication and file operation.
- Authenticate to COS without hardcoded AK/SK by default; support a graceful fallback chain.
- Support horizontal scaling with client-IP session affinity at the Gateway layer.

### 2.2 Non-Goals (v1)

- SFTP (SSH File Transfer Protocol) support.
- WebDAV support.
- Multi-tenant billing or quota enforcement.
- Cross-region COS replication logic (delegated to COS itself).
- **Helm chart and full deployment automation.**
- **Public exposure of the admin UI.** Admin UI is reachable only via the in-cluster `Service` (ClusterIP).
- Per-user IP allowlists.
- Helm chart, Grafana dashboards, alerting rules.

---

## 3. Personas

| Persona | Description | Primary Interactions |
|---|---|---|
| End User | External partner or internal application uploading/downloading files. | FTP client (FileZilla, curl, lftp, rclone, custom). |
| FTP Admin | Operations engineer managing users and folders. | Admin UI/API (in-cluster only). |
| Security Auditor | Compliance reviewer investigating access patterns. | Admin UI, audit log viewer, CSV export. |
| Platform Engineer | Deploys and operates the service on TKE. | Applies TCPRoute + ServiceAccount + manifests; chooses Gateway implementation. |

---

## 4. Functional Requirements

### 4.1 FTP Protocol

| ID | Requirement | Priority |
|---|---|---|
| FR-FTP-01 | Support FTP over explicit TLS (FTPS, `AUTH TLS`). TLS 1.2+ only. | Must |
| FR-FTP-02 | Support plain FTP (configurable, default disabled). | Should |
| FR-FTP-03 | Support passive mode (`PASV`, `EPSV`). Passive port range `50000–50999` (1000 ports). | Must |
| FR-FTP-04 | Active mode (`PORT`, `EPRT`) supported in code, **disabled by default**. Per-user flag `allow_active_mode=true` enables it. | Should |
| FR-FTP-05 | Support commands: `USER`, `PASS`, `PWD`, `CWD`, `CDUP`, `LIST`, `NLST`, `MLSD`, `MLST`, `STOR`, `STOU`, `APPE`, `RETR`, `DELE`, `MKD`, `RMD`, `RNFR`, `RNTO`, `SIZE`, `MDTM`, `REST`, `FEAT`, `OPTS`, `TYPE`, `PASV`, `EPSV`, `QUIT`, `STAT`, `NOOP`. | Must |
| FR-FTP-06 | Enforce per-user root folder; reject path traversal (`..`, absolute paths, symlinks outside root). | Must |
| FR-FTP-07 | UTF-8 filenames (`OPTS UTF8 ON` enforced; reject `LANG` other than `UTF-8`). | Must |
| FR-FTP-08 | Idle timeout configurable (default 300s). | Must |
| FR-FTP-09 | Maximum concurrent sessions per user configurable (default 5). | Should |
| FR-FTP-10 | Maximum total concurrent sessions configurable (default 500 per pod). | Should |
| FR-FTP-11 | `STOR` default = overwrite. Per-user flag `refuse_overwrite=true` makes server respond `550 File exists` if target key exists. `STOU` always picks a unique name within the user's root folder. | Must |
| FR-FTP-12 | `MLST`/`MLSD` facts: `type`, `size`, `modify`, `perm`, `unique`. `LIST` falls back to Unix-style when `MLSD` not negotiated. | Must |
| FR-FTP-13 | `REST` supported for `RETR` (range downloads) and `STOR`/`APPE` (resume interrupted upload). | Should |

### 4.2 User Management UI

| ID | Requirement | Priority |
|---|---|---|
| FR-UI-01 | Admin login with session cookie auth, bcrypt password (cost ≥ 12). | Must |
| FR-UI-02 | Create user with username, password, root folder, COS bucket, region, `allow_active_mode`, `refuse_overwrite`, `max_sessions`. | Must |
| FR-UI-03 | Edit user; password change optional. | Must |
| FR-UI-04 | Enable/disable user without deleting. | Must |
| FR-UI-05 | Soft-delete user. New logins rejected. Existing sessions terminated at next command. | Must |
| FR-UI-06 | List users with pagination and search by username. | Must |
| FR-UI-07 | View active sessions per user (session id, client IP, login time). | Should |
| FR-UI-08 | Force-terminate active session by id (sends `421` and closes). | Should |
| FR-UI-09 | Password policy enforcement (min length, complexity) configurable. | Should |
| FR-UI-10 | RBAC for admin UI (SuperAdmin, Operator, ReadOnly). | Could |

### 4.3 Audit Trail

| ID | Requirement | Priority |
|---|---|---|
| FR-AUD-01 | Log every authentication attempt (success and failure). | Must |
| FR-AUD-02 | Log every file operation: upload, download, delete, rename, mkdir, rmdir, list. | Must |
| FR-AUD-03 | Log `client_ip`, `username`, `event_time` (UTC), `action`, `path`, `bytes`, `success`. | Must |
| FR-AUD-04 | Log contextual detail (error message, TLS cipher, session id, credential source) as JSONB. | Must |
| FR-AUD-05 | Audit records are append-only; no update or delete API. | Must |
| FR-AUD-06 | Admin UI to filter by username, action, date range, success flag, client IP. | Must |
| FR-AUD-07 | CSV export of filtered audit records (streamed, gzip optional). | Must |
| FR-AUD-08 | Retention 365 days. Enforced by monthly partition drop (PostgreSQL declarative partitioning). | Must |
| FR-AUD-09 | Credential refresh events (`CRED_REFRESH`) recorded with source (`STS`/`AKSK`) and outcome. | Must |
| FR-AUD-10 | Optional external sink (Syslog, Kafka, COS) for long-term archival. | Could |

### 4.4 COS Backend

| ID | Requirement | Priority |
|---|---|---|
| FR-COS-01 | Authenticate to COS via credential chain (see §4.5). | Must |
| FR-COS-02 | Per-user COS bucket and root prefix (`root_folder`). | Must |
| FR-COS-03 | Stream uploads/downloads; never buffer full object in memory. Multipart threshold 20 MB, part size 8 MB. | Must |
| FR-COS-04 | Multipart upload for files > 20 MB. Resume via `REST` reuses existing `UploadId`. | Must |
| FR-COS-05 | `CWD`/`LIST` mapped to COS prefix listing with `delimiter=/`. No recursion. | Must |
| FR-COS-06 | **Virtual folders only.** A folder exists iff ≥1 child key has the prefix. `MKD`/`RMD` are no-ops. `CWD` into non-existent prefix → `550`. | Must |
| FR-COS-07 | `RNFR`/`RNTO`: verify source exists, `CopyObject` then `DeleteObject` source. Both calls audited. | Must |
| FR-COS-08 | Preserve object metadata: content-type, size, last-modified. Server-side `Last-Modified` comes from COS `LastModified` header. | Should |
| FR-COS-09 | Configurable COS region and endpoint override per user. | Should |
| FR-COS-10 | `MLST`/`MLSD` facts derived from COS `HeadObject` / `ListObjectsV2`. No synthetic folder markers. | Must |

### 4.5 Pod Identity & Credential Chain

| ID | Requirement | Priority |
|---|---|---|
| FR-PID-01 | **Credential chain:** on each STS-credentialed call, attempt (1) Pod Identity via OIDC token file; (2) static AK/SK from env vars (`COS_STATIC_SECRET_ID`, `COS_STATIC_SECRET_KEY`) if present; (3) hard fail with logged error. | Must |
| FR-PID-02 | STS credential refresh at 80% TTL (default STS TTL 3600s). Refresh failures logged + audit event `CRED_REFRESH` with outcome; in-flight requests continue on previous credentials until expiry. | Must |
| FR-PID-03 | On STS error with no env-fallback, request returns 5xx to FTP client and logs `CRED_REFRESH` failure. Service does not crash. | Must |
| FR-PID-04 | If neither OIDC nor env vars are available at startup, service exits with non-zero code and a structured error log. | Must |
| FR-PID-05 | `CRED_REFRESH` audit events include source (`STS` or `AKSK`), `outcome` (`success`/`failure`), `expiry` (UTC), and `error` (if any). | Must |
| FR-PID-06 | AK/SK env vars, when used, are never logged or written to disk. Audit shows only `source=AKSK`, never the secret. | Must |

---

## 5. Non-Functional Requirements

### 5.1 Performance

| Metric | Target |
|---|---|
| Login latency (p95) | < 300 ms |
| Directory listing latency (p95, 1,000 entries) | < 800 ms |
| Upload throughput (single stream) | ≥ 100 MB/s |
| Download throughput (single stream) | ≥ 100 MB/s |
| Concurrent sessions per pod | ≥ 250 (requires 1000 passive ports — see §4.1) |
| Audit write latency (async, p99) | < 50 ms |

### 5.2 Availability

| Metric | Target |
|---|---|
| Service availability (monthly) | 99.9% |
| RTO | < 5 min |
| RPO | 0 (stateless pods, DB HA) |

### 5.3 Security

- TLS 1.2+ for FTPS and admin UI.
- Passwords hashed with bcrypt (cost ≥ 12).
- All database connections TLS.
- Admin UI: CSRF tokens, secure/HttpOnly/SameSite=Lax cookies.
- COS credentials never written to disk or logs; audit shows source only.
- Rate limiting: 5 failed logins per username per 15 min → lockout.

### 5.4 Scalability

- Stateless pods; HPA on CPU + active sessions.
- Client-IP session affinity required at Gateway (TCPRoute implementation must support it; documented as deployment requirement).
- DB connection pool per pod: default 20.

### 5.5 Observability

- Prometheus metrics: `ftp_sessions_active`, `ftp_bytes_in_total`, `ftp_bytes_out_total`, `ftp_auth_failures_total`, `cos_request_duration_seconds`, `cos_request_errors_total{op=...}`, `cred_refresh_total{source,outcome}`.
- Structured JSON logs to stdout.
- Optional OpenTelemetry tracing for COS calls.

---

## 6. System Architecture

```
                    ┌───────────────────────────────┐
   FTP Clients ───▶ │  Gateway API Implementation   │
                    │  TCPRoute :21, 50000-50999    │
                    │  SessionAffinity: ClientIP    │
                    └───────────────┬───────────────┘
                                    │
                    ┌───────────────▼───────────────┐
                    │  FTP Service Pods (2..N)      │
                    │  ┌─────────────────────────┐  │
                    │  │ ftpserverlib (control)  │  │
                    │  │ Auth → PostgreSQL       │  │
                    │  │ ClientDriver → COS      │  │
                    │  └───────────┬─────────────┘  │
                    │  ┌───────────▼─────────────┐  │
   Admins ────────▶│  │ Admin HTTP (UI/API)     │  │
   (port-forward/  │  │ Service ClusterIP only  │  │
    VPN)           │  └───────────┬─────────────┘  │
                    │  ┌───────────▼─────────────┐  │
                    │  │ Audit Logger (async)    │  │
                    │  └─────────────────────────┘  │
                    └──────┬──────────────────┬─────┘
                           │                  │
                           ▼                  ▼
                    ┌────────────┐    ┌─────────────────┐
                    │ PostgreSQL │    │ Tencent COS     │
                    │ (partitioned│   │ via STS / AK/SK │
                    │  audit)    │    │                 │
                    └────────────┘    └─────────────────┘
```

---

## 7. Data Model

### 7.1 ftp_users

| Column | Type | Notes |
|---|---|---|
| id | BIGSERIAL PK | |
| username | VARCHAR(64) UNIQUE | Login name. |
| password_hash | VARCHAR(255) | bcrypt. |
| root_folder | VARCHAR(512) | COS prefix, e.g. `/alice`. |
| cos_bucket | VARCHAR(256) | Bucket name. |
| cos_region | VARCHAR(64) | Optional override. |
| enabled | BOOLEAN | Default true. |
| allow_active_mode | BOOLEAN | Default false. |
| refuse_overwrite | BOOLEAN | Default false. |
| max_sessions | INT | Default 5. |
| created_at | TIMESTAMPTZ | |
| updated_at | TIMESTAMPTZ | |
| deleted_at | TIMESTAMPTZ | Soft delete. |

### 7.2 admin_users

| Column | Type | Notes |
|---|---|---|
| id | BIGSERIAL PK | |
| username | VARCHAR(64) UNIQUE | |
| password_hash | VARCHAR(255) | bcrypt. |
| role | VARCHAR(32) | SuperAdmin/Operator/ReadOnly. |
| created_at | TIMESTAMPTZ | |

### 7.3 audit_events (partitioned by month)

| Column | Type | Notes |
|---|---|---|
| id | BIGSERIAL | Partition-local. |
| event_time | TIMESTAMPTZ | Indexed per partition. |
| username | VARCHAR(64) | Indexed. |
| client_ip | INET | |
| session_id | UUID | Correlate control + data events. |
| action | VARCHAR(32) | LOGIN, LOGOUT, UPLOAD, DOWNLOAD, DELETE, RENAME, MKDIR, RMDIR, LIST, CRED_REFRESH. |
| path | TEXT | |
| bytes | BIGINT | |
| success | BOOLEAN | Indexed. |
| source | VARCHAR(8) | For CRED_REFRESH only: `STS` or `AKSK`. |
| detail | JSONB | Error msg, TLS cipher, expiry, etc. |

**Partitioning:** `audit_events` is range-partitioned by `event_time` on month boundaries. Retention = `DROP PARTITION` for any month older than 365 days, run by a scheduled job in the same binary or a separate `CronJob`.

**Indexes (per partition):**

- `(username, event_time DESC)`
- `(action, event_time DESC)`
- `(success, event_time DESC)`
- `(client_ip, event_time DESC)`
- GIN on `detail` (optional).

---

## 8. API Specification

### 8.1 Admin REST API

| Method | Path | Description |
|---|---|---|
| POST | `/api/v1/auth/login` | Admin login, returns session cookie. |
| POST | `/api/v1/auth/logout` | Invalidate session. |
| GET | `/api/v1/users` | List users (paginated, searchable). |
| POST | `/api/v1/users` | Create user. |
| GET | `/api/v1/users/{id}` | Get user. |
| PUT | `/api/v1/users/{id}` | Update user. |
| DELETE | `/api/v1/users/{id}` | Soft-delete user. |
| GET | `/api/v1/users/{id}/sessions` | Active sessions for user. |
| DELETE | `/api/v1/sessions/{sid}` | Force-terminate session. |
| GET | `/api/v1/audit` | Query audit events (filters). |
| GET | `/api/v1/audit/export` | CSV export. |

### 8.2 Example: Create User

**Request**

```http
POST /api/v1/users
Content-Type: application/json
Cookie: session=...

{
  "username": "alice",
  "password": "S3cret!Pass",
  "root_folder": "/alice",
  "cos_bucket": "my-ftp-bucket-1250000000",
  "cos_region": "ap-guangzhou",
  "allow_active_mode": false,
  "refuse_overwrite": false,
  "max_sessions": 5
}
```

**Response**

```json
{
  "id": 42,
  "username": "alice",
  "root_folder": "/alice",
  "cos_bucket": "my-ftp-bucket-1250000000",
  "enabled": true,
  "allow_active_mode": false,
  "refuse_overwrite": false,
  "created_at": "2026-09-14T10:30:00Z"
}
```

---

## 9. Deployment

**Scope:** this spec produces the Go service binary + TCPRoute manifest. Helm chart, public ingress, and admin UI ingress are out of scope.

### 9.1 Kubernetes Resources (in scope)

| Resource | Purpose |
|---|---|
| Deployment/ftp-server | FTP + Admin UI pods (2+ replicas). |
| Service/ftp-server | ClusterIP. Exposes FTP control `:2121` (TCPRoute backend) and admin UI `:8080` (in-cluster access). |
| ServiceAccount/ftp-server-sa | Annotated for TKE Pod Identity. |
| Secret/ftp-db | Database credentials. |
| ConfigMap/ftp-config | Passive port range, timeouts, etc. |
| Job/ftp-migrate | DB schema migration. |
| CronJob/ftp-audit-retention | Drop expired monthly partitions. |
| **TCPRoute/ftp-public** | Public FTP via Gateway API. |

### 9.2 ServiceAccount (Pod Identity)

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: ftp-server-sa
  namespace: ftp
  annotations:
    tke.cloud.tencent.com/role-arn: "qcs::cam::uin/100012345678:roleName/COSFTPRole"
    tke.cloud.tencent.com/audience: "sts.cloud.tencent.com"
    tke.cloud.tencent.com/token-expiration: "3600"
```

### 9.3 CAM Role Trust Policy

```json
{
  "version": "2.0",
  "statement": [
    {
      "effect": "allow",
      "principal": {
        "federated": [
          "qcs::cam::uin/100012345678:oidc-provider/tke-oidc-<cluster-id>"
        ]
      },
      "action": ["sts:AssumeRoleWithWebIdentity"],
      "condition": {
        "StringEquals": {
          "oidc:sub": "system:serviceaccount:ftp:ftp-server-sa",
          "oidc:aud": "sts.cloud.tencent.com"
        }
      }
    }
  ]
}
```

### 9.4 CAM Role Permission Policy

```json
{
  "version": "2.0",
  "statement": [
    {
      "effect": "allow",
      "action": [
        "cos:GetObject",
        "cos:PutObject",
        "cos:DeleteObject",
        "cos:HeadObject",
        "cos:GetBucket",
        "cos:ListMultipartUploads",
        "cos:ListParts",
        "cos:AbortMultipartUpload",
        "cos:InitiateMultipartUpload",
        "cos:UploadPart",
        "cos:CompleteMultipartUpload"
      ],
      "resource": [
        "qcs::cos:ap-guangzhou:uid/100012345678:my-ftp-bucket-1250000000/*"
      ]
    }
  ]
}
```

### 9.5 TCPRoute (Gateway API)

```yaml
apiVersion: gateway.networking.k8s.io/v1alpha2
kind: TCPRoute
metadata:
  name: ftp-public
  namespace: ftp
spec:
  parentRefs:
    - name: public-gateway   # out-of-scope: provided by deployer
      sectionName: ftp
  rules:
    - backendRefs:
        - name: ftp-server
          port: 2121
---
# One TCPRoute per passive port; Gateway API TCPRoute does not support port ranges.
# Generate 50000–50999 TCPRoutes, or front the service with a port-multiplexing proxy.
# Alternative: use Gateway listeners with multiple `ports` entries if the implementation supports it.
```

> **Caveat:** TCPRoute (v1alpha2) matches single ports, not ranges. Three options, in order of preference: (a) use a Gateway implementation that supports a `TcpListener` with multiple routes per port range (e.g. Envoy Gateway), (b) front the service with a TCP multiplexer proxy (e.g. HAProxy `tproxy`) that distributes by source IP and forwards to the pod's FTP control port, (c) accept the operational cost of 1000 TCPRoute objects. Option (a) is recommended; verify with the chosen Gateway implementation before deployment.

### 9.6 Admin UI Exposure

The `Service/ftp-server` exposes port 8080 as `ClusterIP` only. Operators reach it via `kubectl port-forward`, a bastion, or an out-of-scope internal ingress. No public exposure.

---

## 10. Passive Mode & Affinity

### 10.1 Client-IP Affinity Requirement

FTP uses two TCP connections (control `:21`, data `:random` in passive mode). For passive transfers to work, both connections from the same client must reach the same FTP pod. The TCPRoute implementation MUST provide client-IP session affinity. This is a **deployment requirement**, not enforced by the service. Document and verify with the chosen Gateway implementation.

The service must set `PublicIP` in `ftpserverlib.Settings` to the public IP advertised by the Gateway and configure `PassiveTransferPortRange` to `50000-50999`.

### 10.2 Connection Tracking Caveats

Client-IP affinity fails when clients share an IP (CGNAT, corporate proxies). If a target client population lives behind CGNAT, deploy a control-connection-tracking TCP proxy (e.g. HAProxy `tproxy`) in front of the service. This is the deployer's responsibility.

### 10.3 Passive Port Range Sizing

Concurrent transfers per pod × 1 port each = minimum port range. With target ≥250 concurrent sessions per pod, the 1000-port range (`50000-50999`) is the engineering floor; expand the range or reduce the concurrent target if Gateway port-mapping cost becomes prohibitive.

---

## 11. Configuration Reference

| Env Var | Default | Description |
|---|---|---|
| `DATABASE_URL` | — | PostgreSQL DSN. |
| `FTP_LISTEN` | `:2121` | Control port bind address. |
| `FTP_PUBLIC_IP` | — | Public IP advertised in `PASV`/`EPSV` replies. |
| `FTP_PASSIVE_PORT_RANGE` | `50000-50999` | Passive data port range. |
| `FTP_TLS_CERT` | — | Path to TLS certificate. |
| `FTP_TLS_KEY` | — | Path to TLS key. |
| `FTP_IDLE_TIMEOUT` | `300s` | Idle session timeout. |
| `FTP_ALLOW_PLAIN` | `false` | Permit non-TLS control connections. |
| `FTP_DEFAULT_ALLOW_ACTIVE` | `false` | Default for new users' `allow_active_mode`. |
| `FTP_DEFAULT_REFUSE_OVERWRITE` | `false` | Default for new users' `refuse_overwrite`. |
| `COS_USE_POD_IDENTITY` | `true` | Attempt STS first. |
| `COS_STATIC_SECRET_ID` | — | Fallback AK. Used only if STS fails AND this is set. |
| `COS_STATIC_SECRET_KEY` | — | Fallback SK. Never logged. |
| `STS_REFRESH_RATIO` | `0.8` | Refresh STS creds at this fraction of TTL. |
| `AUDIT_RETENTION_DAYS` | `365` | Audit partition retention. |
| `ADMIN_LISTEN` | `:8080` | Admin UI bind address. |
| `LOG_LEVEL` | `info` | |
| `LOG_FORMAT` | `json` | `json` or `text`. |

---

## 12. Milestones

| Phase | Deliverable | Duration |
|---|---|---|
| M1 | Core FTP server with local FS backend, auth from DB, basic audit, partitioned table. | 3 weeks |
| M2 | COS backend + credential chain (STS → AK/SK → fail). | 2 weeks |
| M3 | Admin UI (users CRUD, audit viewer, CSV export). | 3 weeks |
| M4 | FTPS, full command set, STOR overwrite, virtual folders, REST/resume. | 2 weeks |
| M5 | TCPRoute manifest, affinity verification, load tests, partition-retention job. | 2 weeks |
| M6 | Documentation (runbook, deploy guide). | 1 week |

---

## 13. Future Work

- SFTP (SSH) protocol support.
- WebDAV endpoint.
- Per-user bandwidth and storage quotas.
- COS cross-region replication dashboard.
- SIEM integration (Splunk, Elastic) via external audit sink.
- Fine-grained RBAC for admin UI.
- Object versioning and restore from audit log.
- Helm chart.

---

## 14. Resolved Decisions

| Question | Resolution |
|---|---|
| Admin UI exposure | `Service` ClusterIP only; port-forward/VPN. Public exposure out of scope. |
| Passive port range | `50000-50999` (1000 ports) — sized to ≥250 concurrent sessions per pod. |
| CLB vs HAProxy affinity | N/A — TCPRoute via Gateway API. Affinity is a deployer concern; documented requirement. |
| External audit sink | Deferred to Future Work. |
| Retention period | 365 days via monthly partition drop. |
| Per-user IP allowlist | Out of scope v1. |
| Active mode default | Off; per-user flag. |
| STOR overwrite default | Overwrite; per-user flag to refuse. |
| Folder representation | Virtual folders only (no marker objects). |
| Credential fallback | STS → env-var AK/SK → hard fail. |

---

## 15. Appendix: Library & Dependency Choices

| Concern | Library | Rationale |
|---|---|---|
| FTP protocol | `github.com/fclairamb/ftpserverlib` | Mature, active, supports FTPS + passive + custom drivers. |
| COS SDK | `github.com/tencentyun/cos-go-sdk-v5` | Official SDK. |
| DB driver | `github.com/jackc/pgx/v5` | Best-in-class PostgreSQL driver. |
| Migrations | `github.com/golang-migrate/migrate` | Simple, widely used. |
| HTTP router | `github.com/go-chi/chi/v5` | Lightweight, idiomatic. |
| Templates | `html/template` (stdlib) | No external dependency for a small UI. |
| Password hashing | `golang.org/x/crypto/bcrypt` | Standard. |
| Metrics | `github.com/prometheus/client_golang` | Standard. |
| Partitioning | PostgreSQL declarative partitioning | No external dep; managed by migrations. |

---

*End of document.*