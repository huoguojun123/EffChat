-- Store extracted attachment text outside PostgreSQL.
-- Text lives under uploads/extracted/<user_id>; PostgreSQL keeps only metadata.

ALTER TABLE files ADD COLUMN IF NOT EXISTS extracted_text_path TEXT;
ALTER TABLE files DROP COLUMN IF EXISTS extracted_text;

COMMENT ON COLUMN files.extracted_text_path IS '提取正文 sidecar 文件路径，位于 uploads 数据目录，供 file_read 按需读取';
