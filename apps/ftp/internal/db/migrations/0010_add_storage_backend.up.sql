-- Per-user storage backend ("cos" or "local"). NULL means "use the deployment
-- default" (STORAGE_BACKEND), so existing users keep resolving exactly as they
-- did before this migration without a data backfill.
ALTER TABLE ftp_users ADD COLUMN storage_backend varchar(32);
