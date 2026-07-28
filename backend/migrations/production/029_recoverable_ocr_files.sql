-- OCR recovery state stays on files so a restart can resume the same attachment.
ALTER TABLE files ADD COLUMN IF NOT EXISTS ocr_source_path TEXT;
ALTER TABLE files ADD COLUMN IF NOT EXISTS ocr_lease_until TIMESTAMPTZ;
ALTER TABLE files ADD COLUMN IF NOT EXISTS ocr_attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE files ADD COLUMN IF NOT EXISTS ocr_next_retry_at TIMESTAMPTZ;

ALTER TABLE files DROP CONSTRAINT IF EXISTS files_ocr_attempts_check;
ALTER TABLE files ADD CONSTRAINT files_ocr_attempts_check CHECK (ocr_attempts >= 0);

CREATE INDEX IF NOT EXISTS idx_files_ocr_recovery ON files(ocr_provider, ocr_next_retry_at, ocr_lease_until)
    WHERE status = 'active' AND extract_status IN ('ocr_pending', 'ocr_running');

COMMENT ON COLUMN files.ocr_source_path IS 'OCR completion source PDF retained until sidecar and file state commit successfully';
