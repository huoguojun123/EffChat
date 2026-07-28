BEGIN;

CREATE EXTENSION IF NOT EXISTS "pg_trgm";

CREATE INDEX IF NOT EXISTS idx_sessions_title_trgm
ON sessions USING GIN (lower(title) gin_trgm_ops)
WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_messages_visible_content_trgm
ON messages USING GIN (lower(COALESCE(message_data->>'content', '')) gin_trgm_ops)
WHERE deleted_at IS NULL AND role IN ('user', 'assistant');

COMMIT;
