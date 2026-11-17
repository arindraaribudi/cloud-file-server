# File Workflow Engine — Component-Based Implementation Plan

**Date:** 2026-11-10
**Status:** Proposed — revised for configurable components
**Goal:** Let users compose workflows from backend-provided component types. A workflow is no longer a fixed “one trigger + fixed steps” shape. Users add **multiple components**, assign each to a **trigger**, **process**, or **output** stage, configure them, and reorder them.

**Stack:** Go 1.26 for a new `apps/workflow` microservice; the existing Go file server (`apps/ftp`) for event capture and file operations; React 19 + TanStack Router/Query for the admin UI; PostgreSQL for both services.

**Core principle:** the file server remains the only component that touches object storage. The workflow service owns the component registry, workflow definitions, matching, run state, retries, and notifications, and calls a small internal file-operations API on the file server.

---

## 1. Scope

### In scope (v1)

- A backend **component registry** exposed as a catalog API.
- Component types are assigned to one of three stages: `trigger`, `process`, `output`.
- **Multiple components per stage**; components can be enabled/disabled and reordered.
- Multiple trigger components with `any` or `all` matching.
- Multiple process components executed sequentially.
- Multiple output components executed sequentially (e.g. copy to several folders, notify several channels).
- Per-component configuration validated against a JSON Schema and semantic rules.
- Schema-driven UI forms so adding a new backend component type does not require a new frontend form.
- Workflow CRUD, enable/disable, duplicate, test-match, dry run, run history, retry.
- At-least-once event delivery with idempotent component execution.
- Workflow file operations written to the existing audit trail.
- Compose/Kubernetes/CI wiring for the new service.

### Out of scope (v1)

- User-authored component code (only backend-provided types).
- Branching, conditions, loops, parallel branches, node-graph canvas.
- Cron or manual “no file event” triggers.
- File content transformation.
- WebDAV triggers.
- In-app notification inbox (webhook + email only; run history is the in-app record).
- Reusable component instances shared across multiple workflows (instances belong to one workflow).
- Exactly-once notification delivery.

---

## 2. Architecture

~~~mermaid
flowchart LR
    FTP[FTP / SFTP clients] --> FTPD[apps/ftp file server]
    FTPD -->|upload / remove / rename| COS[(Object storage: COS or local)]
    FTPD -->|INSERT| EV[(public.file_events)]
    FTPD -->|internal file ops API| API[apps/ftp admin API :7000]
    WF[apps/workflow :8081] -->|poll / claim| EV
    WF -->|rename / remove / copy| API
    WF -->|webhook / SMTP| EXT[External systems]
    WF -->|workflow tables| DB[(PostgreSQL)]
    WEB[apps/web React SPA] -->|/api/*| FTPD
    FTPD -->|reverse proxy /api/v1/workflows| WF
    FTPD -->|sessions + audit| DB
~~~

**Why this shape**

- **Outbox, not a broker.** `file_events` gives durable handoff without Kafka/RabbitMQ. The workflow service polls and marks events dispatched.
- **Internal file API, not shared storage code.** The workflow service does not import `internal/fsdriver` or `internal/cos`, needs no COS credentials, and cannot bypass path validation/audit.
- **File-server proxy, not new auth.** Admin sessions are in-memory in the file server. The file server authenticates `/api/v1/workflows/*` and reverse-proxies with trusted identity headers, so the browser stays same-origin and `apps/web/server.js` needs no change.
- **Component registry, not hardcoded workflow shapes.** The backend advertises component types and their config schemas. The UI builds a workflow from those types; the server validates stage assignment and config.

---

## 3. Component model

### 3.1 Stages and assignment

Every workflow is a list of **component instances**. Each instance has:

- `stage`: `trigger`, `process`, or `output`.
- `label`: optional display name for this instance (e.g. “PDF invoices”); defaults to the type name.
- `type_key`: a backend component type, e.g. `process.file.rename`.
- `type_version`: the registry version used when the instance was saved.
- `position`: order within its stage.
- `config`: component-specific JSONB.
- `enabled`: allow disabling without deleting.

The JSON API calls the type field `type`; the table stores it as `type_key`. Components default to `enabled=true` when omitted.

The server enforces that the type’s `stages` list contains the instance stage. For example, a `process.file.rename` type cannot be assigned to `output`. The built-in keys are namespaced by their primary stage for readability; this is a convention, not a model constraint. A type can declare multiple stages (for example, a future `file.rename` allowed in both `process` and `output`), and the same type key can be used in each.

Execution order is fixed by stage:

1. **Trigger components** are evaluated at dispatch time. They do not execute as actions.
2. **Process components** execute in position order.
3. **Output components** execute in position order.

Multiple trigger components are combined using the workflow’s `trigger_mode`:

- `any` (default): a run starts if **at least one** enabled trigger matches.
- `all`: a run starts only if **every** enabled trigger matches. This is useful when separate trigger components are intended as AND-ed filters; validation rejects combinations that can never match (e.g. an upload trigger plus a remove trigger in `all` mode).

### 3.2 Component type registry

The registry lives in Go, not in the database. It provides the catalog, config schemas, and handlers.

~~~go
type Stage string

const (
    StageTrigger Stage = "trigger"
    StageProcess Stage = "process"
    StageOutput  Stage = "output"
)

type Type struct {
    Key          string          // "process.file.rename"
    Name         string          // "Rename file"
    Description  string
    Stages       []Stage         // allowed stages
    Version      int             // increment on schema/semantic change
    ConfigSchema json.RawMessage // JSON Schema subset for UI + validation
    UISchema     json.RawMessage // optional field order/widgets/help
}

type TypeKey struct {
    Key     string
    Version int
}

type Registry struct {
    Types    map[TypeKey]Type
    Triggers map[TypeKey]TriggerHandler
    Actions  map[TypeKey]ActionHandler
}

type TriggerHandler interface {
    Match(ctx context.Context, ev FileEvent, cfg json.RawMessage) (MatchResult, error)
}

type ActionHandler interface {
    Execute(ctx context.Context, rc *RunContext, cfg json.RawMessage, dryRun bool) (Result, error)
}
~~~

- The catalog is returned by `GET /api/v1/workflow-components`.
- Action handlers receive the component stage in the execution context, so a type that allows multiple stages can vary its behavior by stage.
- Config schema supports a pragmatic JSON Schema subset: object, string, number, boolean, enum, array of strings, and simple nested objects. The UI renders forms from it.
- Semantic validation stays in Go: regex compilation, path safety, URL scheme, template parsing, cross-component rules. JSON Schema handles shape/type/required/enum.
- Adding a component type means: implement handler, register metadata + schema, add tests. No database migration and no new frontend form are required.

### 3.3 Built-in catalog

| Type key | Stage | Purpose | Key config fields |
|---|---|---|---|
| `trigger.file.upload` | trigger | Match upload events | `path_target`, `path_regex`, `mime_patterns`, `mime_regex`, `scope_username` |
| `trigger.file.remove` | trigger | Match remove events | same + `include_directories` |
| `process.file.rename` | process | Rename/move current file | `mode`, `template`, `pattern`, `replacement`, `collision` |
| `process.file.remove` | process | Remove current file or sidecar | `path_template`, `missing_ok` |
| `output.file.copy` | output | Copy current file to a folder/user | `destination_username`, `destination_path`, `collision` |
| `output.notify.webhook` | output | Signed HTTP notification | `url`, `secret_env`, `body_template`, `headers` |
| `output.notify.email` | output | SMTP notification | `to`, `subject_template`, `body_template` |

Trigger components own their filters. For example, two upload triggers can be “PDF invoices” and “JPG scans”. They are OR-ed by default; users can add as many as needed. Trigger components have a stable type and config schema, so the catalog can summarize them in the workflow list.

### 3.4 Catalog API shape

~~~json
{
  "key": "process.file.rename",
  "name": "Rename file",
  "description": "Rename or move the current file using a template or regex.",
  "stages": ["process"],
  "version": 1,
  "config_schema": {
    "type": "object",
    "properties": {
      "mode": {"type": "string", "enum": ["template", "regex"], "default": "template", "title": "Mode"},
      "template": {"type": "string", "title": "Template", "default": "archive/{{.Date}}/{{.Base}}{{.Ext}}"},
      "pattern": {"type": "string", "title": "Regex pattern"},
      "replacement": {"type": "string", "title": "Regex replacement"},
      "collision": {"type": "string", "enum": ["fail", "suffix", "overwrite"], "default": "fail", "title": "On collision"}
    },
    "required": ["mode", "collision"]
  },
  "ui_schema": {
    "order": ["mode", "template", "pattern", "replacement", "collision"],
    "widgets": {
      "template": "template-path",
      "pattern": "regex",
      "replacement": "regex-replacement"
    }
  }
}
~~~

- `ui_schema` is optional. If absent, the UI uses field order from the schema and default widgets.
- Unknown/unsupported fields fall back to a JSON textarea so the UI does not block a valid backend type.

### 3.5 Type versioning

- Each component instance stores `type_version`.
- A run snapshots the component’s `type_key`, `type_version`, and `config`.
- If a workflow references a missing type/version, the workflow is marked **invalid**: it cannot be enabled or run, and the UI shows the offending component. Existing runs remain readable.
- When a component schema changes, bump `Version`. Keep an execution path for the previous version if existing workflows must keep running; otherwise the workflow must be migrated. v1 starts at version 1 and documents the migration policy.

### 3.6 Validation rules

- Workflow name 1–120 chars, unique among non-deleted workflows (case-insensitive).
- Component label 0–80 chars.
- At least one enabled trigger component.
- At least one enabled process or output component.
- `trigger_mode` is `any` or `all`.
- In `all` mode, enabled triggers must accept a common event type; reject upload+remove combinations.
- Every component’s `stage` is allowed by its type.
- Every component config validates against its JSON Schema.
- Semantic checks: RE2 regex compiles; MIME patterns valid; templates parse; URLs valid; paths safe; collision enums valid.
- `process.file.remove` is terminal for file-mutating components: no later `process.*` or `output.file.copy` after a remove. Later `output.notify.*` is allowed.
- Max 50 components total; max 20 per stage.
- Positions are assigned by the server from array order per stage; clients do not need to send them.
- Unknown JSON fields are rejected in component configs.

---

## 4. Data model

### 4.1 File server migration (public schema)

New migration `apps/ftp/internal/db/migrations/0008_file_events.{up,down}.sql`:

~~~sql
CREATE TABLE file_events (
  id               BIGSERIAL PRIMARY KEY,
  event_time       TIMESTAMPTZ NOT NULL DEFAULT now(),
  event_type       VARCHAR(16) NOT NULL CHECK (event_type IN ('upload','remove','rename')),
  user_id          BIGINT,
  username         VARCHAR(64) NOT NULL,
  root_folder      VARCHAR(512) NOT NULL,
  connection_type  VARCHAR(16) NOT NULL,
  backend_location VARCHAR(256) NOT NULL DEFAULT '',
  path             TEXT NOT NULL,
  old_path         TEXT,
  is_dir           BOOLEAN NOT NULL DEFAULT FALSE,
  size             BIGINT NOT NULL DEFAULT 0,
  mime             VARCHAR(255) NOT NULL DEFAULT '',
  mime_source      VARCHAR(16) NOT NULL DEFAULT '',
  overwrite        BOOLEAN NOT NULL DEFAULT FALSE,
  session_id       UUID,
  dispatched_at    TIMESTAMPTZ,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX file_events_pending_idx ON file_events (id) WHERE dispatched_at IS NULL;
CREATE INDEX file_events_time_idx ON file_events (event_time DESC);
~~~

Down: `DROP TABLE IF EXISTS file_events;`

- `path` is root-relative, no leading slash (e.g. `invoices/2026/11/invoice.pdf`).
- `user_id` is a snapshot; no FK so events survive user soft-deletes.
- Dispatched events older than `FILE_EVENT_RETENTION_DAYS` (default 30) are deleted in batches.

### 4.2 Workflow schema

The workflow service owns schema `workflow` and migration table `workflow.schema_migrations`. Use golang-migrate `Config{SchemaName: "workflow", MigrationsTable: "schema_migrations"}` after `CREATE SCHEMA IF NOT EXISTS workflow`.

If a separate schema is operationally undesirable, the same design works with public `workflow_*` tables and a dedicated `workflow_schema_migrations` table.

~~~sql
CREATE TABLE workflow.workflows (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name         TEXT NOT NULL,
  description  TEXT NOT NULL DEFAULT '',
  enabled      BOOLEAN NOT NULL DEFAULT FALSE,
  version      INT NOT NULL DEFAULT 1,
  trigger_mode TEXT NOT NULL DEFAULT 'any' CHECK (trigger_mode IN ('any','all')),
  created_by   TEXT NOT NULL DEFAULT '',
  updated_by   TEXT NOT NULL DEFAULT '',
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at   TIMESTAMPTZ
);

CREATE UNIQUE INDEX workflows_name_uq
  ON workflow.workflows (lower(name))
  WHERE deleted_at IS NULL;

CREATE INDEX workflows_enabled_idx
  ON workflow.workflows (enabled)
  WHERE deleted_at IS NULL AND enabled = TRUE;

CREATE TABLE workflow.components (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workflow_id  UUID NOT NULL REFERENCES workflow.workflows(id) ON DELETE CASCADE,
  stage        TEXT NOT NULL CHECK (stage IN ('trigger','process','output')),
  label        TEXT NOT NULL DEFAULT '',
  type_key     TEXT NOT NULL,
  type_version INT NOT NULL DEFAULT 1,
  position     INT NOT NULL CHECK (position >= 0),
  config       JSONB NOT NULL DEFAULT '{}'::jsonb,
  enabled      BOOLEAN NOT NULL DEFAULT TRUE,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workflow_id, stage, position)
);

CREATE INDEX components_workflow_idx
  ON workflow.components (workflow_id, stage, position);

CREATE TABLE workflow.runs (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workflow_id      UUID NOT NULL REFERENCES workflow.workflows(id),
  workflow_version INT NOT NULL,
  trigger_mode     TEXT NOT NULL CHECK (trigger_mode IN ('any','all')),
  event_id         BIGINT,
  source           TEXT NOT NULL CHECK (source IN ('event','manual','retry','test')),
  status           TEXT NOT NULL CHECK (status IN ('queued','running','succeeded','failed','canceled')),
  event            JSONB NOT NULL DEFAULT '{}'::jsonb,
  context          JSONB NOT NULL DEFAULT '{}'::jsonb,
  error            TEXT NOT NULL DEFAULT '',
  attempt          INT NOT NULL DEFAULT 0,
  max_attempts     INT NOT NULL DEFAULT 3,
  next_attempt_at  TIMESTAMPTZ,
  locked_by        TEXT,
  locked_until     TIMESTAMPTZ,
  cancel_requested BOOLEAN NOT NULL DEFAULT FALSE,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  started_at       TIMESTAMPTZ,
  finished_at      TIMESTAMPTZ
);

CREATE UNIQUE INDEX runs_event_uq
  ON workflow.runs (workflow_id, event_id);
-- Postgres treats NULL event_id values as distinct, so manual/test runs
-- can repeat while event-driven runs remain unique per event.

CREATE INDEX runs_queue_idx
  ON workflow.runs (status, next_attempt_at)
  WHERE status = 'queued';

CREATE INDEX runs_workflow_idx
  ON workflow.runs (workflow_id, created_at DESC);

CREATE TABLE workflow.run_components (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  run_id       UUID NOT NULL REFERENCES workflow.runs(id) ON DELETE CASCADE,
  component_id UUID,
  stage        TEXT NOT NULL CHECK (stage IN ('trigger','process','output')),
  label        TEXT NOT NULL DEFAULT '',
  type_key     TEXT NOT NULL,
  type_version INT NOT NULL,
  position     INT NOT NULL,
  config       JSONB NOT NULL DEFAULT '{}'::jsonb,
  status       TEXT NOT NULL CHECK (
                 status IN ('matched','not_matched','pending','running',
                            'succeeded','failed','skipped')),
  attempt      INT NOT NULL DEFAULT 0,
  started_at   TIMESTAMPTZ,
  finished_at  TIMESTAMPTZ,
  error        TEXT NOT NULL DEFAULT '',
  output       JSONB NOT NULL DEFAULT '{}'::jsonb,
  UNIQUE (run_id, component_id),
  UNIQUE (run_id, stage, position)
);

CREATE INDEX run_components_run_idx
  ON workflow.run_components (run_id, stage, position);

CREATE TABLE workflow.run_locks (
  workflow_id  UUID PRIMARY KEY REFERENCES workflow.workflows(id) ON DELETE CASCADE,
  run_id       UUID NOT NULL REFERENCES workflow.runs(id) ON DELETE CASCADE,
  locked_until TIMESTAMPTZ NOT NULL
);
~~~

- `run_components` snapshots every component at dispatch time, including trigger components (with `matched`/`not_matched` status), so run history survives workflow edits.
- `component_id` has no FK; deleting a workflow component must not break historical runs.
- `run_locks` guarantees one active run per workflow. A `NOT EXISTS running` check is racy when two workers claim different queued runs of the same workflow.

### 4.3 Trigger config location

There is no workflow-level `trigger` JSONB anymore. Trigger filters live in each `trigger.*` component’s `config`. A workflow can have many trigger components:

~~~json
[
  {
    "stage": "trigger",
    "type": "trigger.file.upload",
    "config": {
      "path_target": "basename",
      "path_regex": "(?i)^invoice_.*\\.pdf$",
      "mime_patterns": ["application/pdf"],
      "mime_regex": "",
      "scope_username": ""
    }
  },
  {
    "stage": "trigger",
    "type": "trigger.file.remove",
    "config": {
      "path_target": "full_path",
      "path_regex": "^archive/",
      "mime_patterns": [],
      "scope_username": ""
    }
  }
]
~~~

- `path` is root-relative, no leading slash.
- `events` is implicit in `trigger.file.upload` vs `trigger.file.remove`; no separate event field is needed.
- `path_target` is `basename` or `full_path`.
- `mime_patterns` supports exact/wildcard types; `mime_regex` is optional and ANDed with patterns.
- `scope_username` restricts the trigger to one user; empty means all users.
- `include_directories` (remove trigger only) defaults to `false`; folder removes are ignored unless enabled.

### 4.4 Why `dispatched_at`

The dispatcher needs a correct, efficient pending query:

~~~sql
SELECT ... FROM file_events
WHERE dispatched_at IS NULL
ORDER BY id
LIMIT $1
FOR UPDATE SKIP LOCKED;
~~~

A cursor can skip events that commit out of order; a `NOT EXISTS` anti-join degrades as the table grows. One nullable column plus a partial index is the simplest correct option. The workflow service has a narrow `UPDATE (dispatched_at)` grant; the file server never updates it.

---

## 5. File event capture (file server)

### 5.1 New package `apps/ftp/internal/fileevents`

- `event.go` — `Event` struct and constants.
- `publisher.go` — `Publisher` over `*pgxpool.Pool`; synchronous insert with a ~2s timeout. A failed insert is logged and never fails the file operation.
- `mime.go` — `DetectMIME(fs, name, size, sniffMode, maxBytes)`.

MIME strategy:

1. `mime.TypeByExtension`; strip parameters.
2. If `FILE_EVENTS_MIME_SNIFF_MODE=always`, or if it is `fallback` and the extension result is empty/`application/octet-stream`, read the first 512 bytes and call `http.DetectContentType` (when size ≤ cap).
3. Prefer an optional `RangeReader` capability implemented by COS and local drivers, so sniffing a COS object does not download it.
4. Fallback `application/octet-stream`, `mime_source = "none"`.

Modes: `fallback` (default), `always`, `off`. Sources: `extension`, `sniffed`, `none`.

### 5.2 Hook points in `apps/ftp/internal/fsdriver/audit.go`

`AuditFS` already wraps every FTP/SFTP file command, so extend it instead of adding protocol-specific hooks.

- Constructor gains `userID int64` and `events *fileevents.Publisher` (nil-safe). Update:
  - `apps/ftp/cmd/ftp-server/main.go`
  - `apps/ftp/internal/sftpserver/server.go`
  - `apps/ftp/internal/fsdriver/driver_test.go`
- `auditFile` gains a `failed` flag set on `Write` error; `onClose` receives `(bytes int64, failed bool)`. Only successful uploads publish events.
- `Create` / `OpenFile` with write flags: publish `upload` on close with MIME, size, `overwrite` (stat before open), session ID.
- `Remove`: stat and detect MIME before deletion (extension first; sniff only for non-directories under the cap); publish `remove` with `is_dir`, size, MIME.
- `RemoveAll`: one `remove` event with `is_dir=true`; do not expand trees in v1.
- `Rename`: publish `rename` with `old_path`, `path`, old-path MIME, `overwrite`.
- Reads, listings, mkdir: no event.

**Loop prevention:** workflow-originated operations must not publish file events. The internal file API writes storage directly, audits with `connection_type='workflow'`, and never goes through `AuditFS`.

### 5.3 File-server config

| Env | Default | Purpose |
|---|---|---|
| `FILE_EVENTS_ENABLED` | `true` | Publish events; safe with no workflow service. |
| `FILE_EVENTS_MIME_SNIFF_MODE` | `fallback` | `fallback`, `always`, or `off`. |
| `FILE_EVENTS_MIME_SNIFF_MAX_BYTES` | `8388608` | Skip sniffing above this size. |
| `FILE_EVENT_RETENTION_DAYS` | `30` | Delete dispatched events after this age. |
| `WORKFLOW_API_URL` | empty | Workflow service base URL; empty disables workflow routes (503). |
| `WORKFLOW_SERVICE_TOKEN` | empty | Shared service token; required when the proxy is enabled. |

---

## 6. Internal file-operations API (file server)

New `apps/ftp/internal/admin/internal_files_api.go`, mounted on the admin listener:

~~~text
POST /internal/v1/files/rename   {username, from, to, overwrite}
POST /internal/v1/files/remove   {username, path, missing_ok}
POST /internal/v1/files/copy     {source_username, source_path, target_username, target_path, overwrite}
GET  /internal/v1/files/stat?username=...&path=...
~~~

- `RequireServiceToken` middleware: constant-time compare of `X-Workflow-Token` to `WORKFLOW_SERVICE_TOKEN`; 401 otherwise; never log the token.
- Reuse `sanitizePath` from `files.go`; reject `..`, empty/absolute paths. Resolve users via `db.GetFTPUserByUsername`; mount `Storage.Mount(u.RootFolder)`.
- Copy: target user defaults to source; prefer an optional `core.Copier` implemented by COS (server-side copy) and local (stream copy); fallback to `afero` stream copy with a size guard (e.g. 256 MiB).
- Rename/copy collision: `overwrite=false` returns 409 if the destination exists.
- Remove: 404 if missing and `missing_ok=false`; directory removal requires `recursive=true` (not exposed in v1).
- Audit every operation: `connection_type='workflow'`, `event_type='workflow'`, action `WORKFLOW_RENAME` / `WORKFLOW_DELETE` / `WORKFLOW_COPY`, detail with workflow/run/component IDs. Correlate via `X-Workflow-Id`, `X-Workflow-Run-Id`, `X-Workflow-Component-Id` headers.
- Add `Copy` as a small optional interface in `core`.

### 6.1 Workflow API proxy

New `apps/ftp/internal/admin/workflow_proxy.go`:

- If `WORKFLOW_API_URL` is empty, return a clear 503 for `/api/v1/workflows` (exact and subtree) and `/api/v1/workflow-components`.
- Authenticate with the existing `SessionManager`.
- Allow `GET/HEAD` for any authenticated role; require `admin` for `POST/PUT/PATCH/DELETE`.
- Mount the proxy at `/api/v1/workflows` (exact and subtree) and `/api/v1/workflow-components`, forwarding to the workflow service with:
  - `X-Authenticated-User`, `X-Authenticated-Role`, `X-Authenticated-Email`
  - `X-Workflow-Proxy-Token` = `WORKFLOW_SERVICE_TOKEN`
- Strip client-supplied versions of those headers first.
- Preserve method, path, query, body, status, response headers.
- Proxy failures return 502 JSON with a safe message.

---

## 7. Workflow microservice

### 7.1 Layout

~~~text
apps/workflow/
  go.mod
  Makefile
  Dockerfile
  cmd/workflow-server/main.go
  internal/config/config.go
  internal/db/            # pgxpool, migrate (workflow schema), queries
  internal/db/migrations/ # 0001_init.up/down.sql
  internal/components/    # registry, built-in types, schemas, handlers
  internal/domain/        # Workflow, Component, Run, validation
  internal/store/         # CRUD + run queue
  internal/engine/        # dispatcher, executor, retry, lease reaper
  internal/action/        # file API client; webhook/email notifier
  internal/httpapi/       # chi routes, middleware, JSON helpers
  internal/metrics/       # Prometheus collectors
~~~

Dependencies: `pgx/v5`, `chi/v5`, `google/uuid`, `prometheus/client_golang`, and a JSON Schema validator such as `github.com/santhosh-tekuri/jsonschema/v6`. No COS SDK.

**Audit:** the workflow service writes its own admin rows directly to `public.audit_events` (`event_type='workflow'`, `connection_type='workflow'`, actions `ADMIN_WORKFLOW_CREATE/UPDATE/DELETE/DUPLICATE/RUN`, detail with workflow id/version). File operations are audited by the file server’s internal API. This requires `INSERT` on `public.audit_events` for the workflow DB role.

### 7.2 Component registry and catalog

- The registry is initialized at startup with all built-in types, keyed by `(type_key, version)`.
- `GET /api/v1/workflow-components` returns the **current** catalog, optionally filtered by `stage`. Passing `key` and `version` resolves a specific historical version, which the editor uses for workflows saved before a type changed.
- The registry compiles JSON Schemas once at startup and exposes `Validate(typeKey, version, config)`. It retains schemas and handlers for every supported version so saved workflows can still validate and execute after a type evolves.
- Handler lookup is by `(type_key, version, stage)`. If a workflow contains a type or version that no longer exists, the workflow is invalid; the API returns a clear component-level error and the executor refuses to run it.

### 7.3 Dispatcher: multiple triggers

Poll every `WORKFLOW_POLL_INTERVAL` (default 500ms). Cache compiled trigger matchers by workflow version, invalidated on update.

Workflows that fail registry/type-version resolution are skipped and logged; they do not create runs. The UI marks them invalid and refuses to enable them until fixed.

In one transaction:

1. Claim pending events with `FOR UPDATE SKIP LOCKED`.
2. For each event, load enabled workflows and evaluate **every enabled trigger component**:
   - `matched` with a human-readable reason, or
   - `not_matched` with a reason (e.g. “path did not match”, “MIME excluded”).
3. Apply `trigger_mode`:
   - `any`: run if at least one trigger matched.
   - `all`: run only if every enabled trigger matched.
4. If the workflow matches, insert a run (`status='queued'`) with:
   - event snapshot, workflow version, trigger mode;
   - `run_components` snapshots for **all** components:
     - trigger rows: `matched` / `not_matched` / `skipped` (disabled);
     - process/output rows: `pending` / `skipped` (disabled).
   - `ON CONFLICT (workflow_id, event_id) DO NOTHING`.
5. Mark events `dispatched_at = now()`.
6. Commit.

Each trigger row stores its match decision and reason in the `output` JSON (e.g. `{"matched": true, "reason": "basename matched path_regex"}`), which the run UI displays.

A crash rolls back; duplicates are prevented by the unique index. No match still marks the event dispatched. New workflows do not replay history.

### 7.4 Executor: ordered components

Claim a queued run using `run_locks` in one transaction:

~~~sql
-- 1. Pick one queued run.
SELECT r.* FROM workflow.runs r
WHERE r.status = 'queued'
  AND (r.next_attempt_at IS NULL OR r.next_attempt_at <= now())
ORDER BY r.created_at
LIMIT 1
FOR UPDATE SKIP LOCKED;

-- 2. Take the per-workflow lock. 0 rows means another run owns it:
--    roll back and try another run later.
INSERT INTO workflow.run_locks (workflow_id, run_id, locked_until)
VALUES ($1, $2, now() + $3)
ON CONFLICT (workflow_id) DO NOTHING;

-- 3. Mark running.
UPDATE workflow.runs
SET status = 'running', started_at = now(), attempt = attempt + 1,
    locked_by = $4, locked_until = now() + $3
WHERE id = $2;
~~~

- If the workflow was disabled or soft-deleted before this claim, mark the run `canceled` instead of executing.
- Delete the `run_locks` row in the same transaction that finalizes the run, including failure and retry requeue.
- Renew `locked_until` at each component boundary. The lease reaper every 30s removes expired locks and requeues the associated runs.

Execute only process/output stages:

1. Process components in `position` order.
2. Output components in `position` order.
3. Trigger rows are never executed; they remain as run evidence.
4. Disabled components are recorded as `skipped`.

Before each component:

- Check `cancel_requested`.
- Mark `running`, set `started_at`, call the handler.
- Persist `status`, `output`, `error`, `finished_at`, and updated run context.

On failure:

- Mark the component `failed`.
- Stop by default; mark remaining process/output components `skipped`.
- Mark the run `failed` with the component error.
- Retryable failures requeue with exponential backoff until `max_attempts`.

On success:

- If all enabled process/output components succeeded or skipped, mark the run `succeeded`.

Retry:

- Manual retry resumes the existing run at the first non-succeeded process/output component.
- Trigger rows and already-succeeded components are unchanged.
- Stored output is reused (e.g. the chosen copy destination or collision suffix).

Dry run:

- `source='test'`; every action returns a planned result with no side effects and no notifications.
- The UI uses this for “Run test” before enabling a workflow.

Idempotency:

- Rename treats “source missing + destination exists with the expected size” as success.
- Remove missing is success when allowed.
- Copy checks the planned destination and reuses the stored destination on retry; deterministic suffix mode uses the run ID, not a counter.
- Notify uses a stable `Idempotency-Key` across attempts.

### 7.5 Notification components

- Multiple `output.notify.*` components are independent; each runs in order.
- Webhook:
  - POST JSON by default.
  - `X-Workflow-Id`, `X-Workflow-Run-Id`, `X-Workflow-Component-Id`, `X-Workflow-Timestamp`.
  - `X-Workflow-Signature: sha256=<hex HMAC of timestamp + "." + body>`.
  - `Idempotency-Key: <run_id>:<component_id>`.
  - `secret_env` names an env var; secrets are never stored in the DB or returned by the API.
- Email:
  - `to`, `subject_template`, `body_template`.
  - Global SMTP env; TLS via `WORKFLOW_SMTP_TLS` (`starttls` default, `none` for dev).
- URL validation: https required unless `WORKFLOW_WEBHOOK_ALLOW_HTTP=true`; block loopback/link-local/private unless `WORKFLOW_WEBHOOK_ALLOW_PRIVATE=true`.
- Retry 3× on network errors, timeouts, 429, 5xx; no retry on other 4xx.

Default webhook payload:

~~~json
{
  "workflow": {"id": "...", "name": "Archive invoices"},
  "run": {"id": "...", "source": "event", "status": "succeeded"},
  "event": {
    "type": "upload",
    "username": "alice",
    "path": "invoices/invoice_123.pdf",
    "mime": "application/pdf",
    "size": 12345,
    "time": "2026-11-10T16:00:00Z"
  },
  "triggers": [
    {"component_id": "...", "type": "trigger.file.upload", "matched": true}
  ],
  "file": {
    "original_path": "invoices/invoice_123.pdf",
    "current_path": "archive/2026-11-10/invoice_123.pdf"
  },
  "components": [
    {"position": 0, "stage": "process", "type": "process.file.rename", "status": "succeeded"},
    {"position": 0, "stage": "output", "type": "output.file.copy", "status": "succeeded"},
    {"position": 1, "stage": "output", "type": "output.notify.webhook", "status": "succeeded"}
  ]
}
~~~

### 7.6 Workflow service config

| Env | Default | Purpose |
|---|---|---|
| `DATABASE_URL` | required | Same database as the file server. |
| `WORKFLOW_LISTEN` | `:8081` | HTTP API listener. |
| `WORKFLOW_FILE_API_URL` | `http://ftp-server:7000` | Internal file-operations API base URL. |
| `WORKFLOW_SERVICE_TOKEN` | required | Shared token for proxy and internal API. |
| `WORKFLOW_WORKERS` | `4` | Executor worker count. |
| `WORKFLOW_POLL_INTERVAL` | `500ms` | Dispatcher poll interval. |
| `WORKFLOW_MAX_ATTEMPTS` | `3` | Run retry limit. |
| `WORKFLOW_COMPONENT_TIMEOUT` | `60s` | Per-component timeout. |
| `WORKFLOW_RUN_TIMEOUT` | `10m` | Lease/run timeout. |
| `WORKFLOW_RUN_RETENTION_DAYS` | `90` | Delete finished runs/components after this age. |
| `WORKFLOW_SMTP_HOST` / `_PORT` / `_USER` / `_PASS` / `_FROM` | empty | Email notifier. |
| `WORKFLOW_SMTP_TLS` | `starttls` | `starttls` or `none`. |
| `WORKFLOW_WEBHOOK_ALLOW_HTTP` | `false` | Allow plain HTTP webhook URLs (dev only). |
| `WORKFLOW_WEBHOOK_ALLOW_PRIVATE` | `false` | Allow private/loopback webhook targets (dev only). |

---

## 8. HTTP API (workflow service)

Reached through the file server proxy at `/api/v1/workflows` and `/api/v1/workflow-components`.

| Method | Path | Role | Purpose |
|---|---|---|---|
| GET | `/api/v1/workflow-components` | authed | Component catalog (filter by `stage`; optional `key`/`version`) |
| GET | `/api/v1/workflows` | authed | List (search/enabled/pagination) |
| POST | `/api/v1/workflows` | admin | Create |
| GET | `/api/v1/workflows/{id}` | authed | Detail + components |
| PUT | `/api/v1/workflows/{id}` | admin | Full replace + optimistic version |
| DELETE | `/api/v1/workflows/{id}` | admin | Soft delete |
| POST | `/api/v1/workflows/{id}/duplicate` | admin | Clone as a disabled workflow |
| POST | `/api/v1/workflows/{id}/enable` | admin | Enable after validation |
| POST | `/api/v1/workflows/{id}/disable` | admin | Disable |
| POST | `/api/v1/workflows/test-match` | authed | Test a sample against all workflows or an ad-hoc component set |
| POST | `/api/v1/workflows/{id}/test-match` | authed | Test one workflow |
| POST | `/api/v1/workflows/{id}/run` | admin | Manual/dry run |
| GET | `/api/v1/workflows/{id}/runs` | authed | Run history |
| GET | `/api/v1/workflows/{id}/runs/{runId}` | authed | Run + component timeline |
| POST | `/api/v1/workflows/{id}/runs/{runId}/retry` | admin | Retry/resume |
| POST | `/api/v1/workflows/{id}/runs/{runId}/cancel` | admin | Request cancel |
| GET | `/api/v1/workflows/events` | admin | Recent file events |

Create/update body:

~~~json
{
  "name": "Archive invoices",
  "description": "Rename, copy, and notify",
  "enabled": true,
  "version": 3,
  "trigger_mode": "any",
  "components": [
    {
      "stage": "trigger",
      "label": "Invoice PDFs",
      "type": "trigger.file.upload",
      "config": {
        "path_target": "basename",
        "path_regex": "(?i)^invoice_.*\\.pdf$",
        "mime_patterns": ["application/pdf"],
        "mime_regex": "",
        "scope_username": ""
      }
    },
    {
      "stage": "trigger",
      "label": "Scanned PDFs",
      "type": "trigger.file.upload",
      "config": {
        "path_target": "basename",
        "path_regex": "(?i)^scan_.*\\.pdf$",
        "mime_patterns": ["application/pdf"],
        "mime_regex": "",
        "scope_username": ""
      }
    },
    {
      "stage": "process",
      "label": "Archive PDF",
      "type": "process.file.rename",
      "config": {
        "mode": "template",
        "template": "archive/{{.Date}}/{{.Base}}{{.Ext}}",
        "collision": "suffix"
      }
    },
    {
      "stage": "output",
      "label": "Shared copy",
      "type": "output.file.copy",
      "config": {
        "destination_username": "",
        "destination_path": "shared/{{.Date}}/{{.FileName}}",
        "collision": "suffix"
      }
    },
    {
      "stage": "output",
      "label": "Notify webhook",
      "type": "output.notify.webhook",
      "config": {
        "url": "https://hooks.example.com/file-events",
        "secret_env": "WORKFLOW_WEBHOOK_SECRET",
        "body_template": ""
      }
    }
  ]
}
~~~

- `version` required on update; mismatch returns 409 with the current version.
- Success increments version, sets `updated_by` from the proxy header, updates `updated_at`.
- Component IDs are server-generated; clients send components in order. Positions are assigned per stage by array order. Replace components transactionally.
- On save, the server assigns each component the registry’s **current** `type_version`. Existing workflows keep their stored version until edited; saving migrates them to the current version and validation catches incompatibilities.
- Enabling a workflow runs full validation, including component-type existence, version resolution, stage assignment, and schema checks.

Test/match and manual-run request shapes:

~~~json
// POST /api/v1/workflows/test-match
{"sample": {"username": "alice", "path": "invoices/invoice_123.pdf", "mime": "application/pdf", "event_type": "upload"},
 "trigger_mode": "any",
 "components": []}

// POST /api/v1/workflows/{id}/run
{"username": "alice", "path": "invoices/invoice_123.pdf", "mime": "application/pdf",
 "event_type": "upload", "dry_run": true}
~~~

- Test-match returns `matched` plus per-trigger results with reasons and matched IDs.
- Register static routes before `{id}` routes.

Error shape:

~~~json
{"error": "invalid component", "code": "validation_failed",
 "fields": {"components[0].config.path_regex": "invalid regexp"}}
~~~

---

## 9. Web UI

### 9.1 Routes

Add under `authedRoute` in `apps/web/src/router.tsx`:

- `/workflows` — list.
- `/workflows/new` — create editor.
- `/workflows/$id/edit` — edit editor.
- `/workflows/$id/runs` — run history.
- `/workflows/$id/runs/$runId` — run detail.

Add a **Workflows** link in `apps/web/src/routes/_authed.tsx`.

### 9.2 Component builder

The editor is a three-section builder, not a fixed step form:

~~~text
┌ Edit workflow ─────────────────────────────────────────────┐
│ Name [........................]  Enabled [ ]              │
│ Trigger mode: (•) any  ( ) all                            │
│                                                            │
│ TRIGGERS                          [+ Add trigger]         │
│ ┌────────────────────────────────────────────────────────┐ │
│ │ File uploaded      path: invoice_*.pdf   MIME: PDF  × │ │
│ │ {config form}                                          │ │
│ └────────────────────────────────────────────────────────┘ │
│ ┌────────────────────────────────────────────────────────┐ │
│ │ File removed       path: archive/*         ×           │ │
│ └────────────────────────────────────────────────────────┘ │
│                                                            │
│ PROCESS                           [+ Add process]         │
│ ┌────────────────────────────────────────────────────────┐ │
│ │ 1. Rename file     archive/{{.Date}}/...   ↑ ↓ ×      │ │
│ └────────────────────────────────────────────────────────┘ │
│                                                            │
│ OUTPUT                            [+ Add output]          │
│ ┌────────────────────────────────────────────────────────┐ │
│ │ 1. Copy file       shared/{{.Date}}/...   ↑ ↓ ×       │ │
│ ├────────────────────────────────────────────────────────┤ │
│ │ 2. Webhook         https://hooks...        ↑ ↓ ×      │ │
│ └────────────────────────────────────────────────────────┘ │
│                                                            │
│ [Test trigger] [Run test] [Cancel] [Save]                 │
└────────────────────────────────────────────────────────────┘
~~~

- **Add component** opens the catalog filtered by the target stage. The user picks a type; a card is added with default config.
- Components can be enabled/disabled, reordered with up/down buttons, duplicated, or removed.
- Cards show the instance label when set, otherwise the type name. Labels are optional and capped at 80 characters.
- Changing a component’s type resets its config to the new type’s defaults.
- Multiple components per stage are allowed; validation enforces at least one enabled trigger and at least one enabled action.

### 9.3 Schema-driven forms

New `apps/web/src/components/workflow/SchemaForm.tsx`:

- Renders fields from `config_schema`: string, number, boolean, enum/select, array of strings (chips), nested object, and textarea.
- Uses `ui_schema` for field order, labels, help text, and widgets.
- Built-in widgets:
  - `regex` — validates RE2-ish syntax client-side and shows the server error.
  - `template-path` — inserts/validates `{{.Date}}`-style variables.
  - `mime-patterns` — chip input with presets (PDF, Images `image/*`, Documents, Any).
  - `secret-env` — env-var name input, never a secret value.
  - `json` — fallback textarea for unsupported schema shapes.
- New backend component types appear automatically as long as their schema uses the supported subset. No new React form is required for simple types.

Other components:

- `WorkflowForm.tsx` — metadata, trigger mode, stage sections, save/test.
- `ComponentCard.tsx` — type name, enable toggle, reorder, remove, config form.
- `ComponentPicker.tsx` — catalog modal filtered by stage.
- `TriggerTestPanel.tsx` — sample username/path/MIME/event type; shows per-trigger matched/not matched + reason.
- `RunStatusBadge.tsx`, `RunTimeline.tsx` — component-level timeline including trigger rows.

### 9.4 UX behavior

**List:** Name, trigger summary (e.g. “2 triggers · 1 process · 3 outputs”), enabled toggle, last run, updated, actions (Edit, Runs, Duplicate, Delete). Search/pagination. Mutations hidden for non-admin.

**Editor:** three stage sections; add/reorder/disable; per-component forms; validation panel; unsaved-changes warning.

**Test:** “Test trigger” evaluates trigger components against a sample and shows which matched. “Run test” performs a dry run of process/output components and shows planned results without modifying files or sending notifications.

**Runs:** table of run ID, status, source, event path, started, duration, attempt. Detail shows the trigger results and each component’s status/output/error. Auto-refresh while running. Retry failed runs.

### 9.5 Data layer

New `apps/web/src/lib/workflows.ts`:

- Types: `ComponentType`, `WorkflowComponent`, `Workflow`, `WorkflowRun`, `WorkflowRunComponent`, `ValidationError`.
- Functions: `listComponentTypes`, list/get/create/update/delete/enable/disable, duplicate, testMatch, run, listRuns, getRun, retry, cancel, listFileEvents.
- Reuse `api<T>`.

### 9.6 Styling

Reuse existing ledger/form/switch/button classes. Add stage sections, component cards, reorder controls, status badges, timeline, chip input, schema form styles, and test-result styles. No new UI library.

---

## 10. Execution semantics and edge cases

### 10.1 Trigger evaluation

- Every enabled trigger component is evaluated; `any` and `all` modes are supported.
- A trigger’s `config` owns the event type, path filter, MIME filter, and user scope. Disabled triggers are ignored.
- A run records all trigger results (matched/not matched) so users can debug why it fired.
- If `trigger_mode=all` and enabled triggers accept different event types, validation rejects the workflow because no single event can match all.

### 10.2 Ordering and races

- Process components run in position order, then output components in position order.
- Runs use the component snapshots captured at dispatch. Editing, reordering, or disabling components after dispatch does not change an in-flight run; disabling the whole workflow before it is claimed cancels it.
- Runs of the same workflow are serialized by `run_locks`.
- Different workflows can run concurrently on the same file; one may fail visibly if both rename. v1 does not add cross-workflow locks.
- The event carries size/MIME at event time. A user could replace the file before the run. The executor verifies existence and size before rename/remove and fails safely if they differ.
- Workflow file operations never emit new file events.

### 10.3 Failure semantics

- Stop at the first failed process/output component by default; later process/output components are skipped.
- Performed file operations are not rolled back; the timeline and audit show the exact state.
- A notification failure after successful file operations marks the run failed at that output component; the file state remains changed, and the UI says so.
- Automatic retry only for retryable errors; validation/4xx requires an admin fix and manual retry.
- Manual retry resumes at the first non-succeeded process/output component and reuses stored outputs.
- A future `on_error: continue` option can let later outputs run after a failure; not in v1.

### 10.4 Idempotency

- Unique `(workflow_id, event_id)` prevents duplicate runs.
- `dispatched_at` is set in the same transaction as run creation.
- Components are safe on repeat; stored outputs drive resume.
- Notifications are at-least-once; consumers deduplicate on `Idempotency-Key`.

### 10.5 Security

- Admin-only management; read-only users can view workflows/runs.
- Paths root-relative, validated server-side; reject `..`, absolute, empty, oversized.
- RE2 only; cap pattern/path lengths.
- Webhook SSRF protections (scheme, private/loopback blocking).
- Secrets are env references; never returned.
- Service tokens constant-time compared, never logged.
- Proxy strips client identity headers.
- Workflow CRUD and manual runs audited.

---

## 11. Deployment and CI

### 11.1 Docker Compose

Add a `workflow` service:

~~~yaml
workflow:
  build: ./apps/workflow
  depends_on:
    postgres:
      condition: service_healthy
    ftp-server:
      condition: service_started
  environment:
    DATABASE_URL: postgres://ftp:ftp@postgres:5432/ftp?sslmode=disable
    WORKFLOW_LISTEN: ":8081"
    WORKFLOW_FILE_API_URL: http://ftp-server:7000
    WORKFLOW_SERVICE_TOKEN: dev-workflow-token
    WORKFLOW_WORKERS: "4"
    WORKFLOW_SMTP_HOST: ""
    WORKFLOW_SMTP_PORT: "587"
    WORKFLOW_SMTP_FROM: ""
    WORKFLOW_WEBHOOK_ALLOW_PRIVATE: "true"
  ports:
    - "8081:8081"
~~~

Add to `ftp-server`:

~~~yaml
WORKFLOW_API_URL: http://workflow:8081
WORKFLOW_SERVICE_TOKEN: dev-workflow-token
~~~

Use a real secret in production.

### 11.2 Kubernetes

- `k8s/workflow-deployment.yaml`, `k8s/workflow-service.yaml` (ClusterIP only; no HTTPRoute).
- Add both to `k8s/kustomization.yaml` resources and image overrides.
- ConfigMap: `WORKFLOW_API_URL`, `WORKFLOW_FILE_API_URL`, `WORKFLOW_LISTEN`.
- Secret: `WORKFLOW_SERVICE_TOKEN`, `WORKFLOW_SMTP_PASSWORD`, `WORKFLOW_WEBHOOK_SECRET`.
- 1 replica initially; multiple replicas are supported by the DB queue, but 1 keeps ordering/ops simple.
- Probes: `/healthz`, `/readyz`.
- Do not expose the workflow service through the gateway; the file server proxy is the public path.

### 11.3 CI/release

- `.github/workflows/ci.yml` matrix entry:

~~~yaml
- app: workflow
  context: apps/workflow
  image_var: IMAGE_NAME_WORKFLOW
~~~

- `.github/workflows/release.yml`: govulncheck/lint/test/build for `apps/workflow` (or matrix the Go jobs).
- Root `package.json`: `dev:workflow`, `test:workflow`, `lint:workflow`; extend `typecheck`.
- `apps/workflow/Makefile` mirroring `apps/ftp/Makefile`.
- `apps/workflow/Dockerfile` multi-stage -> distroless, binary `workflow-server`.
- Update `README.md` and `.env.example`.

### 11.4 Startup ordering and grants

- `apps/ftp` runs `0008_file_events` at boot.
- `apps/workflow` runs its migrations, then waits for `public.file_events` (`to_regclass('public.file_events')` with backoff) before starting the dispatcher.
- With separate DB roles:

~~~sql
-- If the app role runs migrations, it also needs CREATE on the schema.
GRANT USAGE, CREATE ON SCHEMA workflow TO workflow_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA workflow TO workflow_app;
GRANT SELECT ON public.file_events TO workflow_app;
GRANT UPDATE (dispatched_at) ON public.file_events TO workflow_app;
GRANT INSERT ON public.audit_events TO workflow_app;
GRANT USAGE ON SEQUENCE public.audit_events_id_seq TO workflow_app;
~~~

The existing single-role setup needs no extra grants.

---

## 12. Testing

### 12.1 File server

- `internal/fileevents/mime_test.go`: extension, parameter stripping, sniff fallback/always/off, unknown, size cap.
- `internal/fileevents/publisher_test.go`: round-trip with `TEST_DATABASE_URL` (skip if unset).
- `internal/fsdriver/audit_test.go`: fake publisher; upload on close; no event on failed write; remove metadata; rename old/new; RemoveAll directory flag.
- `internal/admin/internal_files_api_test.go`: bad token, traversal, rename collision, cross-user copy, remove missing, audit.
- `internal/admin/workflow_proxy_test.go`: identity injection, header stripping, write role gate, 502.
- `internal/config/config_test.go`: new defaults.

### 12.2 Workflow service

- `internal/components/registry_test.go`: catalog completeness, unique keys, stage lists, schema presence, type version.
- `internal/components/validate_test.go`: schema validation, semantic checks, stage mismatch, unknown type/version.
- `internal/engine/dispatch_test.go`: multiple triggers, any/all, matched/not-matched snapshots, no-match dispatch, duplicate idempotency, disabled triggers.
- `internal/engine/executor_test.go`: process then output order, multiple outputs, disabled components, component failure stops run, retry resume, lease expiry, cancellation, dry run.
- `internal/action/fileops_test.go`: request shape, token/correlation headers, 4xx vs retryable mapping.
- `internal/action/notify_test.go`: HMAC, idempotency key, 5xx retry, 4xx no retry, email templates.
- `internal/store/*_test.go`: CRUD, version conflict, component replacement, queue claim, lock serialization, cleanup.
- `internal/httpapi/*_test.go`: validation errors, roles, proxy token, catalog endpoint.

### 12.3 Frontend

- `bun run typecheck` and `bun run build`.
- SchemaForm tests for supported field types and fallback JSON.
- Playwright smoke: add two triggers, one process, two outputs, test match, save, disable, view runs.
- Manual keyboard/accessibility checks for reorder buttons and labels.

### 12.4 End-to-end scenario

1. Start Postgres + file server + workflow + web with local storage.
2. Create a workflow:
   - triggers: upload `^invoice_.*\.pdf$` (MIME PDF), upload `^scan_.*\.pdf$` (MIME PDF);
   - process: rename to `archive/{{.Date}}/...`;
   - outputs: copy to `shared/...`, webhook notify.
3. Upload `invoice_123.pdf` over FTP.
4. Verify:
   - event row and dispatched flag;
   - one run showing both trigger results (one matched, one not), rename/copy/webhook succeeded;
   - renamed and copied files exist;
   - signed webhook payload includes trigger and component results;
   - audit rows for rename/copy.
5. Delete the renamed file over FTP; verify the remove trigger (if configured) or no-op.
6. Re-upload the same filename; verify no duplicate run and correct collision handling.
7. Stop the workflow service mid-run; restart; verify the lease reaper resumes/fails cleanly without duplicating side effects.

---

## 13. Phased implementation

### Phase 1 — Event capture

- [ ] Migration `0008_file_events`.
- [ ] `internal/fileevents` (event, publisher, MIME).
- [ ] Extend `AuditFS`; constructor gains user ID + publisher.
- [ ] Config/env + retention cleanup.
- [ ] Tests.

**Done when:** FTP/SFTP uploads/removes produce correct events with MIME, and no workflow code exists yet.

### Phase 2 — Internal file API + proxy

- [ ] `core.Copier` + COS/local implementations.
- [ ] `/internal/v1/files/*` handlers, token middleware, audit, path validation.
- [ ] `/api/v1/workflows` and `/api/v1/workflow-components` reverse proxy with session auth, role gate, identity headers.
- [ ] Tests.

**Done when:** an authenticated admin reaches a stubbed workflow endpoint through the file server, and an internal caller can rename/copy/remove with audit.

### Phase 3 — Component registry + workflow CRUD

- [ ] Component registry interface, built-in types, schemas, catalog endpoint.
- [ ] `workflow` schema migration with components.
- [ ] Domain validation: stage assignment, schema, semantic rules.
- [ ] Store CRUD with optimistic versioning and component replacement.
- [ ] HTTP API + proxy-token/identity middleware.
- [ ] Tests.

**Done when:** workflows can be composed from multiple components, validated, and saved; invalid type/stage/config combinations are rejected with component-level errors.

### Phase 4 — Dispatcher + executor

- [ ] Event polling/claim/dispatch with multiple trigger evaluation.
- [ ] `any`/`all` trigger modes and per-trigger results.
- [ ] Run + `run_components` snapshots.
- [ ] Executor with process/output ordering, `run_locks`, leases, retries, cancellation.
- [ ] Rename/remove actions via the internal file API.
- [ ] Run/history APIs.
- [ ] Tests.

**Done when:** an upload event matches one or more triggers, creates one run, and executes process/output components exactly once and in order.

### Phase 5 — Outputs

- [ ] Copy action with destination user/path templates and collision policies.
- [ ] Webhook/email notification components with retries, HMAC, idempotency keys.
- [ ] Multiple output components.
- [ ] Tests.

**Done when:** a workflow can copy to multiple folders and deliver multiple notifications.

### Phase 6 — Web UI

- [ ] Routes + nav link.
- [ ] Component builder with stage sections, catalog picker, add/remove/reorder/enable.
- [ ] Schema-driven forms + built-in widgets.
- [ ] Trigger test panel, dry run, run timeline.
- [ ] API client/types.
- [ ] Typecheck/build; Playwright smoke.

**Done when:** an admin can compose a workflow from catalog components entirely in the browser, and a new backend component type with a supported schema appears without a new form.

### Phase 7 — Deployment, CI, docs, hardening

- [ ] Compose + k8s manifests + secrets/config.
- [ ] CI matrix and release jobs.
- [ ] README, `.env.example`, runbook.
- [ ] Metrics/health, retention jobs, log fields.
- [ ] Security review: traversal, SSRF, tokens, audit.
- [ ] Load/soak test.

**Done when:** a fresh deployment works end-to-end and CI builds both Go services and the web app.

---

## 14. Risks and decisions

### 14.1 Risks

| Risk | Mitigation |
|---|---|
| Component type evolution breaks saved workflows | Store `type_version`; mark unknown types invalid; document migration; validate on enable. |
| Schema/UI divergence | Backend schema is the source of truth; frontend supports a documented subset; JSON fallback. |
| Multiple triggers behave unexpectedly | Explicit `any`/`all` mode; per-trigger match reasons in run history; UI test panel. |
| Duplicate runs / side effects | Unique `(workflow_id,event_id)`; idempotent components; stored outputs for retry. |
| Lost events on crash | Durable synchronous insert; audit still records the file op if insert fails. |
| Workflow loops (copy triggers upload) | Internal ops bypass `AuditFS`; no events emitted. |
| Race with user operations | Per-workflow serialization; verify size/existence; visible failure. |
| Large copy memory blow-up | COS server-side copy / local stream copy; fallback size guard. |
| SSRF via webhook URL | Scheme allowlist; private/loopback blocking; admin-only config. |
| Proxy failure blocks workflow UI | File transfers unaffected; workflow routes 502/503. |
| Destructive workflow enabled by mistake | Default disabled; dry run; test match; run history; audit; admin-only. |

### 14.2 Decisions to confirm

1. **Trigger combination:** support both `any` and `all` (recommended), or `any` only?
2. **Multiple output execution:** sequential in order (recommended), or parallel?
3. **Output failure behavior:** stop at the first failed output (recommended for v1), or continue remaining outputs? A per-component `on_error: stop|continue` can be added if you want both.
4. **Component catalog:** code-defined registry (recommended), or database-managed component types?
5. **UI forms:** schema-driven generic forms with JSON fallback (recommended), or custom forms per type?
6. **Component scope:** instances belong to one workflow (recommended), or reusable across workflows?
7. **Notifications:** webhook + email components (recommended), or webhook only?
8. **Copy target:** same user by default with optional target user (recommended), or same user only?
9. **Roles:** admin mutations, read-only can view runs (recommended), or admin-only for everything?

### 14.3 Acceptance criteria

- [ ] Backend exposes a component catalog with type, stage, version, and config schema.
- [ ] Users can add multiple trigger, process, and output components; assign types; configure; enable/disable; reorder; remove.
- [ ] The server rejects a component assigned to a stage its type does not allow.
- [ ] `any`/`all` trigger modes work; run history shows each trigger’s result and reason.
- [ ] Multiple outputs execute in order.
- [ ] Process components execute in order; remove is terminal for later file-mutating components.
- [ ] A failed component stops the run, is visible with an error, and can be retried without duplicating successful operations.
- [ ] Workflow file operations are audited and do not re-trigger workflows.
- [ ] Runs show per-component status, output paths, bytes, duration, errors.
- [ ] Webhooks are signed and retried; email works with configured SMTP.
- [ ] Path traversal is rejected at both layers.
- [ ] The workflow service shares the DB but uses its own schema/migration table.
- [ ] Compose, k8s, and CI build all three components.
- [ ] With `WORKFLOW_API_URL` unset, file transfers and the existing admin UI work normally.
