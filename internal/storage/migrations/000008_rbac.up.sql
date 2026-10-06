-- A single authorization model, replacing two half-working ones.
--
-- Before this, roles and role assignments existed but no code path ever read
-- them: assigning a role granted nothing. Access was decided by the `admin`
-- flag, by per-registry ACLs, and by group claims that only federated identities
-- ever carried. Local users had no groups at all, so "group:x" could never match
-- for them.
--
-- Now: roles hold permissions, principals are bound to roles within a scope, and
-- local users can belong to groups just like federated ones do.

CREATE TABLE IF NOT EXISTS auth_groups (
    name        TEXT PRIMARY KEY,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS auth_group_members (
    group_name TEXT NOT NULL REFERENCES auth_groups(name) ON DELETE CASCADE,
    username   TEXT NOT NULL,
    PRIMARY KEY (group_name, username)
);
CREATE INDEX IF NOT EXISTS idx_group_members_user ON auth_group_members(username);

-- Permissions were a comma-separated string with no defined vocabulary, which
-- is why the UI could not offer a multi-select: there was nothing to choose
-- from. The vocabulary is now enumerated in code and referenced here.
CREATE TABLE IF NOT EXISTS auth_role_permissions (
    role_name  TEXT NOT NULL,
    permission TEXT NOT NULL,
    PRIMARY KEY (role_name, permission)
);

-- A binding grants one role to one principal within one scope. The scope is
-- "*", a registry name, or a registry plus a repository path pattern
-- ("docker:kosmos/**"). Paths have arbitrary depth, so the pattern is matched
-- rather than split into fixed levels.
CREATE TABLE IF NOT EXISTS auth_bindings (
    id      BIGSERIAL PRIMARY KEY,
    subject TEXT NOT NULL,
    role    TEXT NOT NULL,
    scope   TEXT NOT NULL DEFAULT '*',
    UNIQUE (subject, role, scope)
);
CREATE INDEX IF NOT EXISTS idx_bindings_subject ON auth_bindings(subject);

-- The built-in superuser role. Its permission set is not stored: it holds every
-- permission by definition, so a permission added later is covered without a
-- migration. It cannot be deleted.
INSERT INTO auth_roles (name, description, permissions)
VALUES ('system:admin', 'Full administrative access', '')
ON CONFLICT (name) DO NOTHING;

-- Carry the admin flag over as a real grant.
INSERT INTO auth_bindings (subject, role, scope)
SELECT 'user:' || name, 'system:admin', '*' FROM auth_users WHERE admin
ON CONFLICT (subject, role, scope) DO NOTHING;

-- The `admin` column is deliberately left in place for now. Dropping it here
-- would break the previous release the moment this migration runs, while its
-- pods are still serving during the rollout. It is no longer read: a follow-up
-- migration removes it once no old process can be running.

-- Two ready-made roles. They are definitions, not access: a role grants nothing
-- until a binding points a subject at it, so seeding them on any install is
-- safe and saves retyping the obvious two.

INSERT INTO auth_roles (name, description, permissions) VALUES
  ('reader',      'Pull and browse', ''),
  ('contributor', 'Pull, push and delete', '')
ON CONFLICT (name) DO NOTHING;

INSERT INTO auth_role_permissions (role_name, permission) VALUES
  ('reader',      'registry:read'),
  ('contributor', 'registry:read'),
  ('contributor', 'registry:write'),
  ('contributor', 'registry:delete')
ON CONFLICT DO NOTHING;

-- Reproduce the behaviour that was in force, as explicit grants — but only on a
-- database that already holds registries.
--
-- Until now an empty access list meant "anonymous may read, any authenticated
-- user may write". That implicit default is gone: nothing is permitted unless
-- granted. On an upgrade, migrating silently to a deny-all would stop every
-- anonymous pull in the cluster the moment this ships, so the old behaviour is
-- written out as two ordinary grants — visible in the UI, and narrowable there.
--
-- A fresh install has no registries and therefore no behaviour to preserve.
-- Seeding these there would hand every authenticated account push and delete
-- over everything, which is precisely the implicit default this model exists to
-- get rid of. A new installation starts with no grants at all beyond the
-- administrator's.
INSERT INTO auth_bindings (subject, role, scope)
SELECT * FROM (VALUES
  ('anonymous',     'reader',      '*'),
  ('authenticated', 'contributor', '*')
) AS v(subject, role, scope)
WHERE EXISTS (SELECT 1 FROM registries_meta)
ON CONFLICT (subject, role, scope) DO NOTHING;
