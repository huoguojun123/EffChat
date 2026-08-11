-- Prompt application only copies content into the active session and never
-- submits a prompt identity to the server. The old counter therefore could
-- not change and must not remain as a misleading ranking or API contract.

DROP INDEX IF EXISTS idx_prompts_use_count;
ALTER TABLE prompts DROP COLUMN IF EXISTS use_count;
