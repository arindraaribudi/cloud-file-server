# COS-Backed FTP Service Runbook

## Deploy

1. Apply manifests: `kubectl apply -f manifests/namespace.yaml`
2. Create the database Secret in-cluster (template: `manifests/secret.yaml.example`).
3. Configure CAM role-arn in `manifests/serviceaccount.yaml`, then apply remaining manifests.
4. Run migration Job: `kubectl create job -n ftp --from=cronjob/ftp-migrate ftp-migrate-now`
5. Verify pods ready: `kubectl get pods -n ftp -l app=ftp-server`
6. Verify TCPRoute accepted: `kubectl get tcproutes -n ftp`

## Credential Chain

The service uses STS (Pod Identity) first. On STS failure it falls back to env-var AK/SK.
Audit events with `action=CRED_REFRESH` record source and outcome.

Diagnose:

```sql
SELECT event_time, source, success, detail FROM audit_events
WHERE action='CRED_REFRESH' ORDER BY event_time DESC LIMIT 50;
```

## Retention

`audit_events` is range-partitioned by month. The in-binary ticker drops partitions older than
`AUDIT_RETENTION_DAYS` (default 365). To manually drop a partition:

```sql
DROP TABLE IF EXISTS audit_events_2024_01;
```

## Force-terminate Session

Admin API: `DELETE /api/v1/sessions/{sid}`. Server closes the control connection with `421`.

## Backup

PostgreSQL is the source of truth for users, admins, and audit. Run standard `pg_dump` / `pg_basebackup`.
COS data durability is COS's responsibility.

## Smoke test

```bash
DATABASE_URL=postgres://... ./scripts/smoke.sh
```

(Requires Postgres + a seeded admin user. See `scripts/smoke.sh`.)

## Alerts (suggested)

- `ftp_auth_failures_total` rate > 10/min sustained 5 min
- `cred_refresh_total{outcome="failure"}` rate > 0
- `cos_request_errors_total` rate > 1% of requests
- `ftp_sessions_active` > 80% of `max_sessions` per pod for 10 min

## TCPRoute caveat

`TCPRoute` matches single ports, not ranges. Three options (see manifests/README.md):
1. Use a Gateway implementation that supports `TcpListener` with port ranges (recommended).
2. Front the service with a TCP multiplexer (e.g. HAProxy `tproxy`).
3. Accept the operational cost of 1000 `TCPRoute` objects.

## Incident log

### 2026-09-16 — "cos: no credential source available" on every folder listing

**Symptom.** Admin web UI shows `Could not load folder suggestions: list folders: cos: no credential source available`. Every call to `/api/v1/folders` returns 502. FTP data plane works if a user was already provisioned with cached creds, but listing folders always fails.

**Root cause.** Chain had no usable credential at request time:
- TKE pod identity env vars (`TKE_ROLE_ARN`, `TKE_WEB_IDENTITY_TOKEN_FILE`) not injected into the pod
- `COS_STATIC_SECRET_ID`/`KEY` not set in the secret
- Chain booted with `usePodIdentity=true` + empty static → `Chain.Get` exhausted both sources → 502 per request

**Trigger.** Image v0.0.4 (or any pre-v0.0.5) where `NewChainFromEnv` did not fail boot when both sources were missing.

**Fix shipped in v0.0.5.** `NewChainFromEnv` now auto-detects pod identity from `TKE_WEB_IDENTITY_TOKEN_FILE` and fails boot fast with a clear message when neither source is configured, instead of serving silent 502s. Cache logic fixed (was re-fetching on every call). `Chain.Invalidate` + `Client.do` retry once on auth errors.

**Resolve in-cluster.**

1. Pick one credential source:
   - **Static.** Uncomment and fill `COS_STATIC_SECRET_ID` / `COS_STATIC_SECRET_KEY` in the secret. No env-var knob required; absence of `TKE_WEB_IDENTITY_TOKEN_FILE` selects the static path automatically.
   - **TKE pod identity.** Annotate the SA with the binding role ARN, add a projected service-account token volume, and set `TKE_ROLE_ARN` / `TKE_WEB_IDENTITY_TOKEN_FILE` on the ftp container. The CAM role must trust the cluster's OIDC provider for `sts:AssumeRoleWithWebIdentity`.
2. `kubectl rollout restart deploy/ftp-server -n <ns>`
3. Verify startup log: `cos: credential chain ready tke_pod_identity=<bool> static_fallback=<bool>`.
4. `curl -b cookies.txt http://ftp-server:7000/api/v1/folders?prefix=/` should return JSON.

**Verify the chain is actually rotating, not just cached forever.**
```bash
kubectl logs -n <ns> deploy/ftp-server | grep -E 'cos:|cred'
```
You should see no `no credential source` lines and STS calls limited to once per ~50 min per pod.
