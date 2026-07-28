-- Structured skills migration.
--
-- 旧版 skills.content 是 DB-only 正文，升级后不再兼容，也不会迁移到文件包。
-- 这里把旧记录软删，管理员需要按新版 SKILL.md + references 结构重新导入。

ALTER TABLE skills ADD COLUMN IF NOT EXISTS package_checksum VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE skills ADD COLUMN IF NOT EXISTS entry_path TEXT NOT NULL DEFAULT 'SKILL.md';
ALTER TABLE skills ADD COLUMN IF NOT EXISTS min_group_level INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_skills_min_group_level ON skills(min_group_level);

CREATE TABLE IF NOT EXISTS skill_files (
    id            BIGSERIAL PRIMARY KEY,
    skill_id      VARCHAR(100) NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    relative_path TEXT NOT NULL,
    storage_path  TEXT NOT NULL,
    kind          VARCHAR(20) NOT NULL CHECK (kind IN ('entry', 'reference')),
    size          BIGINT NOT NULL CHECK (size >= 0),
    checksum      VARCHAR(64) NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(skill_id, relative_path)
);
CREATE INDEX IF NOT EXISTS idx_skill_files_skill_id ON skill_files(skill_id);
CREATE INDEX IF NOT EXISTS idx_skill_files_kind ON skill_files(kind);

UPDATE skills
SET deleted_at = NOW(), enabled = false, updated_at = NOW()
WHERE deleted_at IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM skill_files sf
    WHERE sf.skill_id = skills.id AND sf.kind = 'entry'
  );

COMMENT ON TABLE skills IS '结构化 Agent skills 元数据表：正文落盘到 uploads/skills，由工具按需读取';
COMMENT ON COLUMN skills.content IS '旧版 DB-only 正文字段，结构化 skills 不再读取';
COMMENT ON TABLE skill_files IS '结构化 Skill 文件清单：SKILL.md 入口和管理员选中的 references';
