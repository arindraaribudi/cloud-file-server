DROP INDEX IF EXISTS audit_events_event_type_idx;
ALTER TABLE audit_events DROP COLUMN event_type;