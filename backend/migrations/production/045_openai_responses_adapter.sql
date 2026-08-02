-- Add the explicit OpenAI Responses wire protocol without changing existing
-- OpenAI-compatible channels. PostgreSQL generated this stable constraint name
-- from the ai_channels.adapter column in migration 016.
ALTER TABLE ai_channels
    DROP CONSTRAINT IF EXISTS ai_channels_adapter_check;

ALTER TABLE ai_channels
    ADD CONSTRAINT ai_channels_adapter_check
    CHECK (adapter IN ('openai_compatible', 'openai_responses', 'anthropic', 'google'));
