-- Drop the legacy DB text body column after moving attachment text to data/uploads/extracted.
--
-- 旧版曾把文本附件正文放在 files.extracted_text，升级后正文只保存在
-- uploads/extracted/<user_id>/<stored_name>.txt。生产迁移按文件名记录执行状态，
-- 因此不能只修改已经发布过的 007；新增 008 才能让已有部署补跑一次 DROP。

ALTER TABLE files ADD COLUMN IF NOT EXISTS extracted_text_path TEXT;
ALTER TABLE files DROP COLUMN IF EXISTS extracted_text;

COMMENT ON COLUMN files.extracted_text_path IS '提取正文文件路径，位于 uploads/extracted 数据目录，供 file_read 按需读取';
