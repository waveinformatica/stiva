-- SSO providers managed from the admin UI (never from the config file).
--
-- Only the non-secret half lives here: display and routing fields plus the
-- OIDC/OAuth2/CAS endpoints. The client secret, when the provider needs one,
-- lives in the vault under key 'sso/<id>' like every other credential, so a
-- database dump alone never exposes it. Updating a provider without a secret
-- keeps the vault entry; deleting the provider drops it.

CREATE TABLE IF NOT EXISTS sso_providers (
    id            TEXT PRIMARY KEY,
    label         TEXT NOT NULL DEFAULT '',
    provider      TEXT NOT NULL,
    client_id     TEXT NOT NULL DEFAULT '',
    tenant        TEXT NOT NULL DEFAULT '',
    base_url      TEXT NOT NULL DEFAULT '',
    issuer        TEXT NOT NULL DEFAULT '',
    authorize_url TEXT NOT NULL DEFAULT '',
    token_url     TEXT NOT NULL DEFAULT '',
    userinfo_url  TEXT NOT NULL DEFAULT '',
    scope         TEXT NOT NULL DEFAULT '',
    username_claim TEXT NOT NULL DEFAULT '',
    groups_claim  TEXT NOT NULL DEFAULT '',
    audience      TEXT NOT NULL DEFAULT '',
    admin_group   TEXT NOT NULL DEFAULT '',
    enabled       BOOLEAN NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
