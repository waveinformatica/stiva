-- The OCI Distribution format was stored as "docker". That is the old spelling
-- of the standard, and it read badly in a grant scope: a registry named
-- "docker" of format "docker" produced "docker:docker:kosmos/**".
--
-- The application also normalises "docker" to "oci" when reading a definition,
-- so a row that escapes this migration still resolves correctly. This rewrites
-- the stored values so what is on disk matches what is shown.

UPDATE registries_meta
   SET config = jsonb_set(config, '{format}', '"oci"')
 WHERE config->>'format' = 'docker';

UPDATE registries_meta SET format = 'oci' WHERE format = 'docker';

-- Grant scopes carry the format in their first position.
UPDATE auth_bindings SET scope = 'oci' || substring(scope from 7) WHERE scope LIKE 'docker:%';
UPDATE auth_bindings SET scope = 'oci' WHERE scope = 'docker';
