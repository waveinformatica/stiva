-- Anonymous callers become named identities with recognition rules, instead of
-- a single global on/off switch.
--
-- The point is to be able to say "pull without credentials, but only from the
-- cluster" explicitly and controllably: one identity matching the node and pod
-- networks, another with no filter for everyone else, each carrying whatever
-- grants it should. A caller with no credentials is matched against them in
-- order, and the first that fits becomes the principal — from there on it goes
-- through the ordinary authorization path.

ALTER TABLE auth_users ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'local';
ALTER TABLE auth_users ADD COLUMN IF NOT EXISTS description TEXT NOT NULL DEFAULT '';
ALTER TABLE auth_users ADD COLUMN IF NOT EXISTS email TEXT NOT NULL DEFAULT '';
ALTER TABLE auth_users ADD COLUMN IF NOT EXISTS match JSONB NOT NULL DEFAULT '{}'::jsonb;

CREATE INDEX IF NOT EXISTS idx_auth_users_kind ON auth_users(kind);

-- Reproduce today's behaviour as an explicit identity rather than a hidden
-- default: an anonymous caller matching nothing else lands here, and the grant
-- already made to the "anonymous" subject applies to it. Removing this row is
-- how anonymous access gets closed to everyone not covered by a narrower rule.
--
-- Again only where there is behaviour to preserve. A catch-all that matches
-- every address is not something a new installation should find already in
-- place; it is created from the UI, deliberately, if it is wanted at all.
INSERT INTO auth_users (name, password_hash, disabled, admin, password_change_required, created_at, kind, match)
SELECT 'anonymous', '', false, false, false, extract(epoch from now())::bigint, 'anonymous', '{}'::jsonb
WHERE EXISTS (SELECT 1 FROM registries_meta)
ON CONFLICT (name) DO NOTHING;
