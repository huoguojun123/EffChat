-- Remove session_timeline_events table (timeline feature replaced by dated bullets in current_progress)
DROP TABLE IF EXISTS session_timeline_events;

DELETE FROM tool_configs WHERE tool_key = 'memory_timeline';
