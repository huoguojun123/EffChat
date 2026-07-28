-- Lightweight user-group quotas for the open-source alpha.
-- 0 means unlimited for each limit.

ALTER TABLE user_groups
    ADD COLUMN IF NOT EXISTS daily_message_limit INTEGER NOT NULL DEFAULT 0 CHECK (daily_message_limit >= 0),
    ADD COLUMN IF NOT EXISTS daily_token_limit INTEGER NOT NULL DEFAULT 0 CHECK (daily_token_limit >= 0),
    ADD COLUMN IF NOT EXISTS concurrent_run_limit INTEGER NOT NULL DEFAULT 0 CHECK (concurrent_run_limit >= 0),
    ADD COLUMN IF NOT EXISTS daily_tool_call_limit INTEGER NOT NULL DEFAULT 0 CHECK (daily_tool_call_limit >= 0),
    ADD COLUMN IF NOT EXISTS daily_web_search_limit INTEGER NOT NULL DEFAULT 0 CHECK (daily_web_search_limit >= 0),
    ADD COLUMN IF NOT EXISTS daily_web_extract_limit INTEGER NOT NULL DEFAULT 0 CHECK (daily_web_extract_limit >= 0);

COMMENT ON COLUMN user_groups.daily_message_limit IS '每日用户消息数上限，0 表示不限制';
COMMENT ON COLUMN user_groups.daily_token_limit IS '每日模型 token 近似上限，0 表示不限制';
COMMENT ON COLUMN user_groups.concurrent_run_limit IS '并发 chat run 上限，0 表示不限制';
COMMENT ON COLUMN user_groups.daily_tool_call_limit IS '每日工具调用总次数上限，0 表示不限制';
COMMENT ON COLUMN user_groups.daily_web_search_limit IS '每日 web_search 调用次数上限，0 表示不限制';
COMMENT ON COLUMN user_groups.daily_web_extract_limit IS '每日 web_extract 调用次数上限，0 表示不限制';
