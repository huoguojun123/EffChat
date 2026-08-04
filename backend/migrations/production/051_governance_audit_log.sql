-- Append-only governance history for Tool and Skill catalog mutations.
--
-- Skill package bodies remain in managed storage. The version table records
-- immutable file references owned by an import record so a future rollback
-- can restore an exact package without copying its contents into PostgreSQL or
-- depending on the original Git/Zip source still being available.

CREATE TABLE IF NOT EXISTS skill_import_record_files (
    import_record_id BIGINT NOT NULL REFERENCES skill_import_records(id) ON DELETE CASCADE,
    relative_path   TEXT NOT NULL,
    storage_path    TEXT NOT NULL,
    kind            VARCHAR(20) NOT NULL CHECK (kind IN ('entry', 'reference')),
    size            BIGINT NOT NULL CHECK (size >= 0),
    checksum        VARCHAR(64) NOT NULL,
    PRIMARY KEY (import_record_id, relative_path)
);

CREATE INDEX IF NOT EXISTS idx_skill_import_record_files_storage_path
    ON skill_import_record_files(storage_path);

CREATE TABLE IF NOT EXISTS governance_events (
    id                       BIGSERIAL PRIMARY KEY,
    resource_type            VARCHAR(20) NOT NULL CHECK (resource_type IN ('tool', 'skill')),
    resource_key             VARCHAR(100) NOT NULL,
    action                   VARCHAR(30) NOT NULL CHECK (action IN ('create', 'update', 'delete', 'import', 'rollback')),
    actor_type               VARCHAR(20) NOT NULL CHECK (actor_type IN ('admin', 'import', 'system')),
    actor_user_id            BIGINT REFERENCES users(id) ON DELETE SET NULL,
    reason                   TEXT NOT NULL CHECK (length(btrim(reason)) > 0),
    before_state             JSONB,
    after_state              JSONB,
    skill_import_record_id   BIGINT REFERENCES skill_import_records(id) ON DELETE SET NULL,
    rollback_of_event_id     BIGINT REFERENCES governance_events(id) ON DELETE SET NULL,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (before_state IS NOT NULL OR after_state IS NOT NULL),
    CHECK (actor_type = 'system' OR actor_user_id IS NOT NULL),
    CHECK (resource_type = 'skill' OR skill_import_record_id IS NULL),
    CHECK (action = 'rollback' OR rollback_of_event_id IS NULL),
    CHECK (action <> 'rollback' OR rollback_of_event_id IS NOT NULL)
);

CREATE INDEX IF NOT EXISTS idx_governance_events_resource
    ON governance_events(resource_type, resource_key, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_governance_events_actor
    ON governance_events(actor_user_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_governance_events_rollback_of
    ON governance_events(rollback_of_event_id) WHERE rollback_of_event_id IS NOT NULL;

COMMENT ON TABLE governance_events IS 'Tool/Skill 管理变更的追加式审计事件；回滚新增反向事件，不改写历史';
COMMENT ON COLUMN governance_events.before_state IS '变更前的非正文状态；用于显示和 current-state CAS';
COMMENT ON COLUMN governance_events.after_state IS '变更后的非正文状态；用于显示和 current-state CAS';
COMMENT ON COLUMN governance_events.skill_import_record_id IS 'Skill 包事件关联的不可变 import/package version；正文仍在受管存储';
COMMENT ON TABLE skill_import_record_files IS 'Skill import record 对应的不可变受管文件版本引用；活动包切换不删除审计仍引用的版本';
