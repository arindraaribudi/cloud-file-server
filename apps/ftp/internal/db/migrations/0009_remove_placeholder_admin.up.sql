-- Remove the old unusable placeholder admin created by the original seed.
-- Only delete the exact known placeholder hash; a real "admin" account is
-- preserved.
DELETE FROM admin_users
WHERE username = 'admin'
  AND password_hash = '$2a$10$replace-with-real-bcrypt-hash-of-changeme';
