-- Tool result token counts are observability data only.
-- User-group anti-abuse limits stay count-based for predictable behavior.

ALTER TABLE user_groups
    DROP COLUMN IF EXISTS daily_tool_context_token_limit;
