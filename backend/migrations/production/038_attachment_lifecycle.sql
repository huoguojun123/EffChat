-- Attachment ownership is explicit so message admission and physical cleanup cannot both own a file.
ALTER TABLE files ADD COLUMN IF NOT EXISTS cleanup_after TIMESTAMPTZ;
ALTER TABLE files ADD COLUMN IF NOT EXISTS cleanup_claim_token TEXT;
ALTER TABLE files ADD COLUMN IF NOT EXISTS cleanup_lease_until TIMESTAMPTZ;

ALTER TABLE files DROP CONSTRAINT IF EXISTS files_status_check;

UPDATE files f
SET status = CASE
    WHEN f.status = 'active' AND EXISTS (
        SELECT 1
        FROM messages m
        WHERE m.session_id = f.session_id
          AND m.deleted_at IS NULL
	  AND m.role = 'user'
          AND jsonb_typeof(m.message_data->'attachments') = 'array'
          AND EXISTS (
              SELECT 1
              FROM jsonb_array_elements(m.message_data->'attachments') AS attachment(item)
              WHERE attachment.item->>'file_id' ~ '^[0-9]+$'
                AND (attachment.item->>'file_id')::bigint = f.id
          )
    ) THEN 'formal'
    WHEN f.status = 'active' THEN 'staged'
    WHEN f.status IN ('deleted', 'orphan') THEN 'storage_removed'
    ELSE f.status
END
WHERE f.status IN ('active', 'deleted', 'orphan');

ALTER TABLE files ALTER COLUMN status SET DEFAULT 'staged';

ALTER TABLE files
    ADD CONSTRAINT files_status_check
    CHECK (status IN ('staged', 'formal', 'cleanup_claimed', 'storage_removed'));

DROP INDEX IF EXISTS idx_files_ocr_recovery;
CREATE INDEX IF NOT EXISTS idx_files_ocr_recovery ON files(ocr_provider, ocr_next_retry_at, ocr_lease_until)
    WHERE status = 'staged' AND extract_status IN ('ocr_pending', 'ocr_running');
CREATE INDEX IF NOT EXISTS idx_files_cleanup_claim ON files(cleanup_after, cleanup_lease_until, created_at)
    WHERE status = 'cleanup_claimed';
