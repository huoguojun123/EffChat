-- Add OCR anti-abuse quotas to user groups.

ALTER TABLE user_groups
    ADD COLUMN IF NOT EXISTS daily_ocr_file_limit INTEGER NOT NULL DEFAULT 0 CHECK (daily_ocr_file_limit >= 0);

ALTER TABLE user_groups
    ADD COLUMN IF NOT EXISTS daily_ocr_page_limit INTEGER NOT NULL DEFAULT 0 CHECK (daily_ocr_page_limit >= 0);

COMMENT ON COLUMN user_groups.daily_ocr_file_limit IS '每日 OCR 文件数上限，0 表示不限制';
COMMENT ON COLUMN user_groups.daily_ocr_page_limit IS '每日 OCR 页数上限，0 表示不限制';
