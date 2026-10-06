-- API keys were stored in cleartext, so anyone able to read the database (or a
-- backup of it) obtained directly usable credentials. Store a SHA-256 hash
-- instead, plus a short non-secret prefix used to identify the key in the admin
-- UI. Keys are 24 random bytes, so a plain fast hash is sufficient here: there
-- is nothing to brute-force.
--
-- Existing keys keep working: the hash is computed from the stored cleartext,
-- so the value clients already hold still matches. Requires PostgreSQL 11+ for
-- the built-in sha256().

ALTER TABLE auth_api_keys ADD COLUMN IF NOT EXISTS key_hash   TEXT;
ALTER TABLE auth_api_keys ADD COLUMN IF NOT EXISTS key_prefix TEXT;

UPDATE auth_api_keys
   SET key_hash   = encode(sha256(key::bytea), 'hex'),
       key_prefix = left(key, 11)
 WHERE key_hash IS NULL;

-- Any row that still has no hash carries no recoverable credential.
DELETE FROM auth_api_keys WHERE key_hash IS NULL;

ALTER TABLE auth_api_keys DROP CONSTRAINT IF EXISTS auth_api_keys_pkey;
ALTER TABLE auth_api_keys DROP COLUMN IF EXISTS key;
ALTER TABLE auth_api_keys ALTER COLUMN key_hash SET NOT NULL;
ALTER TABLE auth_api_keys ADD PRIMARY KEY (key_hash);

CREATE INDEX IF NOT EXISTS idx_api_keys_prefix ON auth_api_keys(key_prefix);
