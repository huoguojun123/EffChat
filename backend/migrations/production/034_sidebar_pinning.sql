ALTER TABLE sessions ADD COLUMN IF NOT EXISTS pinned_at TIMESTAMPTZ;
ALTER TABLE session_folders ADD COLUMN IF NOT EXISTS pinned_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_sessions_user_pinned_updated
    ON sessions (user_id, pinned_at DESC NULLS LAST, updated_at DESC, id DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_session_folders_user_pinned
    ON session_folders (user_id, pinned_at DESC NULLS LAST, name ASC, id ASC);
