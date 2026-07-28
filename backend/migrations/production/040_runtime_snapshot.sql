ALTER TABLE chat_run_reservations
    ADD COLUMN IF NOT EXISTS runtime_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE chat_run_reservations
    DROP CONSTRAINT IF EXISTS chat_run_reservations_runtime_snapshot_size_check;

ALTER TABLE chat_run_reservations
    ADD CONSTRAINT chat_run_reservations_runtime_snapshot_size_check
    CHECK (octet_length(runtime_snapshot::text) <= 16384);
