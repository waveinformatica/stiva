-- Lossy by design: cleartext keys cannot be recovered from their hashes. This
-- restores the previous column shape, but every existing key stops working and
-- must be reissued.

DROP INDEX IF EXISTS idx_api_keys_prefix;

ALTER TABLE auth_api_keys ADD COLUMN IF NOT EXISTS key TEXT;
UPDATE auth_api_keys SET key = key_hash WHERE key IS NULL;

ALTER TABLE auth_api_keys DROP CONSTRAINT IF EXISTS auth_api_keys_pkey;
ALTER TABLE auth_api_keys DROP COLUMN IF EXISTS key_hash;
ALTER TABLE auth_api_keys DROP COLUMN IF EXISTS key_prefix;
ALTER TABLE auth_api_keys ADD PRIMARY KEY (key);
