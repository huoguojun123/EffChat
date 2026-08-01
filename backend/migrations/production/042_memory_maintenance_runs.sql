-- Make user-triggered memory maintenance a durable RunHub operation.
-- The run transition, memory mutation, change history and utility-task audit are
-- committed together so a process crash cannot expose changed memory behind a
-- failed/reconciled run.

ALTER TABLE chat_run_reservations
    DROP CONSTRAINT IF EXISTS chat_run_reservations_kind_operation_check,
    DROP CONSTRAINT IF EXISTS chat_run_reservations_operation_check,
    DROP CONSTRAINT IF EXISTS chat_run_reservations_kind_check;

ALTER TABLE chat_run_reservations
    ADD CONSTRAINT chat_run_reservations_kind_check
        CHECK (kind IN ('chat', 'compaction', 'memory_maintenance')),
    ADD CONSTRAINT chat_run_reservations_operation_check
        CHECK (operation IN ('send', 'retry', 'compaction', 'memory_compact', 'memory_retry')),
    ADD CONSTRAINT chat_run_reservations_kind_operation_check
        CHECK (
            (kind = 'chat' AND operation IN ('send', 'retry')) OR
            (kind = 'compaction' AND operation = 'compaction') OR
            (kind = 'memory_maintenance' AND operation IN ('memory_compact', 'memory_retry'))
        );

ALTER TABLE session_memory_changes
    ADD COLUMN IF NOT EXISTS run_id TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_session_memory_changes_run_id
    ON session_memory_changes(run_id)
    WHERE run_id IS NOT NULL;

COMMENT ON COLUMN session_memory_changes.run_id IS
    'RunHub run id for an atomic user-triggered memory maintenance mutation';
