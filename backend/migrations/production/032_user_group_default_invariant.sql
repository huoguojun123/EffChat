BEGIN;

WITH ranked_defaults AS (
    SELECT id, ROW_NUMBER() OVER (ORDER BY level ASC, id ASC) AS ordinal
    FROM user_groups
    WHERE is_default = true
)
UPDATE user_groups AS groups
SET is_default = false
FROM ranked_defaults
WHERE groups.id = ranked_defaults.id
  AND ranked_defaults.ordinal > 1;

CREATE UNIQUE INDEX IF NOT EXISTS idx_user_groups_single_default
    ON user_groups (is_default)
    WHERE is_default = true;

COMMIT;
