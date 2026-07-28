-- Tool usage quotas for existing Docker deployments.

ALTER TABLE user_groups
    ADD COLUMN IF NOT EXISTS daily_tool_call_limit INTEGER NOT NULL DEFAULT 0 CHECK (daily_tool_call_limit >= 0),
    ADD COLUMN IF NOT EXISTS daily_web_search_limit INTEGER NOT NULL DEFAULT 0 CHECK (daily_web_search_limit >= 0),
    ADD COLUMN IF NOT EXISTS daily_web_extract_limit INTEGER NOT NULL DEFAULT 0 CHECK (daily_web_extract_limit >= 0);

COMMENT ON COLUMN user_groups.daily_tool_call_limit IS '每日工具调用总次数上限，0 表示不限制';
COMMENT ON COLUMN user_groups.daily_web_search_limit IS '每日 web_search 调用次数上限，0 表示不限制';
COMMENT ON COLUMN user_groups.daily_web_extract_limit IS '每日 web_extract 调用次数上限，0 表示不限制';

CREATE TABLE IF NOT EXISTS tool_usage_events (
    id             BIGSERIAL PRIMARY KEY,
    user_id        BIGINT,
    session_id     BIGINT,
    run_id         VARCHAR(120),
    call_id        VARCHAR(120),
    tool_key       VARCHAR(80) NOT NULL,
    success        BOOLEAN NOT NULL DEFAULT true,
    context_tokens INTEGER NOT NULL DEFAULT 0 CHECK (context_tokens >= 0),
    truncated      BOOLEAN NOT NULL DEFAULT false,
    duration_ms    BIGINT NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
    error_type     VARCHAR(60),
    error_message  TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_tool_usage_events_created_at ON tool_usage_events(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_tool_usage_events_user_time ON tool_usage_events(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_tool_usage_events_tool_time ON tool_usage_events(tool_key, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_tool_usage_events_session_time ON tool_usage_events(session_id, created_at DESC);

COMMENT ON TABLE tool_usage_events IS '工具调用用量事实表：用于管理员用量展示和轻量防滥用限额，不保存工具参数或完整结果';
COMMENT ON COLUMN tool_usage_events.context_tokens IS '工具结果进入模型上下文前的近似 token 数，仅用于观测，不作为限额';
COMMENT ON COLUMN tool_usage_events.truncated IS '工具自身或未来压缩策略是否截断了结果';
