DELETE FROM auth_role_permissions WHERE permission = 'registry:scan';
DELETE FROM auth_role_permissions WHERE role_name = 'scanner';
DELETE FROM auth_roles WHERE name = 'scanner';
DROP TABLE IF EXISTS image_packages;
DROP TABLE IF EXISTS package_vulnerabilities;
DROP TABLE IF EXISTS scan_packages;
DROP TABLE IF EXISTS image_scans;
