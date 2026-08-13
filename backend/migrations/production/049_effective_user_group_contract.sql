COMMENT ON COLUMN user_groups.is_default IS
    '未显式分组用户动态继承的默认组（应仅一个为 true）';

COMMENT ON COLUMN users.group_id IS
    '显式所属分级组，NULL 动态继承当前默认组';
