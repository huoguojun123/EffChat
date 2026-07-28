-- Runtime tool governance for the open-source alpha.

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
    ('web_extract', 'Web extract', true, 20, 90)
ON CONFLICT (tool_key) DO NOTHING;
