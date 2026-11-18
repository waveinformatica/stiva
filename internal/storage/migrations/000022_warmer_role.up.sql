-- The pre-warm controller's role: ask a cache registry to pull images. It
-- replaces the administrative power the warm endpoint used to require.
INSERT INTO auth_roles (name, description, permissions) VALUES
  ('warmer', 'Pre-pull images into a cache registry', '')
ON CONFLICT (name) DO NOTHING;
INSERT INTO auth_role_permissions (role_name, permission) VALUES
  ('warmer', 'registry:warm')
ON CONFLICT DO NOTHING;
