-- Track OCR status on existing file records; no separate task table.

ALTER TABLE files
    DROP CONSTRAINT IF EXISTS files_extract_status_check;

ALTER TABLE files
    ADD CONSTRAINT files_extract_status_check
    CHECK (extract_status IN ('pending', 'ready', 'failed', 'ocr_pending', 'ocr_running'));

ALTER TABLE files ADD COLUMN IF NOT EXISTS ocr_provider VARCHAR(40);
ALTER TABLE files ADD COLUMN IF NOT EXISTS ocr_task_id TEXT;
ALTER TABLE files ADD COLUMN IF NOT EXISTS ocr_page_count INTEGER NOT NULL DEFAULT 0 CHECK (ocr_page_count >= 0);
ALTER TABLE files ADD COLUMN IF NOT EXISTS ocr_progress_pages INTEGER NOT NULL DEFAULT 0 CHECK (ocr_progress_pages >= 0);
ALTER TABLE files ADD COLUMN IF NOT EXISTS ocr_started_at TIMESTAMPTZ;
ALTER TABLE files ADD COLUMN IF NOT EXISTS ocr_completed_at TIMESTAMPTZ;
ALTER TABLE files ADD COLUMN IF NOT EXISTS ocr_error_type VARCHAR(80);

CREATE INDEX IF NOT EXISTS idx_files_ocr_task ON files(ocr_provider, ocr_task_id) WHERE ocr_task_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_files_ocr_status ON files(extract_status, created_at DESC) WHERE extract_status IN ('ocr_pending', 'ocr_running');
