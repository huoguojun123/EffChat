-- 新建会话默认开启会话记忆。
--
-- 只调整列默认值，不批量改写已有会话：已有会话的开关代表用户当时的显式状态；
-- 后端服务层也会在请求未携带 memory_enabled 时写入 true，保证所有创建入口一致。
ALTER TABLE sessions
    ALTER COLUMN memory_enabled SET DEFAULT TRUE;
