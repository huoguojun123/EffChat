-- Production migration: add admin-managed global chat font assets.
-- Schema-only. Font files live under /app/uploads/fonts, persisted by the
-- existing ${DATA_DIR:-./data}/uploads:/app/uploads Docker volume.

CREATE TABLE IF NOT EXISTS font_assets (
    id           BIGSERIAL PRIMARY KEY,
    display_name VARCHAR(160) NOT NULL,
    family_name  VARCHAR(160) NOT NULL,
    file_name    VARCHAR(255) NOT NULL,
    file_path    TEXT NOT NULL UNIQUE,
    mime_type    VARCHAR(80) NOT NULL,
    file_size    BIGINT NOT NULL CHECK (file_size > 0),
    checksum     VARCHAR(64) NOT NULL,
    weight       INTEGER NOT NULL DEFAULT 400 CHECK (weight BETWEEN 100 AND 900),
    style        VARCHAR(20) NOT NULL DEFAULT 'normal' CHECK (style IN ('normal', 'italic', 'oblique')),
    enabled      BOOLEAN NOT NULL DEFAULT true,
    created_by   BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at   TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_font_assets_enabled ON font_assets(enabled) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_font_assets_created_by ON font_assets(created_by);
CREATE INDEX IF NOT EXISTS idx_font_assets_checksum ON font_assets(checksum);

DROP TRIGGER IF EXISTS update_font_assets_updated_at ON font_assets;
CREATE TRIGGER update_font_assets_updated_at
    BEFORE UPDATE ON font_assets
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

COMMENT ON TABLE font_assets IS '管理员上传的全局对话字体资产，文件存储在 uploads/fonts';
COMMENT ON COLUMN font_assets.file_path IS '服务端本地字体文件路径，Docker 部署时位于 /app/uploads/fonts';
COMMENT ON COLUMN font_assets.checksum IS 'SHA256 哈希，用于审计和识别重复文件';
