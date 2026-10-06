-- Blob storage becomes a named entity instead of configuration copied into
-- every registry definition. A registry references a store by name, so one
-- object store is described once and shared.
--
-- Stores are created from the UI; nothing is seeded here. An installation with
-- no store defined simply has no registry that can serve blobs yet.

CREATE TABLE IF NOT EXISTS blob_stores (
    name        TEXT PRIMARY KEY,
    kind        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    config      JSONB NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Nullable: group registries aggregate their members and own no storage.
ALTER TABLE registries_meta ADD COLUMN IF NOT EXISTS blob_store TEXT;

CREATE INDEX IF NOT EXISTS idx_registries_blob_store ON registries_meta(blob_store);
