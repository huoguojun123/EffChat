-- users.group_id = NULL dynamically inherits the current default group. A
-- fresh installation therefore needs one before the first user is created;
-- otherwise permission and administrator reads cannot resolve an effective
-- group. Existing installations with a default group are left untouched.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM user_groups WHERE is_default = true) THEN
        INSERT INTO user_groups (name, level, description, is_default)
        VALUES (
            'Default',
            0,
            'Default access group for users without an explicit assignment',
            true
        )
        ON CONFLICT (name) DO UPDATE
        SET is_default = true;
    END IF;
END
$$;
