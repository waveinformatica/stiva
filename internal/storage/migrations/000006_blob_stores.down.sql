-- Lossy: registries keep whatever inline blob configuration they still carry,
-- but the named stores and the references to them are dropped.
DROP INDEX IF EXISTS idx_registries_blob_store;
ALTER TABLE registries_meta DROP COLUMN IF EXISTS blob_store;
DROP TABLE IF EXISTS blob_stores;
