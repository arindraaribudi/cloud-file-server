ALTER TABLE audit_events ADD COLUMN backend_location VARCHAR(32) NOT NULL DEFAULT 'cos';

CREATE INDEX ON audit_events (backend_location, event_time DESC);