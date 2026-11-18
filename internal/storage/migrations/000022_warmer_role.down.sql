DELETE FROM auth_role_permissions WHERE permission = 'registry:warm';
DELETE FROM auth_role_permissions WHERE role_name = 'warmer';
DELETE FROM auth_roles WHERE name = 'warmer';
