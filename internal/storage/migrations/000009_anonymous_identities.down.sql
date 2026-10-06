DELETE FROM auth_users WHERE kind = 'anonymous';
DROP INDEX IF EXISTS idx_auth_users_kind;
ALTER TABLE auth_users DROP COLUMN IF EXISTS match;
ALTER TABLE auth_users DROP COLUMN IF EXISTS email;
ALTER TABLE auth_users DROP COLUMN IF EXISTS description;
ALTER TABLE auth_users DROP COLUMN IF EXISTS kind;
