DROP TABLE IF EXISTS auth_bindings;
DROP TABLE IF EXISTS auth_role_permissions;
DROP TABLE IF EXISTS auth_group_members;
DROP TABLE IF EXISTS auth_groups;
DELETE FROM auth_roles WHERE name = 'system:admin';
