-- Seed the initial global administrator. The password is the bcrypt hash of
-- "admin" (cost 10). The account is flagged password_change_required so the
-- user is forced to set a personal password on first login.
ALTER TABLE auth_users ADD COLUMN IF NOT EXISTS password_change_required BOOLEAN NOT NULL DEFAULT false;

INSERT INTO auth_users (name, password_hash, disabled, admin, password_change_required, created_at)
SELECT 'admin', '$2a$10$mR1626cNRywcKj4z02l0AOncmr6G0gDnnXqZseofCYcq5kW9IhG5i', false, true, true, extract(epoch from now())::bigint
WHERE NOT EXISTS (SELECT 1 FROM auth_users WHERE name = 'admin');
