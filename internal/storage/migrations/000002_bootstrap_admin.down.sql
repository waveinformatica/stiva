DELETE FROM auth_users WHERE name = 'admin';
ALTER TABLE auth_users DROP COLUMN IF EXISTS password_change_required;
