DROP INDEX IF EXISTS audit_events_connection_type_idx;
ALTER TABLE audit_events DROP COLUMN root_folder;
ALTER TABLE audit_events DROP COLUMN connection_type;