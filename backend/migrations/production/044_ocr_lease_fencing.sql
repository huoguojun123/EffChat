-- Give every OCR recovery claim a monotonic identity. A worker must present
-- the generation it claimed for every later mutation, so an expired worker
-- cannot overwrite the state owned by a newer claim.
ALTER TABLE files
    ADD COLUMN IF NOT EXISTS ocr_lease_generation BIGINT NOT NULL DEFAULT 0;

COMMENT ON COLUMN files.ocr_lease_generation IS
    'Monotonic OCR recovery claim generation used to fence expired workers';
