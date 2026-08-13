ALTER TABLE models
    ADD COLUMN IF NOT EXISTS openai_top_p DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS openai_n INTEGER,
    ADD COLUMN IF NOT EXISTS openai_presence_penalty DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS openai_frequency_penalty DOUBLE PRECISION;

ALTER TABLE models
    DROP CONSTRAINT IF EXISTS models_openai_top_p_check,
    ADD CONSTRAINT models_openai_top_p_check
        CHECK (openai_top_p IS NULL OR (openai_top_p >= 0 AND openai_top_p <= 1)),
    DROP CONSTRAINT IF EXISTS models_openai_n_check,
    ADD CONSTRAINT models_openai_n_check
        CHECK (openai_n IS NULL OR openai_n >= 1),
    DROP CONSTRAINT IF EXISTS models_openai_presence_penalty_check,
    ADD CONSTRAINT models_openai_presence_penalty_check
        CHECK (openai_presence_penalty IS NULL OR (openai_presence_penalty >= -2 AND openai_presence_penalty <= 2)),
    DROP CONSTRAINT IF EXISTS models_openai_frequency_penalty_check,
    ADD CONSTRAINT models_openai_frequency_penalty_check
        CHECK (openai_frequency_penalty IS NULL OR (openai_frequency_penalty >= -2 AND openai_frequency_penalty <= 2));

COMMENT ON COLUMN models.openai_top_p IS 'Optional fixed top_p for OpenAI-compatible requests; null omits the field';
COMMENT ON COLUMN models.openai_n IS 'Optional fixed n for OpenAI-compatible requests; null omits the field';
COMMENT ON COLUMN models.openai_presence_penalty IS 'Optional fixed presence_penalty for OpenAI-compatible requests; null omits the field';
COMMENT ON COLUMN models.openai_frequency_penalty IS 'Optional fixed frequency_penalty for OpenAI-compatible requests; null omits the field';
