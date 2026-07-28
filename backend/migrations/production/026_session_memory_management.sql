-- Session memory management: lightweight change history plus memory maintenance usage kind.

ALTER TABLE model_usage_events
    DROP CONSTRAINT IF EXISTS model_usage_events_kind_check;

ALTER TABLE model_usage_events
    ADD CONSTRAINT model_usage_events_kind_check
    CHECK (kind IN ('chat', 'retry', 'title', 'compression', 'tool_chain', 'memory_maintenance'));

COMMENT ON COLUMN model_usage_events.kind IS '调用类型：chat/retry/title/compression/tool_chain/memory_maintenance';

CREATE TABLE IF NOT EXISTS session_memory_changes (
    id             BIGSERIAL PRIMARY KEY,
    session_id     BIGINT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    user_id        BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    source         VARCHAR(20) NOT NULL CHECK (source IN ('auto', 'manual', 'tool', 'compact', 'undo')),
    action         VARCHAR(20) NOT NULL CHECK (action IN ('update', 'compact', 'clear', 'undo')),
    before_content TEXT NOT NULL DEFAULT '',
    after_content  TEXT NOT NULL DEFAULT '',
    summary        TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    undone_at      TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_session_memory_changes_session_time
    ON session_memory_changes(session_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_session_memory_changes_user_time
    ON session_memory_changes(user_id, created_at DESC);

COMMENT ON TABLE session_memory_changes IS '会话记忆轻量变更记录，仅保留最近记录用于可见和撤销';
