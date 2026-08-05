-- ============================================
-- EffChat production/001 的不可变历史基线。
-- 用途：只能由 production/001_schema.sql 引用，再继续执行后续迁移。
--      本文件只包含历史基线 schema，不是当前快照，也不是可独立
--      启动应用的手动 fresh-install 入口。
--
-- 首次公开后不得回改；所有结构修正只追加 production migration。
--
-- 官方 fresh install：使用 Docker Compose migrate 服务或 ./init_db.sh，
-- 由统一 runner 原子执行完整 production 链并写入 schema_migrations。
--
-- 首个注册用户自动成为管理员（由应用层 auth_service 处理，无需在此预置）。
-- ============================================

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pg_trgm";

-- updated_at 自动更新函数（被多个表的触发器复用）
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- ============================================
-- 1. 用户分级组（先于 users，因 users.group_id 外键引用）
-- ============================================
CREATE TABLE IF NOT EXISTS user_groups (
    id          SERIAL PRIMARY KEY,
    name        VARCHAR(50)  NOT NULL UNIQUE,
    level       INTEGER      NOT NULL DEFAULT 0,
    description VARCHAR(200) NOT NULL DEFAULT '',
    is_default  BOOLEAN      NOT NULL DEFAULT false,
    daily_message_limit  INTEGER NOT NULL DEFAULT 0 CHECK (daily_message_limit >= 0),
    daily_token_limit    INTEGER NOT NULL DEFAULT 0 CHECK (daily_token_limit >= 0),
    concurrent_run_limit INTEGER NOT NULL DEFAULT 0 CHECK (concurrent_run_limit >= 0),
    daily_tool_call_limit INTEGER NOT NULL DEFAULT 0 CHECK (daily_tool_call_limit >= 0),
    daily_web_search_limit INTEGER NOT NULL DEFAULT 0 CHECK (daily_web_search_limit >= 0),
    daily_web_extract_limit INTEGER NOT NULL DEFAULT 0 CHECK (daily_web_extract_limit >= 0),
    daily_ocr_file_limit INTEGER NOT NULL DEFAULT 0 CHECK (daily_ocr_file_limit >= 0),
    daily_ocr_page_limit INTEGER NOT NULL DEFAULT 0 CHECK (daily_ocr_page_limit >= 0),
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_user_groups_level ON user_groups(level);
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_groups_single_default
    ON user_groups (is_default)
    WHERE is_default = true;

DROP TRIGGER IF EXISTS update_user_groups_updated_at ON user_groups;
CREATE TRIGGER update_user_groups_updated_at
    BEFORE UPDATE ON user_groups
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

COMMENT ON TABLE  user_groups            IS '用户分级组：level 越大权限越高';
COMMENT ON COLUMN user_groups.is_default IS '新用户默认组（应仅一个为 true）';
COMMENT ON COLUMN user_groups.daily_message_limit IS '每日用户消息数上限，0 表示不限制';
COMMENT ON COLUMN user_groups.daily_token_limit IS '每日模型 token 近似上限，0 表示不限制';
COMMENT ON COLUMN user_groups.concurrent_run_limit IS '并发 chat run 上限，0 表示不限制';
COMMENT ON COLUMN user_groups.daily_tool_call_limit IS '每日工具调用总次数上限，0 表示不限制';
COMMENT ON COLUMN user_groups.daily_web_search_limit IS '每日 web_search 调用次数上限，0 表示不限制';
COMMENT ON COLUMN user_groups.daily_web_extract_limit IS '每日 web_extract 调用次数上限，0 表示不限制';
COMMENT ON COLUMN user_groups.daily_ocr_file_limit IS '每日 OCR 文件数上限，0 表示不限制';
COMMENT ON COLUMN user_groups.daily_ocr_page_limit IS '每日 OCR 页数上限，0 表示不限制';

-- ============================================
-- 2. 用户表（含 group_id）
-- ============================================
CREATE TABLE IF NOT EXISTS users (
    id BIGSERIAL PRIMARY KEY,
    username VARCHAR(50) NOT NULL UNIQUE,
    email VARCHAR(255) UNIQUE,
    password_hash VARCHAR(255) NOT NULL,
    nickname VARCHAR(100),
    avatar_url TEXT,
    role VARCHAR(20) NOT NULL DEFAULT 'user' CHECK (role IN ('admin', 'user')),
    group_id INTEGER REFERENCES user_groups(id) ON DELETE SET NULL,
    permissions JSONB DEFAULT '{}',
    preferences JSONB DEFAULT '{}',
    is_active BOOLEAN NOT NULL DEFAULT true,
    auth_version INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_login_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);
CREATE INDEX IF NOT EXISTS idx_users_email ON users(email) WHERE email IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_users_role ON users(role);
CREATE INDEX IF NOT EXISTS idx_users_is_active ON users(is_active) WHERE is_active = true;
CREATE INDEX IF NOT EXISTS idx_users_created_at ON users(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_users_group_id ON users(group_id);

DROP TRIGGER IF EXISTS update_users_updated_at ON users;
CREATE TRIGGER update_users_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

COMMENT ON TABLE users IS '用户表';
COMMENT ON COLUMN users.role IS '角色：admin（管理员）或 user（普通用户）。首个注册用户由应用层自动设为 admin';
COMMENT ON COLUMN users.group_id IS '所属分级组，NULL 视为默认最低级';

-- ============================================
-- 3. 会话文件夹表
-- ============================================
CREATE TABLE IF NOT EXISTS session_folders (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(80) NOT NULL,
    pinned_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_session_folders_user_name ON session_folders(user_id, lower(name));
CREATE INDEX IF NOT EXISTS idx_session_folders_user_id ON session_folders(user_id);
CREATE INDEX IF NOT EXISTS idx_session_folders_user_pinned ON session_folders(user_id, pinned_at DESC NULLS LAST, name ASC, id ASC);

DROP TRIGGER IF EXISTS update_session_folders_updated_at ON session_folders;
CREATE TRIGGER update_session_folders_updated_at
    BEFORE UPDATE ON session_folders
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

COMMENT ON TABLE session_folders IS '用户手动会话文件夹';

-- ============================================
-- 4. 会话表（含 search_mode / memory_enabled）
-- ============================================
CREATE TABLE IF NOT EXISTS sessions (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    folder_id BIGINT REFERENCES session_folders(id) ON DELETE SET NULL,
    pinned_at TIMESTAMPTZ,
    title VARCHAR(500) NOT NULL DEFAULT '新对话',
    title_generated BOOLEAN NOT NULL DEFAULT false,
    model_id VARCHAR(100) NOT NULL,
    provider VARCHAR(50) NOT NULL,
    system_prompt TEXT,
    temperature DECIMAL(3,2) DEFAULT 0.7 CHECK (temperature >= 0 AND temperature <= 2),
    max_tokens INTEGER CHECK (max_tokens > 0),
    message_format VARCHAR(10) NOT NULL DEFAULT 'v1' CHECK (message_format IN ('v1', 'v2')),
    search_mode VARCHAR(10) NOT NULL DEFAULT 'auto' CHECK (search_mode IN ('off', 'auto', 'on')),
    memory_enabled BOOLEAN NOT NULL DEFAULT true,
    answer_selection_revision BIGINT NOT NULL DEFAULT 0,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_updated_at ON sessions(updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_sessions_deleted_at ON sessions(deleted_at) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_sessions_message_format ON sessions(message_format);
CREATE INDEX IF NOT EXISTS idx_sessions_model_id ON sessions(model_id);
CREATE INDEX IF NOT EXISTS idx_sessions_user_folder_updated ON sessions(user_id, folder_id, updated_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_sessions_user_updated ON sessions(user_id, updated_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_sessions_user_pinned_updated ON sessions(user_id, pinned_at DESC NULLS LAST, updated_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_sessions_title_trgm ON sessions USING GIN (lower(title) gin_trgm_ops) WHERE deleted_at IS NULL;

DROP TRIGGER IF EXISTS update_sessions_updated_at ON sessions;
CREATE TRIGGER update_sessions_updated_at
    BEFORE UPDATE ON sessions
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

COMMENT ON TABLE sessions IS '对话会话表';
COMMENT ON COLUMN sessions.folder_id IS '会话所属文件夹，NULL 表示未分组';
COMMENT ON COLUMN sessions.search_mode IS '会话联网搜索模式：off / auto / on';
COMMENT ON COLUMN sessions.memory_enabled IS '会话记忆开关：true 时挂载 memory 工具并注入会话笔记';

-- ============================================
-- 5. 消息表（核心表，has_tool_calls 已含 009 修复）
-- ============================================
CREATE TABLE IF NOT EXISTS messages (
    id BIGSERIAL PRIMARY KEY,
    session_id BIGINT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    schema_version VARCHAR(10) NOT NULL DEFAULT 'v1' CHECK (schema_version IN ('v1', 'v2')),
    message_data JSONB NOT NULL,
    role VARCHAR(20) GENERATED ALWAYS AS (message_data->>'role') STORED,
    has_tool_calls BOOLEAN GENERATED ALWAYS AS (
        CASE schema_version
            WHEN 'v1' THEN COALESCE(
                jsonb_typeof(message_data->'tool_calls') = 'array'
                AND jsonb_array_length(message_data->'tool_calls') > 0,
                false
            )
            WHEN 'v2' THEN
                message_data::text LIKE '%"type":"function_tool_call"%' OR
                message_data::text LIKE '%"type":"mcp_tool_call"%' OR
                message_data::text LIKE '%"type":"server_tool_call"%'
            ELSE false
        END
    ) STORED,
    has_reasoning BOOLEAN GENERATED ALWAYS AS (
        CASE schema_version
            WHEN 'v1' THEN length(COALESCE(message_data->>'reasoning_content', '')) > 0
            WHEN 'v2' THEN message_data::text LIKE '%"type":"reasoning"%'
            ELSE false
        END
    ) STORED,
    has_multimodal BOOLEAN GENERATED ALWAYS AS (
        CASE schema_version
            WHEN 'v1' THEN
                (message_data->'user_input_multi_content') IS NOT NULL OR
                (message_data->'assistant_gen_multi_content') IS NOT NULL
            WHEN 'v2' THEN
                message_data::text LIKE '%"type":"user_input_image"%' OR
                message_data::text LIKE '%"type":"user_input_audio"%' OR
                message_data::text LIKE '%"type":"user_input_video"%' OR
                message_data::text LIKE '%"type":"user_input_file"%' OR
                message_data::text LIKE '%"type":"assistant_gen_image"%' OR
                message_data::text LIKE '%"type":"assistant_gen_audio"%' OR
                message_data::text LIKE '%"type":"assistant_gen_video"%'
            ELSE false
        END
    ) STORED,
    compressed_at TIMESTAMPTZ,
    compression_summary_id BIGINT REFERENCES messages(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_messages_session_id ON messages(session_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_messages_schema_version ON messages(schema_version);
CREATE INDEX IF NOT EXISTS idx_messages_role ON messages(role);
CREATE INDEX IF NOT EXISTS idx_messages_has_tool_calls ON messages(has_tool_calls) WHERE has_tool_calls = true;
CREATE INDEX IF NOT EXISTS idx_messages_has_reasoning ON messages(has_reasoning) WHERE has_reasoning = true;
CREATE INDEX IF NOT EXISTS idx_messages_has_multimodal ON messages(has_multimodal) WHERE has_multimodal = true;
CREATE INDEX IF NOT EXISTS idx_messages_deleted_at ON messages(deleted_at) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_messages_data_gin ON messages USING GIN(message_data jsonb_path_ops);
CREATE UNIQUE INDEX IF NOT EXISTS idx_messages_run_user_unique
ON messages (
    session_id,
    (message_data->'metadata'->>'run_id')
)
WHERE deleted_at IS NULL
  AND role = 'user'
  AND COALESCE(message_data->'metadata'->>'run_id', '') <> '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_messages_run_produced_sequence_unique
ON messages (
    session_id,
    (message_data->'metadata'->>'run_id'),
    (message_data->'metadata'->>'run_sequence')
)
WHERE deleted_at IS NULL
  AND role IN ('assistant', 'tool')
  AND COALESCE(message_data->'metadata'->>'run_id', '') <> ''
  AND COALESCE(message_data->'metadata'->>'run_sequence', '') <> '';
CREATE INDEX IF NOT EXISTS idx_messages_compressed_at ON messages(compressed_at) WHERE compressed_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_messages_compression_summary_id ON messages(compression_summary_id) WHERE compression_summary_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_messages_content_search ON messages USING GIN(
    to_tsvector('simple',
        COALESCE(message_data->>'content', '') || ' ' ||
        COALESCE(message_data->>'reasoning_content', '')
    )
);
CREATE INDEX IF NOT EXISTS idx_messages_visible_content_trgm
ON messages USING GIN (lower(COALESCE(message_data->>'content', '')) gin_trgm_ops)
WHERE deleted_at IS NULL AND role IN ('user', 'assistant');

CREATE TABLE IF NOT EXISTS answer_attempts (
    id BIGSERIAL PRIMARY KEY,
    session_id BIGINT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    user_message_id BIGINT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    run_id VARCHAR(128) UNIQUE,
    attempt_number INTEGER NOT NULL CHECK (attempt_number > 0),
    status VARCHAR(20) NOT NULL CHECK (status IN ('running', 'completed', 'incomplete', 'failed')),
    selected BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);
ALTER TABLE messages
    ADD COLUMN IF NOT EXISTS answer_attempt_id BIGINT REFERENCES answer_attempts(id) ON DELETE SET NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_answer_attempts_user_number
    ON answer_attempts(user_message_id, attempt_number);
CREATE UNIQUE INDEX IF NOT EXISTS idx_answer_attempts_selected_user
    ON answer_attempts(user_message_id)
    WHERE selected;
CREATE INDEX IF NOT EXISTS idx_answer_attempts_session_user
    ON answer_attempts(session_id, user_message_id, attempt_number);
CREATE INDEX IF NOT EXISTS idx_messages_answer_attempt_id
    ON messages(answer_attempt_id)
    WHERE answer_attempt_id IS NOT NULL;

DROP TRIGGER IF EXISTS update_messages_updated_at ON messages;
CREATE TRIGGER update_messages_updated_at
    BEFORE UPDATE ON messages
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

COMMENT ON TABLE messages IS '消息表 - 支持 Eino Message(v1) 和 AgenticMessage(v2) 双格式';

-- ============================================
-- 6. 模型 API 调用事实表
-- ============================================
CREATE TABLE IF NOT EXISTS model_usage_events (
    id                BIGSERIAL PRIMARY KEY,
    user_id           BIGINT,
    session_id        BIGINT,
    message_id        BIGINT,
    run_id            VARCHAR(120),
    kind              VARCHAR(30) NOT NULL CHECK (kind IN ('chat', 'retry', 'title', 'compression', 'tool_chain', 'memory_maintenance')),
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
COMMENT ON COLUMN model_usage_events.kind IS '调用类型：chat/retry/title/compression/tool_chain/memory_maintenance';

CREATE TABLE IF NOT EXISTS model_task_runs (
    id            BIGSERIAL PRIMARY KEY,
    task_key      VARCHAR(40) NOT NULL CHECK (task_key IN ('title_generation', 'compression', 'tool_extract_summary', 'memory_maintenance')),
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

CREATE TABLE IF NOT EXISTS chat_run_reservations (
    run_id                TEXT PRIMARY KEY,
    user_id               BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    session_id            BIGINT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    kind                  TEXT NOT NULL DEFAULT 'chat' CHECK (kind IN ('chat', 'compaction')),
    operation             TEXT NOT NULL DEFAULT 'send',
    intent_hash           TEXT NOT NULL DEFAULT '',
    intent_version        INTEGER NOT NULL DEFAULT 0,
    retry_target_message_id BIGINT,
    runtime_snapshot       JSONB NOT NULL DEFAULT '{}'::jsonb,
    status                TEXT NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'completed', 'failed', 'canceled')),
    cancel_cause          TEXT NOT NULL DEFAULT '',
    public_error_code     TEXT NOT NULL DEFAULT '',
    public_error_message  TEXT NOT NULL DEFAULT '',
    user_message_id       BIGINT REFERENCES messages(id) ON DELETE SET NULL,
    terminal_message_id   BIGINT REFERENCES messages(id) ON DELETE SET NULL,
    terminal_event        JSONB,
    message_reserved      BOOLEAN NOT NULL DEFAULT false,
    expires_at            TIMESTAMPTZ NOT NULL,
    released_at           TIMESTAMPTZ,
    accepted_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    terminal_at           TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chat_run_reservations_operation_check
        CHECK (operation IN ('send', 'retry', 'compaction')),
    CONSTRAINT chat_run_reservations_kind_operation_check CHECK (
        (kind = 'chat' AND operation IN ('send', 'retry')) OR
        (kind = 'compaction' AND operation = 'compaction')
    ),
    CONSTRAINT chat_run_reservations_intent_check
        CHECK (intent_version >= 0 AND (intent_version = 0 OR intent_hash <> '')),
    CONSTRAINT chat_run_reservations_retry_target_check CHECK (
        (operation = 'retry' AND retry_target_message_id IS NOT NULL) OR
        (operation <> 'retry' AND retry_target_message_id IS NULL)
    ),
    CONSTRAINT chat_run_reservations_runtime_snapshot_size_check
        CHECK (octet_length(runtime_snapshot::text) <= 16384)
);
CREATE INDEX IF NOT EXISTS idx_chat_run_reservations_user_active
    ON chat_run_reservations (user_id, expires_at)
    WHERE released_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_chat_run_reservations_session_status
    ON chat_run_reservations (session_id, status, accepted_at DESC);
CREATE INDEX IF NOT EXISTS idx_chat_run_reservations_user_status
    ON chat_run_reservations (user_id, status, accepted_at DESC);

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

-- ============================================
-- 7. 提示词分组与预设提示词表
-- ============================================
CREATE TABLE IF NOT EXISTS prompt_groups (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_prompt_groups_user_name ON prompt_groups(user_id, lower(name));
CREATE INDEX IF NOT EXISTS idx_prompt_groups_user_id ON prompt_groups(user_id);

DROP TRIGGER IF EXISTS update_prompt_groups_updated_at ON prompt_groups;
CREATE TRIGGER update_prompt_groups_updated_at
    BEFORE UPDATE ON prompt_groups
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
COMMENT ON TABLE prompt_groups IS '用户可管理提示词分组';

CREATE TABLE IF NOT EXISTS prompts (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title VARCHAR(200) NOT NULL,
    content TEXT NOT NULL,
    description TEXT,
    tags TEXT[],
    group_id BIGINT REFERENCES prompt_groups(id) ON DELETE SET NULL,
    group_name VARCHAR(100) NOT NULL DEFAULT '默认分组',
    is_public BOOLEAN NOT NULL DEFAULT false,
    use_count INTEGER NOT NULL DEFAULT 0 CHECK (use_count >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_prompts_user_id ON prompts(user_id);
CREATE INDEX IF NOT EXISTS idx_prompts_tags ON prompts USING GIN(tags);
CREATE INDEX IF NOT EXISTS idx_prompts_is_public ON prompts(is_public) WHERE is_public = true;
CREATE INDEX IF NOT EXISTS idx_prompts_use_count ON prompts(use_count DESC);
CREATE INDEX IF NOT EXISTS idx_prompts_group_name ON prompts(group_name);
CREATE INDEX IF NOT EXISTS idx_prompts_group_id ON prompts(group_id);
CREATE INDEX IF NOT EXISTS idx_prompts_public_group ON prompts(group_name, is_public) WHERE is_public = true;

DROP TRIGGER IF EXISTS update_prompts_updated_at ON prompts;
CREATE TRIGGER update_prompts_updated_at
    BEFORE UPDATE ON prompts
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
COMMENT ON TABLE prompts IS '预设提示词表（提示词市场）';

-- ============================================
-- 6. 系统配置表（KV 存储）
-- ============================================
CREATE TABLE IF NOT EXISTS system_config (
    key VARCHAR(100) PRIMARY KEY,
    value JSONB NOT NULL,
    description TEXT,
    config_type VARCHAR(50) NOT NULL CHECK (config_type IN ('number', 'string', 'boolean', 'json', 'select')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
DROP TRIGGER IF EXISTS update_system_config_updated_at ON system_config;
CREATE TRIGGER update_system_config_updated_at
    BEFORE UPDATE ON system_config
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
COMMENT ON TABLE system_config IS '系统配置 KV 表（Admin 可修改）';

-- ============================================
-- 7. 文件表（含 010 提取字段）
-- ============================================
CREATE TABLE IF NOT EXISTS files (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    session_id BIGINT REFERENCES sessions(id) ON DELETE SET NULL,
    file_name VARCHAR(255) NOT NULL,
    file_path TEXT NOT NULL UNIQUE,
    file_type VARCHAR(100) NOT NULL,
    file_size BIGINT NOT NULL CHECK (file_size > 0),
    file_hash VARCHAR(64),
    status VARCHAR(20) NOT NULL DEFAULT 'staged' CHECK (status IN ('staged', 'formal', 'cleanup_claimed', 'storage_removed')),
    extracted_text_path TEXT,
    extract_status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (extract_status IN ('pending', 'ready', 'failed', 'ocr_pending', 'ocr_running')),
    extract_error TEXT,
    token_estimate INT NOT NULL DEFAULT 0,
    ocr_provider VARCHAR(40),
    ocr_task_id TEXT,
    ocr_page_count INTEGER NOT NULL DEFAULT 0 CHECK (ocr_page_count >= 0),
    ocr_progress_pages INTEGER NOT NULL DEFAULT 0 CHECK (ocr_progress_pages >= 0),
    ocr_started_at TIMESTAMPTZ,
    ocr_completed_at TIMESTAMPTZ,
    ocr_error_type VARCHAR(80),
    ocr_source_path TEXT,
    ocr_lease_until TIMESTAMPTZ,
    ocr_attempts INTEGER NOT NULL DEFAULT 0 CHECK (ocr_attempts >= 0),
    ocr_next_retry_at TIMESTAMPTZ,
    cleanup_after TIMESTAMPTZ,
    cleanup_claim_token TEXT,
    cleanup_lease_until TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_files_user_id ON files(user_id);
CREATE INDEX IF NOT EXISTS idx_files_session_id ON files(session_id);
CREATE INDEX IF NOT EXISTS idx_files_file_hash ON files(file_hash);
CREATE INDEX IF NOT EXISTS idx_files_status ON files(status);
CREATE INDEX IF NOT EXISTS idx_files_created_at ON files(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_files_ocr_recovery ON files(ocr_provider, ocr_next_retry_at, ocr_lease_until)
    WHERE status = 'staged' AND extract_status IN ('ocr_pending', 'ocr_running');
CREATE INDEX IF NOT EXISTS idx_files_cleanup_claim ON files(cleanup_after, cleanup_lease_until, created_at)
    WHERE status = 'cleanup_claimed';
COMMENT ON TABLE files IS '上传文件元数据表';
COMMENT ON COLUMN files.extracted_text_path IS '提取正文 sidecar 文件路径，位于 uploads 数据目录，供 file_read 按需读取';
COMMENT ON COLUMN files.ocr_source_path IS 'OCR 完成前暂存的受管 PDF 原件；sidecar 与文件状态提交成功后删除';

-- ============================================
-- 8. 会话记忆表（每会话一条持久记忆）
-- ============================================
CREATE TABLE IF NOT EXISTS session_memories (
    session_id BIGINT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    content    TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
COMMENT ON TABLE session_memories IS '每会话一条持久记忆，由 memory 工具读写，不进压缩，每轮注入系统提示';

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

-- ============================================
-- 9. 文字型 Skills 库
-- ============================================
CREATE TABLE IF NOT EXISTS skills (
    id          VARCHAR(100) PRIMARY KEY,
    name        VARCHAR(160) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    -- 旧版 DB-only skills 曾使用 content 保存完整正文；新版正文只落到
    -- uploads/skills 下，保留此列仅降低升级风险，不再作为运行时来源。
    content     TEXT NOT NULL,
    source_type VARCHAR(20) NOT NULL DEFAULT 'manual'
        CHECK (source_type IN ('builtin', 'manual', 'git', 'zip')),
    source_url  TEXT,
    source_ref  VARCHAR(160),
    source_path TEXT,
    checksum    VARCHAR(64) NOT NULL,
    package_checksum VARCHAR(64) NOT NULL DEFAULT '',
    entry_path  TEXT NOT NULL DEFAULT 'SKILL.md',
    min_group_level INTEGER NOT NULL DEFAULT 0,
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

CREATE TABLE IF NOT EXISTS skill_import_record_files (
    import_record_id BIGINT NOT NULL REFERENCES skill_import_records(id) ON DELETE CASCADE,
    relative_path   TEXT NOT NULL,
    storage_path    TEXT NOT NULL,
    kind            VARCHAR(20) NOT NULL CHECK (kind IN ('entry', 'reference')),
    size            BIGINT NOT NULL CHECK (size >= 0),
    checksum        VARCHAR(64) NOT NULL,
    PRIMARY KEY (import_record_id, relative_path)
);
CREATE INDEX IF NOT EXISTS idx_skill_import_record_files_storage_path ON skill_import_record_files(storage_path);

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
CREATE INDEX IF NOT EXISTS idx_governance_events_resource ON governance_events(resource_type, resource_key, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_governance_events_actor ON governance_events(actor_user_id, created_at DESC, id DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_governance_events_rollback_of ON governance_events(rollback_of_event_id) WHERE rollback_of_event_id IS NOT NULL;

DROP TRIGGER IF EXISTS update_skills_updated_at ON skills;
CREATE TRIGGER update_skills_updated_at
    BEFORE UPDATE ON skills
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
COMMENT ON TABLE skills IS '结构化 Agent skills 元数据表：正文落盘到 uploads/skills，由工具按需读取';
COMMENT ON TABLE skill_files IS '结构化 Skill 文件清单：SKILL.md 入口和管理员选中的 references';
COMMENT ON TABLE skill_import_records IS '结构化 Skills 的导入和更新记录，用于审计、重复导入确认和后续回滚基础';
COMMENT ON TABLE skill_import_record_files IS 'Skill import record 对应的不可变受管文件版本引用；活动包切换不删除审计仍引用的版本';
COMMENT ON TABLE governance_events IS 'Tool/Skill 管理变更的追加式审计事件；回滚新增反向事件，不改写历史';
COMMENT ON COLUMN governance_events.before_state IS '变更前的非正文状态；用于显示和 current-state CAS';
COMMENT ON COLUMN governance_events.after_state IS '变更后的非正文状态；用于显示和 current-state CAS';
COMMENT ON COLUMN governance_events.skill_import_record_id IS 'Skill 包事件关联的不可变 import/package version；正文仍在受管存储';

-- ============================================
-- 10. 字体资产表（管理员全局对话字体）
-- ============================================
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

-- ============================================
-- 11. 模型能力表（含 007 min_group_level）
-- ============================================
CREATE TABLE IF NOT EXISTS models (
    id            VARCHAR(100) PRIMARY KEY,
    display_name  VARCHAR(100) NOT NULL,
    provider      VARCHAR(50)  NOT NULL,
    vision         BOOLEAN NOT NULL DEFAULT false,
    tool_use       BOOLEAN NOT NULL DEFAULT false,
    reasoning      BOOLEAN NOT NULL DEFAULT false,
    thinking_format VARCHAR(64) NOT NULL DEFAULT 'auto',
    search_impl    VARCHAR(20) NOT NULL DEFAULT '' CHECK (search_impl IN ('', 'internal', 'params', 'tool')),
    context_window INTEGER NOT NULL DEFAULT 0 CHECK (context_window >= 0),
    max_output     INTEGER NOT NULL DEFAULT 0 CHECK (max_output >= 0),
    enabled        BOOLEAN NOT NULL DEFAULT true,
    min_group_level INTEGER NOT NULL DEFAULT 0 CHECK (min_group_level >= 0),
    sort_order     INTEGER NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_models_provider ON models(provider);
CREATE INDEX IF NOT EXISTS idx_models_enabled ON models(enabled);
CREATE INDEX IF NOT EXISTS idx_models_min_group_level ON models(min_group_level);

DROP TRIGGER IF EXISTS update_models_updated_at ON models;
CREATE TRIGGER update_models_updated_at
    BEFORE UPDATE ON models
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
COMMENT ON TABLE models IS '模型能力表（Admin 可增删改）';
COMMENT ON COLUMN models.thinking_format IS '思考参数格式：auto=按 model_id 自动匹配，none=不下发，其它值=管理员指定官方参数格式';

-- ============================================
-- 12. 运行时渠道与外部服务配置
-- ============================================
CREATE TABLE IF NOT EXISTS ai_channels (
    id           BIGSERIAL PRIMARY KEY,
    channel_key  VARCHAR(80) NOT NULL UNIQUE,
    display_name VARCHAR(160) NOT NULL,
    adapter      VARCHAR(40) NOT NULL CHECK (adapter IN ('openai_compatible', 'anthropic', 'google')),
    base_url     TEXT NOT NULL DEFAULT '',
    api_key      TEXT,
    enabled      BOOLEAN NOT NULL DEFAULT true,
    sort_order   INTEGER NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at   TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_ai_channels_enabled ON ai_channels(enabled) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_ai_channels_adapter ON ai_channels(adapter) WHERE deleted_at IS NULL;

DROP TRIGGER IF EXISTS update_ai_channels_updated_at ON ai_channels;
CREATE TRIGGER update_ai_channels_updated_at
    BEFORE UPDATE ON ai_channels
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TABLE IF NOT EXISTS external_services (
    id           BIGSERIAL PRIMARY KEY,
    service_key  VARCHAR(80) NOT NULL UNIQUE,
    display_name VARCHAR(160) NOT NULL,
    kind         VARCHAR(40) NOT NULL CHECK (kind IN ('search', 'crawler', 'ocr')),
    base_url     TEXT NOT NULL DEFAULT '',
    api_key      TEXT,
    enabled      BOOLEAN NOT NULL DEFAULT true,
    sort_order   INTEGER NOT NULL DEFAULT 0,
    max_concurrency INTEGER NOT NULL DEFAULT 0 CHECK (max_concurrency >= 0),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at   TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_external_services_enabled ON external_services(enabled) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_external_services_kind ON external_services(kind) WHERE deleted_at IS NULL;

DROP TRIGGER IF EXISTS update_external_services_updated_at ON external_services;
CREATE TRIGGER update_external_services_updated_at
    BEFORE UPDATE ON external_services
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

COMMENT ON TABLE ai_channels IS '管理员网页配置的模型渠道；API key 不回显，运行时新请求实时读取';
COMMENT ON TABLE external_services IS '管理员网页配置的搜索、网页提取和 OCR 服务；替代模型/搜索相关环境变量';

-- ============================================
-- 13. Agent 工具治理配置
-- ============================================
CREATE TABLE IF NOT EXISTS tool_configs (
    id              BIGSERIAL PRIMARY KEY,
    tool_key        VARCHAR(80) NOT NULL UNIQUE,
    display_name    VARCHAR(160) NOT NULL,
    enabled         BOOLEAN NOT NULL DEFAULT true,
    timeout_seconds INTEGER NOT NULL DEFAULT 20 CHECK (timeout_seconds > 0 AND timeout_seconds <= 120),
    sort_order      INTEGER NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_tool_configs_enabled ON tool_configs(enabled);

DROP TRIGGER IF EXISTS update_tool_configs_updated_at ON tool_configs;
CREATE TRIGGER update_tool_configs_updated_at
    BEFORE UPDATE ON tool_configs
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

COMMENT ON TABLE tool_configs IS '管理员网页配置的现有 Agent 工具治理项：启停和单次调用超时';
COMMENT ON COLUMN tool_configs.timeout_seconds IS '单次工具调用超时，超过后返回结构化工具错误，0 不允许';

-- ============================================
-- 14. 视图
-- ============================================
CREATE OR REPLACE VIEW v_active_sessions AS
SELECT s.*, u.username, u.nickname,
       COUNT(m.id) AS message_count, MAX(m.created_at) AS last_message_at
FROM sessions s
INNER JOIN users u ON s.user_id = u.id
LEFT JOIN messages m ON s.id = m.session_id AND m.deleted_at IS NULL
WHERE s.deleted_at IS NULL
GROUP BY s.id, u.username, u.nickname;

CREATE OR REPLACE VIEW v_user_stats AS
SELECT u.id, u.username, u.role,
       COUNT(DISTINCT s.id) AS session_count,
       COUNT(m.id) AS message_count,
       SUM(CASE WHEN m.role = 'user' THEN 1 ELSE 0 END) AS user_message_count,
       SUM(CASE WHEN m.role = 'assistant' THEN 1 ELSE 0 END) AS assistant_message_count,
       MAX(s.updated_at) AS last_active_at
FROM users u
LEFT JOIN sessions s ON u.id = s.user_id AND s.deleted_at IS NULL
LEFT JOIN messages m ON s.id = m.session_id AND m.deleted_at IS NULL
GROUP BY u.id;

-- ============================================
\echo '✅ init.sql 执行完成：表/索引/触发器/视图已就绪'
\echo '   首个注册用户将自动成为管理员。'
