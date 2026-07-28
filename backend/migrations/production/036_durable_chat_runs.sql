ALTER TABLE chat_run_reservations
    ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'chat',
    ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'running',
    ADD COLUMN IF NOT EXISTS cancel_cause TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS public_error_code TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS public_error_message TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS user_message_id BIGINT,
    ADD COLUMN IF NOT EXISTS terminal_message_id BIGINT,
    ADD COLUMN IF NOT EXISTS terminal_event JSONB,
    ADD COLUMN IF NOT EXISTS accepted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ADD COLUMN IF NOT EXISTS terminal_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

UPDATE chat_run_reservations
SET status = 'canceled',
    cancel_cause = 'legacy_reconciled',
    message_reserved = false,
    released_at = COALESCE(released_at, NOW()),
    accepted_at = created_at,
    terminal_at = COALESCE(released_at, NOW()),
    updated_at = NOW()
WHERE terminal_at IS NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'chat_run_reservations_kind_check'
    ) THEN
        ALTER TABLE chat_run_reservations
            ADD CONSTRAINT chat_run_reservations_kind_check
            CHECK (kind IN ('chat', 'compaction'));
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'chat_run_reservations_status_check'
    ) THEN
        ALTER TABLE chat_run_reservations
            ADD CONSTRAINT chat_run_reservations_status_check
            CHECK (status IN ('running', 'completed', 'failed', 'canceled'));
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'chat_run_reservations_user_message_fk'
    ) THEN
        ALTER TABLE chat_run_reservations
            ADD CONSTRAINT chat_run_reservations_user_message_fk
            FOREIGN KEY (user_message_id) REFERENCES messages(id) ON DELETE SET NULL;
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'chat_run_reservations_terminal_message_fk'
    ) THEN
        ALTER TABLE chat_run_reservations
            ADD CONSTRAINT chat_run_reservations_terminal_message_fk
            FOREIGN KEY (terminal_message_id) REFERENCES messages(id) ON DELETE SET NULL;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_chat_run_reservations_session_status
    ON chat_run_reservations (session_id, status, accepted_at DESC);

CREATE INDEX IF NOT EXISTS idx_chat_run_reservations_user_status
    ON chat_run_reservations (user_id, status, accepted_at DESC);
