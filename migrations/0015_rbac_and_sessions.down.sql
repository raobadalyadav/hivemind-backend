DROP TABLE IF EXISTS refresh_tokens;
ALTER TABLE users DROP COLUMN role;
DROP TYPE user_role;
