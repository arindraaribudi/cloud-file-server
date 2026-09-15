CREATE TABLE ftp_users (
  id              BIGSERIAL PRIMARY KEY,
  username        VARCHAR(64) UNIQUE NOT NULL,
  password_hash   VARCHAR(255) NOT NULL,
  root_folder     VARCHAR(512) NOT NULL,
  cos_bucket      VARCHAR(256) NOT NULL,
  cos_region      VARCHAR(64),
  enabled         BOOLEAN NOT NULL DEFAULT TRUE,
  allow_active_mode BOOLEAN NOT NULL DEFAULT FALSE,
  refuse_overwrite BOOLEAN NOT NULL DEFAULT FALSE,
  max_sessions    INT NOT NULL DEFAULT 5,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at      TIMESTAMPTZ
);

CREATE TABLE admin_users (
  id              BIGSERIAL PRIMARY KEY,
  username        VARCHAR(64) UNIQUE NOT NULL,
  password_hash   VARCHAR(255) NOT NULL,
  role            VARCHAR(32) NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE audit_events (
  id          BIGSERIAL,
  event_time  TIMESTAMPTZ NOT NULL,
  username    VARCHAR(64) NOT NULL,
  client_ip   INET,
  session_id  UUID,
  action      VARCHAR(32) NOT NULL,
  path        TEXT,
  bytes       BIGINT,
  success     BOOLEAN NOT NULL,
  source      VARCHAR(8),
  detail      JSONB,
  PRIMARY KEY (id, event_time)
) PARTITION BY RANGE (event_time);

CREATE INDEX ON audit_events (username, event_time DESC);
CREATE INDEX ON audit_events (action, event_time DESC);
CREATE INDEX ON audit_events (success, event_time DESC);
CREATE INDEX ON audit_events (client_ip, event_time DESC);

CREATE TABLE audit_sessions (
  id          UUID PRIMARY KEY,
  username    VARCHAR(64) NOT NULL,
  client_ip   INET NOT NULL,
  started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen   TIMESTAMPTZ NOT NULL DEFAULT now(),
  terminated  BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE INDEX ON audit_sessions (username) WHERE terminated = FALSE;

CREATE TABLE audit_events_default PARTITION OF audit_events DEFAULT;
