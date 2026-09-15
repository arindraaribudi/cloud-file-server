-- Default admin: username + bcrypt-hashed bootstrap password "changeme-please".
-- Adjust the hash for your environment; this is generated with:
--   python -c "import bcrypt; print(bcrypt.hashpw(b'changeme-please', bcrypt.gensalt(10)).decode())"
-- Idempotent: re-running is a no-op.
INSERT INTO admin_users (username, password_hash, role)
SELECT 'admin', '$2a$10$replace-with-real-bcrypt-hash-of-changeme', 'SuperAdmin'
WHERE NOT EXISTS (SELECT 1 FROM admin_users WHERE username = 'admin');
