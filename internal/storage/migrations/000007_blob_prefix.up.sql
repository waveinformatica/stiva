-- Per-registry isolation inside a shared blob store.
--
-- Blob deletion checks the reference count of the deleting registry only, then
-- removes the object from the store. Two registries sharing an unprefixed store
-- would therefore delete each other's layers: the second drops its last
-- manifest referencing a layer, removes the object, and silently breaks the
-- first. Giving each registry its own key prefix makes that impossible.
--
-- The prefix belongs to the registry↔store link, not to the store: the same
-- store serves several registries, each under its own prefix.
--
-- Existing rows default to the empty prefix on purpose. Registries created
-- before this change have their blobs at "sha256/<hex>"; deriving a prefix for
-- them retroactively would make every existing object unreachable.

ALTER TABLE registries_meta ADD COLUMN IF NOT EXISTS blob_prefix TEXT NOT NULL DEFAULT '';
