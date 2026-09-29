ALTER TABLE ftp_users ADD COLUMN ftp_enabled boolean NOT NULL DEFAULT true;
ALTER TABLE ftp_users ADD COLUMN sftp_enabled boolean NOT NULL DEFAULT false;
ALTER TABLE ftp_users ADD COLUMN sftp_public_key text;
