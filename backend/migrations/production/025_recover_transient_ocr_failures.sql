-- Recover OCR rows that were incorrectly marked failed when MinerU polling was
-- temporarily blocked by another long upload. Real MinerU failures keep their
-- failed status; only transient upstream polling failures with a task id are
-- returned to a pollable state.

UPDATE files
SET extract_status = 'ocr_running',
    extract_error = NULL,
    ocr_error_type = NULL,
    ocr_completed_at = NULL
WHERE status = 'active'
  AND ocr_provider = 'mineru'
  AND extract_status = 'failed'
  AND ocr_error_type = 'ocr_upstream_failed'
  AND NULLIF(TRIM(ocr_task_id), '') IS NOT NULL;

UPDATE files
SET extract_error = NULL,
    ocr_error_type = NULL
WHERE status = 'active'
  AND extract_status = 'ready'
  AND (extract_error IS NOT NULL OR ocr_error_type IS NOT NULL);
