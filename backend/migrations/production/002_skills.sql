-- Production migration: add text skills registry.
-- Idempotent for fresh databases where 001_schema.sql already created the table.

CREATE TABLE IF NOT EXISTS skills (
    id          VARCHAR(100) PRIMARY KEY,
    name        VARCHAR(160) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    content     TEXT NOT NULL,
    source_type VARCHAR(20) NOT NULL DEFAULT 'manual'
        CHECK (source_type IN ('builtin', 'manual', 'git', 'zip')),
    source_url  TEXT,
    source_ref  VARCHAR(160),
    source_path TEXT,
    checksum    VARCHAR(64) NOT NULL,
    enabled     BOOLEAN NOT NULL DEFAULT true,
    is_builtin  BOOLEAN NOT NULL DEFAULT false,
    created_by  BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_skills_enabled ON skills(enabled) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_skills_source_type ON skills(source_type);
CREATE INDEX IF NOT EXISTS idx_skills_created_by ON skills(created_by);

DROP TRIGGER IF EXISTS update_skills_updated_at ON skills;
CREATE TRIGGER update_skills_updated_at
    BEFORE UPDATE ON skills
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

COMMENT ON TABLE skills IS '文字型 Agent skills 库：仅作为系统提示注入，不执行代码';
COMMENT ON COLUMN skills.content IS 'Skill Markdown/text 指令正文';
COMMENT ON COLUMN skills.source_type IS '来源：builtin/manual/git/zip';
