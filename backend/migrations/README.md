# 数据库迁移说明

0.3.0 预发布后，迁移体系只保留一条线：

- `init.sql`：全新数据库的 schema 快照，只建表、索引、触发器、视图，不写模型、配置、用户组或测试数据。
- `production/*.sql`：唯一增量迁移链，已有数据库升级只跑这里，并用 `schema_migrations.version/checksum` 记录文件名和内容指纹。
- `init_db.sh`：本地初始化/升级脚本，内部同样按 `production/*.sql` 顺序执行。

不再保留顶层 `001_*.sql` 到 `025_*.sql` 的重复迁移，也不提供测试数据脚本。

## 文件职责

| 文件 | 说明 |
|------|------|
| `init.sql` | 当前 schema 快照，供全新库快速建库，也被 `production/001_schema.sql` 引用 |
| `production/001_schema.sql` | 生产基线迁移，使用 `\ir ../init.sql` 相对引用 schema 快照 |
| `production/002_*.sql` 及后续 | 生产增量迁移，只允许追加新文件，不回改已发布迁移 |
| `init_db.sh` | 本地脚本，创建数据库、确保 `schema_migrations`、按 production 文件名顺序执行未记录迁移 |

## Docker 部署

Docker Compose 会挂载：

```text
backend/migrations/init.sql -> /migrations/init.sql
backend/migrations/production -> /migrations/production
```

迁移容器启动后会：

1. 创建 `schema_migrations(version, checksum, applied_at)`。
2. 在同一 PostgreSQL advisory lock 会话中按文件名顺序扫描 `/migrations/production/*.sql`。
3. 已记录且已有指纹的迁移先校验内容指纹，未记录的迁移执行成功后写入文件名和指纹。`001_schema.sql` 的指纹同时覆盖它引用的 `init.sql` 快照；旧库中没有指纹的历史记录首次升级时属于一次性信任回填（TOFU），之后所有不匹配都会失败。

`init_db.sh` 使用相同的锁和校验规则，不能与 Compose 的迁移容器交错执行。

`production/001_schema.sql` 使用 `\ir ../init.sql`，因此在 Docker 和本机 `psql -f production/001_schema.sql` 下都能解析到同一个 schema 快照。

## 本地初始化/升级

```bash
cd backend/migrations
./init_db.sh
```

脚本会按以下优先级读取数据库配置：

1. 当前 shell 已导出的环境变量。
2. `backend/.env`。
3. 仓库根目录 `.env`。
4. 默认值：`localhost:5432 / postgres / 123456 / fchat / sslmode=disable`。

支持的变量：

- `DB_HOST`
- `DB_PORT`
- `DB_USER`
- `DB_PASSWORD`
- `DB_NAME`
- `DB_SSLMODE`

本地重建数据库需要显式确认：

```bash
CONFIRM_RESET=DELETE_FCHAT_DB ./init_db.sh reset
```

`reset` 会删除整个 `DB_NAME` 数据库，只适合本地开发重建。普通升级不会删除用户、会话、消息、文件、Skills、字体等真实数据。

## 手动执行

全新库可以直接执行 schema 快照：

```bash
createdb -h localhost -U postgres fchat
psql -h localhost -U postgres -d fchat -f init.sql
```

如果要模拟生产升级，请执行 production 链并记录 `schema_migrations`，不要手动挑选顶层历史迁移。

## 新增迁移规则

1. 只在 `production/` 里追加下一个编号文件，例如 `021_example.sql`。
2. 同步更新 `init.sql`，保证全新库和升级库结构一致。
3. 已经进入发布包并可能被记录到 `schema_migrations` 的迁移文件不要回改内容；需要修正时追加新迁移。
4. 迁移脚本必须保护真实数据，避免清空 `users`、`sessions`、`messages`、`files`、`skills`、`font_assets` 等业务表。
5. 发布包不包含测试数据入口；演示数据如未来需要，应放在单独的开发文档和非发布脚本中。

## 常见问题

### `.env.docker` 要改成 `.env` 吗？

不需要。Docker 部署继续使用 `.env.docker`，本地直接跑后端时才使用 `backend/.env`。这里说的“env 只保留基础设施配置”是指环境文件不再存模型渠道、API key、搜索服务 key 等业务运行时配置。

### 已有库没有 `schema_migrations` 怎么办？

`init_db.sh` 和 Docker 迁移容器都会先创建 `schema_migrations`。第一次接入 production 链时会按文件名执行迁移；这些迁移应保持幂等，已有列、表、索引会跳过或安全更新。

`001_schema.sql` 是迁移链收敛前的历史基线，因此统一记录为 `legacy-baseline-v1`，不再把会随 fresh install 演进的 `init.sql` 内容作为它的校验和。`002` 及之后的迁移仍逐文件严格校验；任何不匹配都会拒绝升级。

### 为什么不再保留测试数据？

0.3.0 是预发布包，默认只提供 schema 和真实运行路径。首个注册用户会自动成为管理员，模型、渠道、联网服务和字体都在管理员后台配置。
