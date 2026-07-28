-- Session timeline cold history and utility model business task runs.

ALTER TABLE model_usage_events
    DROP CONSTRAINT IF EXISTS model_usage_events_kind_check;

ALTER TABLE model_usage_events
    ADD CONSTRAINT model_usage_events_kind_check
    CHECK (kind IN ('chat', 'retry', 'title', 'compression', 'tool_chain', 'memory_maintenance', 'timeline_compaction'));

COMMENT ON COLUMN model_usage_events.kind IS '调用类型：chat/retry/title/compression/tool_chain/memory_maintenance/timeline_compaction';

CREATE TABLE IF NOT EXISTS model_task_runs (
    id            BIGSERIAL PRIMARY KEY,
    task_key      VARCHAR(40) NOT NULL CHECK (task_key IN ('title_generation', 'compression', 'tool_extract_summary', 'memory_maintenance', 'timeline_compaction')),
    user_id       BIGINT REFERENCES users(id) ON DELETE SET NULL,
    session_id    BIGINT REFERENCES sessions(id) ON DELETE CASCADE,
    run_id        TEXT NOT NULL DEFAULT '',
    source        VARCHAR(20) NOT NULL CHECK (source IN ('auto', 'manual', 'tool', 'system')),
    status        VARCHAR(20) NOT NULL CHECK (status IN ('success', 'failed', 'skipped')),
    provider      TEXT NOT NULL DEFAULT '',
    model_id      TEXT NOT NULL DEFAULT '',
    target_type   TEXT NOT NULL DEFAULT '',
    target_id     TEXT NOT NULL DEFAULT '',
    error_type    TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    retry_after   TIMESTAMPTZ,
    metadata      JSONB NOT NULL DEFAULT '{}'::jsonb,
    started_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    duration_ms   BIGINT NOT NULL DEFAULT 0 CHECK (duration_ms >= 0)
);

CREATE INDEX IF NOT EXISTS idx_model_task_runs_task_session_time
    ON model_task_runs(task_key, session_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_model_task_runs_status_retry
    ON model_task_runs(status, retry_after);
CREATE INDEX IF NOT EXISTS idx_model_task_runs_user_time
    ON model_task_runs(user_id, started_at DESC);

COMMENT ON TABLE model_task_runs IS 'utility 小模型业务任务运行结果：记录生成、解析、校验、提交层面的成功/失败/跳过';

CREATE TABLE IF NOT EXISTS session_timeline_events (
    id           BIGSERIAL PRIMARY KEY,
    session_id   BIGINT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    user_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_date   DATE NOT NULL DEFAULT CURRENT_DATE,
    kind         VARCHAR(20) NOT NULL CHECK (kind IN ('progress', 'blocker', 'decision', 'correction', 'milestone', 'risk')),
    title        TEXT NOT NULL DEFAULT '',
    content      TEXT NOT NULL,
    next_step    TEXT NOT NULL DEFAULT '',
    source       VARCHAR(20) NOT NULL CHECK (source IN ('auto', 'manual', 'tool', 'compact')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    compacted_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_session_timeline_events_session_date
    ON session_timeline_events(session_id, event_date DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_session_timeline_events_uncompacted
    ON session_timeline_events(session_id, compacted_at, event_date DESC);
CREATE INDEX IF NOT EXISTS idx_session_timeline_events_user_date
    ON session_timeline_events(user_id, event_date DESC);

DROP TRIGGER IF EXISTS update_session_timeline_events_updated_at ON session_timeline_events;
CREATE TRIGGER update_session_timeline_events_updated_at
    BEFORE UPDATE ON session_timeline_events
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

COMMENT ON TABLE session_timeline_events IS '当前会话内的状态轨迹冷历史：阶段推进、决策、阻塞、风险和里程碑';

INSERT INTO tool_configs (tool_key, display_name, enabled, timeout_seconds, sort_order)
VALUES ('memory_timeline', 'Memory timeline', true, 20, 15)
ON CONFLICT (tool_key) DO NOTHING;
