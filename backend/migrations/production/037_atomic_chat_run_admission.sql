ALTER TABLE chat_run_reservations
    ADD COLUMN IF NOT EXISTS operation TEXT NOT NULL DEFAULT 'send',
    ADD COLUMN IF NOT EXISTS intent_version INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS intent_hash TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS retry_target_message_id BIGINT;

UPDATE chat_run_reservations
SET operation = CASE WHEN kind = 'compaction' THEN 'compaction' ELSE 'send' END
WHERE intent_version = 0;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'chat_run_reservations_operation_check'
          AND conrelid = 'chat_run_reservations'::regclass
    ) THEN
        ALTER TABLE chat_run_reservations
            ADD CONSTRAINT chat_run_reservations_operation_check
            CHECK (operation IN ('send', 'retry', 'compaction'));
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'chat_run_reservations_kind_operation_check'
          AND conrelid = 'chat_run_reservations'::regclass
    ) THEN
        ALTER TABLE chat_run_reservations
            ADD CONSTRAINT chat_run_reservations_kind_operation_check
            CHECK (
                (kind = 'chat' AND operation IN ('send', 'retry')) OR
                (kind = 'compaction' AND operation = 'compaction')
            );
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'chat_run_reservations_intent_check'
          AND conrelid = 'chat_run_reservations'::regclass
    ) THEN
        ALTER TABLE chat_run_reservations
            ADD CONSTRAINT chat_run_reservations_intent_check
            CHECK (intent_version >= 0 AND (intent_version = 0 OR intent_hash <> ''));
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'chat_run_reservations_retry_target_check'
          AND conrelid = 'chat_run_reservations'::regclass
    ) THEN
        ALTER TABLE chat_run_reservations
            ADD CONSTRAINT chat_run_reservations_retry_target_check
            CHECK (
                (operation = 'retry' AND retry_target_message_id IS NOT NULL) OR
                (operation <> 'retry' AND retry_target_message_id IS NULL)
            );
    END IF;
END $$;
