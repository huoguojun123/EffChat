-- A user turn can retain several independently persisted assistant/tool outputs.
ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS answer_selection_revision BIGINT NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS answer_attempts (
    id BIGSERIAL PRIMARY KEY,
    session_id BIGINT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    user_message_id BIGINT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    run_id VARCHAR(128) UNIQUE,
    attempt_number INTEGER NOT NULL CHECK (attempt_number > 0),
    status VARCHAR(20) NOT NULL CHECK (status IN ('running', 'completed', 'incomplete', 'failed')),
    selected BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);

ALTER TABLE messages
    ADD COLUMN IF NOT EXISTS answer_attempt_id BIGINT REFERENCES answer_attempts(id) ON DELETE SET NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_answer_attempts_user_number
    ON answer_attempts(user_message_id, attempt_number);
CREATE UNIQUE INDEX IF NOT EXISTS idx_answer_attempts_selected_user
    ON answer_attempts(user_message_id)
    WHERE selected;
CREATE INDEX IF NOT EXISTS idx_answer_attempts_session_user
    ON answer_attempts(session_id, user_message_id, attempt_number);
CREATE INDEX IF NOT EXISTS idx_messages_answer_attempt_id
    ON messages(answer_attempt_id)
    WHERE answer_attempt_id IS NOT NULL;

WITH ordered AS (
    SELECT
        m.id,
        m.session_id,
        m.role,
        m.message_data,
        MAX(CASE WHEN m.role = 'user' THEN m.id END) OVER (
            PARTITION BY m.session_id
            ORDER BY m.id
            ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW
        ) AS user_message_id
    FROM messages m
    WHERE m.deleted_at IS NULL
), legacy_attempts AS (
    SELECT
        session_id,
        user_message_id,
        CASE
            WHEN BOOL_OR(COALESCE(message_data->'metadata'->>'ephemeral_error', '') = 'true') THEN 'failed'
            WHEN BOOL_OR(COALESCE(message_data->'metadata'->>'incomplete', '') = 'true') THEN 'incomplete'
            ELSE 'completed'
        END AS status
    FROM ordered
    WHERE role IN ('assistant', 'tool')
      AND user_message_id IS NOT NULL
    GROUP BY session_id, user_message_id
)
INSERT INTO answer_attempts (session_id, user_message_id, attempt_number, status, selected, completed_at)
SELECT session_id, user_message_id, 1, status, true, NOW()
FROM legacy_attempts
ON CONFLICT (user_message_id) WHERE selected DO NOTHING;

WITH ordered AS (
    SELECT
        m.id,
        m.session_id,
        m.role,
        MAX(CASE WHEN m.role = 'user' THEN m.id END) OVER (
            PARTITION BY m.session_id
            ORDER BY m.id
            ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW
        ) AS user_message_id
    FROM messages m
    WHERE m.deleted_at IS NULL
)
UPDATE messages m
SET answer_attempt_id = a.id
FROM ordered o
JOIN answer_attempts a
  ON a.session_id = o.session_id
 AND a.user_message_id = o.user_message_id
 AND a.attempt_number = 1
WHERE m.id = o.id
  AND o.role IN ('assistant', 'tool')
  AND m.answer_attempt_id IS NULL;
