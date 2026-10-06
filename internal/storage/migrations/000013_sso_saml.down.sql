ALTER TABLE sso_providers
    DROP COLUMN IF EXISTS entity_id,
    DROP COLUMN IF EXISTS idp_sso_url,
    DROP COLUMN IF EXISTS idp_cert;
