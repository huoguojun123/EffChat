-- Model request profiles are administrator-owned runtime contracts. They are
-- intentionally typed instead of storing arbitrary provider JSON so catalog
-- metadata cannot silently inject unsupported request parameters.
ALTER TABLE models
    ADD COLUMN IF NOT EXISTS temperature_policy VARCHAR(16) NOT NULL DEFAULT 'configurable',
    ADD COLUMN IF NOT EXISTS temperature_value DOUBLE PRECISION;

ALTER TABLE models
    DROP CONSTRAINT IF EXISTS models_temperature_policy_check,
    ADD CONSTRAINT models_temperature_policy_check
        CHECK (temperature_policy IN ('configurable', 'omit', 'fixed')),
    DROP CONSTRAINT IF EXISTS models_temperature_profile_check,
    ADD CONSTRAINT models_temperature_profile_check
        CHECK (
            (temperature_policy = 'fixed' AND temperature_value IS NOT NULL AND temperature_value >= 0 AND temperature_value <= 2)
            OR
            (temperature_policy IN ('configurable', 'omit') AND temperature_value IS NULL)
        );

COMMENT ON COLUMN models.temperature_policy IS 'Whether session temperature is configurable, omitted, or fixed for this model';
COMMENT ON COLUMN models.temperature_value IS 'Required fixed temperature value; null for configurable or omitted policies';
