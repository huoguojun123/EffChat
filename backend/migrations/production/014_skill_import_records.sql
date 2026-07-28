-- Skills 导入/更新记录。生产迁移只新增结构，不写初始化数据。

CREATE TABLE IF NOT EXISTS skill_import_records (
    id               BIGSERIAL PRIMARY KEY,
    skill_id         VARCHAR(100) NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    action           VARCHAR(20) NOT NULL CHECK (action IN ('create', 'update')),
    source_type      VARCHAR(20) NOT NULL CHECK (source_type IN ('manual', 'git', 'zip')),
    source_url       TEXT,
    source_ref       VARCHAR(160),
    source_path      TEXT NOT NULL DEFAULT '',
    upstream_skill_id VARCHAR(100) NOT NULL DEFAULT '',
    upstream_name    VARCHAR(160) NOT NULL DEFAULT '',
    package_checksum VARCHAR(64) NOT NULL DEFAULT '',
    selected_files   JSONB NOT NULL DEFAULT '[]'::jsonb,
    file_manifest    JSONB NOT NULL DEFAULT '[]'::jsonb,
    import_report    JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_by       BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_skill_import_records_skill_id ON skill_import_records(skill_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_skill_import_records_source ON skill_import_records(source_type, source_url);

COMMENT ON TABLE skill_import_records IS '结构化 Skills 的导入和更新记录，用于审计、重复导入确认和后续回滚基础';
