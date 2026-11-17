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
