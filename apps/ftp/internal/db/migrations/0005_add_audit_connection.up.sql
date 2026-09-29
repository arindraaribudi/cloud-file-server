ALTER TABLE audit_events ADD COLUMN connection_type VARCHAR(16) NOT NULL DEFAULT 'ftp';
ALTER TABLE audit_events ADD COLUMN root_folder VARCHAR(512) NOT NULL DEFAULT '';

CREATE INDEX ON audit_events (connection_type, event_time DESC);