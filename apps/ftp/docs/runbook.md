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
