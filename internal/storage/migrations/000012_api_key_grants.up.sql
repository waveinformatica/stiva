-- Per-key grants: an API key authenticates as its owner, but only within the
-- listed (role, scope) pairs. The request must additionally pass the owner's
-- own grants, so a key can never exceed its owner however it is scoped. An
-- empty array preserves the legacy behavior (full owner power).
ALTER TABLE auth_api_keys ADD COLUMN IF NOT EXISTS grants JSONB NOT NULL DEFAULT '[]';
