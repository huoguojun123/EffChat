-- Keep persisted Tool governance aligned with the runtime/Admin catalog.

INSERT INTO tool_configs (tool_key, display_name, enabled, timeout_seconds, sort_order)
VALUES
    ('memory', 'Session memory', true, 20, 10),
    ('file_list', 'File list', true, 20, 20),
    ('file_search', 'File search', true, 20, 30),
    ('file_read', 'File read', true, 20, 40),
    ('skill_list', 'Skill list', true, 20, 50),
    ('skill_search', 'Skill search', true, 20, 60),
    ('skill_read', 'Skill read', true, 20, 70),
    ('web_search', 'Web search', true, 20, 80),
    ('web_extract', 'Web extract', true, 30, 90)
ON CONFLICT (tool_key) DO NOTHING;

DELETE FROM tool_configs
WHERE tool_key NOT IN (
    'memory',
    'file_list',
    'file_search',
    'file_read',
    'skill_list',
    'skill_search',
    'skill_read',
    'web_search',
    'web_extract'
);

ALTER TABLE tool_configs
    DROP CONSTRAINT IF EXISTS tool_configs_tool_key_check;

ALTER TABLE tool_configs
    ADD CONSTRAINT tool_configs_tool_key_check
    CHECK (tool_key IN (
        'memory',
        'file_list',
        'file_search',
        'file_read',
        'skill_list',
        'skill_search',
        'skill_read',
        'web_search',
        'web_extract'
    ));

COMMENT ON TABLE tool_configs IS '管理员可见的 Agent Tool 治理目录；运行时仅允许目录内 key，未知 key fail closed';
COMMENT ON COLUMN tool_configs.tool_key IS '必须与运行时和 Admin 共享的 Tool catalog 一致；新增 Tool 需同步 schema migration';
