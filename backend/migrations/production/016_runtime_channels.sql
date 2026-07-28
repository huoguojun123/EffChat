-- Runtime-administered AI channels and external web services.

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

CREATE TABLE IF NOT EXISTS external_services (
    id           BIGSERIAL PRIMARY KEY,
    service_key  VARCHAR(80) NOT NULL UNIQUE,
    display_name VARCHAR(160) NOT NULL,
    kind         VARCHAR(40) NOT NULL CHECK (kind IN ('search', 'crawler')),
    base_url     TEXT NOT NULL DEFAULT '',
    api_key      TEXT,
    enabled      BOOLEAN NOT NULL DEFAULT true,
    sort_order   INTEGER NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at   TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_external_services_enabled ON external_services(enabled) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_external_services_kind ON external_services(kind) WHERE deleted_at IS NULL;

DROP TRIGGER IF EXISTS update_ai_channels_updated_at ON ai_channels;
CREATE TRIGGER update_ai_channels_updated_at
    BEFORE UPDATE ON ai_channels
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS update_external_services_updated_at ON external_services;
CREATE TRIGGER update_external_services_updated_at
    BEFORE UPDATE ON external_services
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
