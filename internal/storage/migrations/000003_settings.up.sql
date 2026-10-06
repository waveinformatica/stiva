CREATE TABLE IF NOT EXISTS auth_settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Anonymous access is OFF by default; it is enabled from the admin UI.
INSERT INTO auth_settings (key, value) VALUES ('allow_anonymous', 'false')
    ON CONFLICT (key) DO NOTHING;
