-- Add one-level manual folders for chat sessions.
-- Schema-only migration: no seed data, no destructive changes.

CREATE TABLE IF NOT EXISTS session_folders (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(80) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_session_folders_user_name
    ON session_folders(user_id, lower(name));
CREATE INDEX IF NOT EXISTS idx_session_folders_user_id
    ON session_folders(user_id);

DROP TRIGGER IF EXISTS update_session_folders_updated_at ON session_folders;
CREATE TRIGGER update_session_folders_updated_at
    BEFORE UPDATE ON session_folders
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

ALTER TABLE sessions ADD COLUMN IF NOT EXISTS folder_id BIGINT;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'sessions_folder_id_fkey'
          AND conrelid = 'sessions'::regclass
    ) THEN
        ALTER TABLE sessions
            ADD CONSTRAINT sessions_folder_id_fkey
            FOREIGN KEY (folder_id) REFERENCES session_folders(id) ON DELETE SET NULL;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_sessions_user_folder_updated
    ON sessions(user_id, folder_id, updated_at DESC, id DESC)
    WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_sessions_user_updated
    ON sessions(user_id, updated_at DESC, id DESC)
    WHERE deleted_at IS NULL;

COMMENT ON TABLE session_folders IS '用户手动会话文件夹';
COMMENT ON COLUMN sessions.folder_id IS '会话所属文件夹，NULL 表示未分组';
