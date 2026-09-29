DROP INDEX IF EXISTS audit_events_backend_location_idx;
ALTER TABLE audit_events DROP COLUMN backend_location;