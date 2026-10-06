-- Vault for credentials that must not sit in cleartext in configuration rows
-- (object-store keys, LDAP bind passwords, proxy upstream tokens).
--
-- Envelope encryption: every secret gets its own random data key, which
-- encrypts the value; the data key is then wrapped with the process master key
-- (REGISTRY_VAULT_KEY). Rotating the master key therefore only requires
-- re-wrapping the data keys, not re-encrypting every value.
--
-- public_data holds the non-secret half of a credential — an S3 access key id,
-- an LDAP bind DN — so one entry describes one credential instead of splitting
-- it across two fields that can drift apart.

CREATE TABLE IF NOT EXISTS secrets (
    key         TEXT PRIMARY KEY,
    description TEXT NOT NULL DEFAULT '',
    enc_key     BYTEA NOT NULL,
    enc_value   BYTEA NOT NULL,
    public_data JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
