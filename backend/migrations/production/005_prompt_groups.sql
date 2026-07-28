-- Add managed prompt groups while keeping prompts.group_name for compatibility.
-- Existing prompt group names are preserved by creating per-user groups.

CREATE TABLE IF NOT EXISTS prompt_groups (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_prompt_groups_user_name
    ON prompt_groups(user_id, lower(name));
CREATE INDEX IF NOT EXISTS idx_prompt_groups_user_id
    ON prompt_groups(user_id);

DROP TRIGGER IF EXISTS update_prompt_groups_updated_at ON prompt_groups;
CREATE TRIGGER update_prompt_groups_updated_at
    BEFORE UPDATE ON prompt_groups
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

ALTER TABLE prompts ADD COLUMN IF NOT EXISTS group_id BIGINT;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'prompts_group_id_fkey'
          AND conrelid = 'prompts'::regclass
    ) THEN
        ALTER TABLE prompts
            ADD CONSTRAINT prompts_group_id_fkey
            FOREIGN KEY (group_id) REFERENCES prompt_groups(id) ON DELETE SET NULL;
    END IF;
END $$;

INSERT INTO prompt_groups (user_id, name)
SELECT DISTINCT user_id, COALESCE(NULLIF(BTRIM(group_name), ''), '默认分组')
FROM prompts
WHERE group_id IS NULL
ON CONFLICT DO NOTHING;

UPDATE prompts p
SET group_id = g.id,
    group_name = g.name
FROM prompt_groups g
WHERE p.group_id IS NULL
  AND g.user_id = p.user_id
  AND lower(g.name) = lower(COALESCE(NULLIF(BTRIM(p.group_name), ''), '默认分组'));

CREATE INDEX IF NOT EXISTS idx_prompts_group_id ON prompts(group_id);

COMMENT ON TABLE prompt_groups IS '用户可管理提示词分组';
COMMENT ON COLUMN prompts.group_id IS '提示词所属分组，NULL 表示未分组';
