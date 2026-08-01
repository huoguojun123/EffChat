# 数据库迁移说明

迁移体系只有一个可执行入口：

- `production/*.sql`：全新安装和已有数据库升级都完整执行的唯一生产链，并用 `schema_migrations.version/checksum` 记录文件名和内容指纹。
- `init.sql`：只能由 `production/001_schema.sql` 引用的历史 schema 基线，不是当前快照，也不能单独作为 fresh-install 命令。
- `build_migration_script.sh`：Compose 与 `init_db.sh` 共用的 runner 生成器，统一 checksum、锁和事务所有权。
- `init_db.sh`：本地初始化/升级入口，复用同一个 runner。

不再保留顶层 `001_*.sql` 到 `025_*.sql` 的重复迁移，也不提供测试数据脚本。

## 文件职责

| 文件 | 说明 |
|------|------|
| `init.sql` | 不可独立执行的 001 历史 schema 基线；首次公开后不得回改 |
| `production/001_schema.sql` | 使用 `\ir ../init.sql` 引用历史基线的首条生产迁移 |
| `production/002_*.sql` 及后续 | 生产增量迁移，只允许追加新文件，不回改已发布迁移 |
| `build_migration_script.sh` | 生成单会话、逐 migration 原子执行的 psql 脚本 |
| `legacy-checksums.txt` | 只列出可一次性 reconcile 的精确历史 checksum；未知值仍失败 |
| `init_db.sh` | 本地脚本，创建数据库并执行统一 production runner |

## Docker 部署

Docker Compose 只读挂载整个 migration 目录：

```text
backend/migrations -> /migrations
```

迁移容器启动后会：

1. 创建 `schema_migrations(version, checksum, applied_at)`。
2. 在同一 PostgreSQL session advisory lock 下按文件名顺序扫描 `/migrations/production/*.sql`。
3. 每条 migration SQL 与对应 `schema_migrations` 行在同一个 `BEGIN/COMMIT` 中提交；失败连接退出时整条回滚，下一次可从同一账本状态重试。
4. 已记录迁移先校验内容指纹。001 的指纹同时覆盖 `001_schema.sql` 与 `init.sql`；空指纹和 `legacy-checksums.txt` 中的精确历史值只允许一次性 reconcile，未知不匹配立即失败。

`init_db.sh` 使用完全相同的生成器。即使它与 Compose migrate 同时启动，也会由 advisory lock 串行化并读取同一账本。

`production/001_schema.sql` 保留 `\ir ../init.sql` 的相对引用，统一 runner 会把同一历史基线内联到受控事务中；不要直接运行 001，因为单文件执行不会建立完整版本账本。

## 本地初始化/升级

```bash
cd backend/migrations
./init_db.sh
```

脚本会按以下优先级读取数据库配置：

1. 当前 shell 已导出的环境变量。
2. `backend/.env`。
3. 仓库根目录 `.env`。
4. 本地连接示例：`localhost:5432 / effchat / <your-password> / effchat / sslmode=disable`。

支持的变量：

- `DB_HOST`
- `DB_PORT`
- `DB_USER`
- `DB_PASSWORD`
- `DB_NAME`
- `DB_SSLMODE`

本地重建数据库需要显式确认：

```bash
CONFIRM_RESET=DELETE_EFFCHAT_DB ./init_db.sh reset
```

`reset` 会删除整个 `DB_NAME` 数据库，只适合本地开发重建。普通升级不会删除用户、会话、消息、文件、Skills、字体等真实数据。

## 手动执行

不要直接执行 `init.sql` 或手工挑选 migration。需要显式控制数据库时，仍使用统一 runner：

```bash
backend/migrations/build_migration_script.sh > /tmp/effchat-migrations.sql
psql -v ON_ERROR_STOP=1 -d effchat -f /tmp/effchat-migrations.sql
```

正常安装优先使用 Docker Compose 或 `init_db.sh`，它们会创建数据库并调用同一生成器。

## 新增迁移规则

1. 只在 `production/` 里追加下一个编号文件，例如 `044_example.sql`。
2. 不再同步修改 `init.sql`；全新库也会完整执行 001 到最新 migration。
3. 已经进入发布包并可能被记录到 `schema_migrations` 的迁移文件不要回改内容；需要修正时追加新迁移。
4. migration 文件不得自行写 `BEGIN`、`COMMIT`、`ROLLBACK` 或 `CREATE INDEX CONCURRENTLY`；runner 在生成期拒绝这些语句，事务所有权只保留一层。
5. 迁移脚本必须保护真实数据，避免清空 `users`、`sessions`、`messages`、`files`、`skills`、`font_assets` 等业务表。
6. 发布包不包含测试数据入口；故障 fixture 只放在 `backend/migrations/testdata/`，不会被 production runner 扫描。

## 常见问题

### `.env.docker` 要改成 `.env` 吗？

不需要。Docker 部署继续使用 `.env.docker`，本地直接跑后端时才使用 `backend/.env`。这里说的“env 只保留基础设施配置”是指环境文件不再存模型渠道、API key、搜索服务 key 等业务运行时配置。

### 已有库没有 `schema_migrations` 怎么办？

`init_db.sh` 和 Docker migrate 都会先创建 `schema_migrations`。没有账本的既有数据库会完整执行 production 链；执行前必须先备份并在隔离副本验证，因为 runner 不会猜测任意手工 schema 的来源。

`001_schema.sql` 的当前指纹包含它和不可变 `init.sql`。历史 `legacy-baseline-v1`、已知旧指纹和旧空指纹只按 `legacy-checksums.txt` 的规则 reconcile；除此以外任何不匹配都会拒绝升级。

### 为什么不再保留测试数据？

0.3.0 是预发布包，默认只提供 schema 和真实运行路径。首个注册用户会自动成为管理员，模型、渠道、联网服务和字体都在管理员后台配置。
