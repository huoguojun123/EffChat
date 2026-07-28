-- 轻量模型 API 调用统计。
-- 只记录迁移上线后的新调用，不从 messages 回填历史 usage。

CREATE TABLE IF NOT EXISTS model_usage_events (
    id                BIGSERIAL PRIMARY KEY,
    user_id           BIGINT,
    session_id        BIGINT,
    message_id        BIGINT,
    run_id            VARCHAR(120),
    kind              VARCHAR(30) NOT NULL CHECK (kind IN ('chat', 'retry', 'title', 'compression', 'tool_chain')),
    provider          VARCHAR(50) NOT NULL DEFAULT '',
    model_id          VARCHAR(160) NOT NULL DEFAULT '',
    success           BOOLEAN NOT NULL DEFAULT true,
    prompt_tokens     INTEGER NOT NULL DEFAULT 0 CHECK (prompt_tokens >= 0),
    completion_tokens INTEGER NOT NULL DEFAULT 0 CHECK (completion_tokens >= 0),
    total_tokens      INTEGER NOT NULL DEFAULT 0 CHECK (total_tokens >= 0),
    cached_tokens     INTEGER NOT NULL DEFAULT 0 CHECK (cached_tokens >= 0),
    reasoning_tokens  INTEGER NOT NULL DEFAULT 0 CHECK (reasoning_tokens >= 0),
    duration_ms       BIGINT NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
    error_type        VARCHAR(60),
    error_message     TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_model_usage_events_created_at ON model_usage_events(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_model_usage_events_user_time ON model_usage_events(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_model_usage_events_model_time ON model_usage_events(provider, model_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_model_usage_events_kind_time ON model_usage_events(kind, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_model_usage_events_session_time ON model_usage_events(session_id, created_at DESC);

COMMENT ON TABLE model_usage_events IS '模型 API 调用事实表：聊天、重试、标题、压缩、工具链小模型均记录；不回填历史';
COMMENT ON COLUMN model_usage_events.kind IS '调用类型：chat/retry/title/compression/tool_chain';
