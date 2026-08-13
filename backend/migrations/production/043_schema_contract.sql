-- Normalize the immutable baseline, legacy upgrades and current production
-- databases to one catalog shape. This migration changes schema metadata only;
-- it does not rewrite user, message, file or managed-storage data.

CREATE INDEX IF NOT EXISTS idx_files_ocr_task
    ON files(ocr_provider, ocr_task_id)
    WHERE ocr_task_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_files_ocr_status
    ON files(extract_status, created_at DESC)
    WHERE extract_status IN ('ocr_pending', 'ocr_running');

ALTER TABLE model_usage_events
    DROP CONSTRAINT IF EXISTS model_usage_events_kind_check;

ALTER TABLE model_usage_events
    ADD CONSTRAINT model_usage_events_kind_check
    CHECK (kind IN (
        'chat', 'retry', 'title', 'compression', 'tool_chain',
        'memory_maintenance', 'timeline_compaction'
    ));

ALTER TABLE model_task_runs
    DROP CONSTRAINT IF EXISTS model_task_runs_task_key_check;

ALTER TABLE model_task_runs
    ADD CONSTRAINT model_task_runs_task_key_check
    CHECK (task_key IN (
        'title_generation', 'compression', 'tool_extract_summary',
        'memory_maintenance', 'timeline_compaction'
    ));

-- init.sql historically created anonymous foreign keys while migration 036
-- checked only its preferred names. Drop every single-column FK owned by these
-- two columns, then recreate one canonical constraint for each relationship.
DO $$
DECLARE
    constraint_name TEXT;
BEGIN
    FOR constraint_name IN
        SELECT c.conname
        FROM pg_constraint c
        JOIN LATERAL unnest(c.conkey) AS key(attnum) ON true
        JOIN pg_attribute a
          ON a.attrelid = c.conrelid AND a.attnum = key.attnum
        WHERE c.conrelid = 'chat_run_reservations'::regclass
          AND c.contype = 'f'
          AND array_length(c.conkey, 1) = 1
          AND a.attname IN ('user_message_id', 'terminal_message_id')
    LOOP
        EXECUTE format(
            'ALTER TABLE chat_run_reservations DROP CONSTRAINT %I',
            constraint_name
        );
    END LOOP;
END $$;

ALTER TABLE chat_run_reservations
    ADD CONSTRAINT chat_run_reservations_user_message_fk
        FOREIGN KEY (user_message_id) REFERENCES messages(id) ON DELETE SET NULL,
    ADD CONSTRAINT chat_run_reservations_terminal_message_fk
        FOREIGN KEY (terminal_message_id) REFERENCES messages(id) ON DELETE SET NULL;

-- Views store the expanded column list from creation time. Columns added to
-- sessions after the 001 baseline therefore never appeared on upgraded
-- databases, even though a fresh baseline created the view after those
-- columns. No application object depends on this compatibility view, so
-- recreate it once with the canonical current projection.
DROP VIEW IF EXISTS v_active_sessions;
CREATE VIEW v_active_sessions AS
SELECT s.id, s.user_id, s.folder_id, s.pinned_at, s.title,
       s.title_generated, s.model_id, s.provider, s.system_prompt,
       s.temperature, s.max_tokens, s.message_format, s.search_mode,
       s.memory_enabled, s.answer_selection_revision, s.metadata,
       s.created_at, s.updated_at, s.deleted_at,
       u.username, u.nickname,
       COUNT(m.id) AS message_count, MAX(m.created_at) AS last_message_at
FROM sessions s
INNER JOIN users u ON s.user_id = u.id
LEFT JOIN messages m ON s.id = m.session_id AND m.deleted_at IS NULL
WHERE s.deleted_at IS NULL
GROUP BY s.id, u.username, u.nickname;

-- These comments are part of the public schema contract and were present in
-- the baseline snapshot but absent or stale on some upgraded installations.
COMMENT ON TABLE ai_channels IS '管理员网页配置的模型渠道；API key 不回显，运行时新请求实时读取';
COMMENT ON TABLE external_services IS '管理员网页配置的搜索、网页提取和 OCR 服务；替代模型/搜索相关环境变量';
COMMENT ON TABLE tool_configs IS '管理员网页配置的现有 Agent 工具治理项：启停和单次调用超时';
COMMENT ON COLUMN tool_configs.timeout_seconds IS '单次工具调用超时，超过后返回结构化工具错误，0 不允许';
COMMENT ON COLUMN tool_usage_events.context_tokens IS '工具结果进入模型上下文前的近似 token 数，仅用于观测，不作为限额';
COMMENT ON COLUMN tool_usage_events.truncated IS '工具自身或未来压缩策略是否截断了结果';
