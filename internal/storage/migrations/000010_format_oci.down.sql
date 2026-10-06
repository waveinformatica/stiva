UPDATE registries_meta SET format = 'docker' WHERE format = 'oci';
UPDATE auth_bindings SET scope = 'docker' || substring(scope from 4) WHERE scope LIKE 'oci:%';
UPDATE auth_bindings SET scope = 'docker' WHERE scope = 'oci';
