-- Persist the provenance and lifecycle of the capability values currently
-- stored on each administrator-owned model record. Directory presence is not
-- treated as proof of runtime availability, so existing rows remain manual and
-- unknown until an administrator or a verified catalog workflow updates them.
ALTER TABLE models
    ADD COLUMN IF NOT EXISTS catalog_source VARCHAR(32) NOT NULL DEFAULT 'manual',
    ADD COLUMN IF NOT EXISTS catalog_checked_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS lifecycle_status VARCHAR(16) NOT NULL DEFAULT 'unknown';

ALTER TABLE models
    DROP CONSTRAINT IF EXISTS models_catalog_source_check,
    ADD CONSTRAINT models_catalog_source_check
        CHECK (catalog_source IN ('manual', 'channel', 'models_dev', 'builtin', 'unknown')),
    DROP CONSTRAINT IF EXISTS models_lifecycle_status_check,
    ADD CONSTRAINT models_lifecycle_status_check
        CHECK (lifecycle_status IN ('active', 'preview', 'deprecated', 'retired', 'unknown'));

COMMENT ON COLUMN models.catalog_source IS 'Source of the persisted capability metadata; administrator edits remain authoritative';
COMMENT ON COLUMN models.catalog_checked_at IS 'When the persisted capability metadata was last checked against its source';
COMMENT ON COLUMN models.lifecycle_status IS 'Administrator-visible lifecycle evidence; unknown is used when availability is not proven';
