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
