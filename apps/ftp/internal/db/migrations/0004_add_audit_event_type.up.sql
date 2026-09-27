ALTER TABLE audit_events ADD COLUMN event_type VARCHAR(16) NOT NULL DEFAULT 'other';

-- Backfill existing rows so the column is consistent on day one. The
-- application also derives event_type from action on insert, so this only
-- matters for rows written before the column existed.
UPDATE audit_events SET event_type = 'auth'        WHERE action IN ('LOGIN', 'LOGOUT');
UPDATE audit_events SET event_type = 'file_access' WHERE action IN ('UPLOAD', 'DOWNLOAD', 'LIST', 'DELETE', 'RENAME', 'MKDIR', 'FILE_DOWNLOAD');
UPDATE audit_events SET event_type = 'admin'       WHERE action LIKE 'ADMIN%';
UPDATE audit_events SET event_type = 'system'      WHERE action = 'CRED_REFRESH';

CREATE INDEX ON audit_events (event_type, event_time DESC);