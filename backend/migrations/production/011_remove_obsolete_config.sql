-- Production migration: remove obsolete admin/runtime configuration entries.
--
-- Runtime compression now uses only compression_context_threshold. File upload
-- limits now keep only max batch count, max session file count, and max file
-- size. These deletes are intentionally narrow and do not touch user data.

DELETE FROM system_config
WHERE key IN (
    'compression_threshold',
    'compression_message_threshold',
    'file_upload_max_session_total_mb'
);
