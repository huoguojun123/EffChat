CREATE TABLE IF NOT EXISTS chat_run_reservations (
    run_id           TEXT PRIMARY KEY,
    user_id          BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    session_id       BIGINT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    message_reserved BOOLEAN NOT NULL DEFAULT false,
    expires_at       TIMESTAMPTZ NOT NULL,
    released_at      TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_chat_run_reservations_user_active
    ON chat_run_reservations (user_id, expires_at)
    WHERE released_at IS NULL;
