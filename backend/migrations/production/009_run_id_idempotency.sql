-- Run id 幂等约束。
--
-- 生产库可能已经存在旧版重复 active run 消息。这里保留每组最早 id，
-- 软删重复副本，再建立部分唯一索引，保证后续并发重复请求由数据库兜住。

WITH duplicated_user_messages AS (
    SELECT id
    FROM (
        SELECT
            id,
            ROW_NUMBER() OVER (
                PARTITION BY session_id, message_data->'metadata'->>'run_id'
                ORDER BY id ASC
            ) AS rn
        FROM messages
        WHERE deleted_at IS NULL
          AND role = 'user'
          AND COALESCE(message_data->'metadata'->>'run_id', '') <> ''
    ) ranked
    WHERE rn > 1
)
UPDATE messages
SET deleted_at = NOW(), updated_at = NOW()
WHERE id IN (SELECT id FROM duplicated_user_messages);

WITH duplicated_produced_messages AS (
    SELECT id
    FROM (
        SELECT
            id,
            ROW_NUMBER() OVER (
                PARTITION BY
                    session_id,
                    message_data->'metadata'->>'run_id',
                    message_data->'metadata'->>'run_sequence'
                ORDER BY id ASC
            ) AS rn
        FROM messages
        WHERE deleted_at IS NULL
          AND role IN ('assistant', 'tool')
          AND COALESCE(message_data->'metadata'->>'run_id', '') <> ''
          AND COALESCE(message_data->'metadata'->>'run_sequence', '') <> ''
    ) ranked
    WHERE rn > 1
)
UPDATE messages
SET deleted_at = NOW(), updated_at = NOW()
WHERE id IN (SELECT id FROM duplicated_produced_messages);

CREATE UNIQUE INDEX IF NOT EXISTS idx_messages_run_user_unique
ON messages (
    session_id,
    (message_data->'metadata'->>'run_id')
)
WHERE deleted_at IS NULL
  AND role = 'user'
  AND COALESCE(message_data->'metadata'->>'run_id', '') <> '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_messages_run_produced_sequence_unique
ON messages (
    session_id,
    (message_data->'metadata'->>'run_id'),
    (message_data->'metadata'->>'run_sequence')
)
WHERE deleted_at IS NULL
  AND role IN ('assistant', 'tool')
  AND COALESCE(message_data->'metadata'->>'run_id', '') <> ''
  AND COALESCE(message_data->'metadata'->>'run_sequence', '') <> '';
