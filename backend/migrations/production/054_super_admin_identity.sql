-- Persist the first-registration owner so later administrator edits cannot
-- accidentally remove the only recovery identity. Existing installations are
-- backfilled by creation order; no user, message, file, or session content is
-- rewritten beyond the role/state invariant of that account.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS is_super_admin BOOLEAN NOT NULL DEFAULT false;

DO $$
DECLARE
    first_user_id BIGINT;
BEGIN
    SELECT id INTO first_user_id
    FROM users
    ORDER BY id ASC
    LIMIT 1;

    IF first_user_id IS NOT NULL THEN
        UPDATE users
        SET is_super_admin = true,
            role = 'admin',
            is_active = true,
            updated_at = NOW()
        WHERE id = first_user_id;
    END IF;
END
$$;

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_single_super_admin
    ON users (is_super_admin)
    WHERE is_super_admin = true;

COMMENT ON COLUMN users.is_super_admin IS
    '首个注册账号的不可降级身份；应用层和管理员接口禁止改为普通用户或停用';
