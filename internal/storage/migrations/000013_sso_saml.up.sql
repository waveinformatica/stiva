-- SAML 2.0 identity providers for browser login (UI-managed SSO). The IdP
-- signing certificate is public key material, so it lives here beside the
-- endpoints rather than in the vault; client secrets, when a provider needs
-- one, stay vault-side as before.

ALTER TABLE sso_providers
    ADD COLUMN IF NOT EXISTS entity_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS idp_sso_url TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS idp_cert TEXT NOT NULL DEFAULT '';
