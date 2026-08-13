# Docker Compose 部署

本文档描述全量 Docker 部署路径：PostgreSQL 17 开新库，`migrate` 按 `backend/migrations/production/` 记录并执行生产迁移，后端和前端分别构建为镜像。Compose 会把 `web`、`backend`、`postgres` 放在同一个 Docker 网络里；浏览器访问前端容器，前端 Nginx 在内部网络把 `/api/` 转发给 `backend:8080`。数据库和受管存储都位于 `DATA_DIR`，但运行中的 PostgreSQL 数据目录不能用普通目录复制充当备份。

## Registry 镜像部署

需要从公开 Docker Hub beta 镜像运行、而不在部署机编译源码时，使用
`docker-compose.registry.yml`。该模板保留本文件中的环境变量、端口、网络、
PostgreSQL 和 storage 挂载；只把三个应用服务的 `build`/`:local` 替换为
`${DOCKERHUB_NAMESPACE:-gjhuo}` 下的 `${EFFCHAT_VERSION:-v0.3.4-beta.1}` 镜像。

应用镜像不携带迁移 SQL。部署目录必须同时保留由同一 public release 导出的
`backend/migrations/`（包含 `build_migration_script.sh`、`init.sql`、`legacy-checksums.txt` 和
`production/*.sql`），并通过 `MIGRATIONS_DIR` 挂载给一次性 `migrate` 服务。这样源码可以
归档，migration 仍有精确、可审计和可回滚的输入；不要把运行中的数据库目录或 storage
目录作为 migration 来源。

## 组件

- `postgres`：PostgreSQL 17，数据写入 `${DATA_DIR:-../data}/postgres`。
- `migrate`：一次性迁移容器，等待数据库健康后执行尚未记录到 `schema_migrations` 的生产迁移。
- `backend`：Go release 服务，受管文件写入 `${DATA_DIR:-../data}/storage`，默认只映射到宿主机 `127.0.0.1:18080`。
- `web`：Nginx 静态前端，内置 `/api/` 到 `backend:8080` 的容器内转发，默认只映射到宿主机 `127.0.0.1:8088`。

## 首次部署

```bash
cp .env.docker.example .env.docker
```

### 需要改哪些环境变量

**必须改，否则不建议对外发：**

- `POSTGRES_PASSWORD`
- `JWT_SECRET`

**想让聊天功能真正可用：**

- 启动后用首个管理员账号进入“管理后台 → 渠道”
- 新建至少一个模型渠道，填写 `channel_key`、协议适配、Base URL 和 API key
- 在“模型”页把模型绑定到该渠道并检测可用性

**可直接沿用默认值：**

- `COMPOSE_PROJECT_NAME`
- `DOCKER_NETWORK`
- `DATA_DIR`
- `WEB_PORT`
- `BACKEND_PORT`
- `DOCKER_LOG_MAX_SIZE` / `DOCKER_LOG_MAX_FILE`（默认每个容器最多保留 `20m * 5`）
- `SERVER_MODE`
- `DB_HOST` / `DB_PORT` / `DB_NAME`
- `CORS_EXTRA_ORIGINS`（同源公共反代一般不填；前后端不同域名才填）
- `TRUST_PROXY_HEADERS`（使用内置 Web 反代时保持 `true`；后端可能绕过可信代理时设为 `false`）
- `RUN_FIRST_OUTPUT_TIMEOUT` / `SSE_HEARTBEAT_INTERVAL`

`.env.docker` 完全遵循 Docker Compose 的 dotenv 语义：单引号、双引号、转义、行尾注释和当前 shell 覆盖均由 Compose 解析。`docker-build.sh` 与 `storage-layout.sh` 只读取 `docker compose config --environment` 的最终插值结果，不会 `source` 或自行解释 env 文件；因此 secret 占位检查、`DATA_DIR` 文件操作和实际 Compose 挂载使用同一值。可先执行 `scripts/docker-build.sh config` 检查最终配置。

`RUN_FIRST_OUTPUT_TIMEOUT` 支持 Go duration（如 `90s`、`25m`）或纯秒数，只限制模型返回首个有效文本、思考内容或具名工具调用前的等待。一旦有效输出开始，后端会继续读取流直到 EOF；用户停止、服务排空、账号或会话失效仍可取消任务。值为 `0` 时使用聊天 15 分钟、压缩 5 分钟的内建首包默认值。

`scripts/docker-build.sh up` 先构建全部镜像，再启动 PostgreSQL、执行 migration 和 storage layout，最后用已有镜像 `up --no-build --wait` 切换并等待服务健康；构建失败不会先停服务或改 schema/storage。`reset-db` 也先完成构建，再执行明确确认的删除和等待健康启动。

### Git Skill 的出站网络边界

管理员从 Git 导入 Skill 时，backend 只接受 HTTPS/443、无凭据且不跟随重定向的仓库地址。自定义主机的 DNS 必须返回真实公网 A/AAAA 地址；backend 会在 Go 预检后把这些地址固定到每个 Git 子进程，代理、`GIT_*`、global/system Git config 和 URL rewrite 不会被继承。loopback、私网、link-local、benchmark（包括 `198.18.0.0/15`）或混合 blocked DNS 响应会整体拒绝，这是防止 DNS rebinding/SSRF 的预期行为。

因此，使用本地代理的 Docker/OrbStack 网络如果把外部域名解析成 synthetic `198.18.0.0/15`，Git Skill 自定义来源会被拒绝；不要通过放宽地址策略或恢复任意代理来“修复”。应为部署容器提供真实公网 DNS 与 HTTPS 直连，或在受信网络边界单独配置经过审计的出站方案。GitHub、GitLab、Bitbucket、Codeberg 四个精确主机名保留 CDN 轮转例外，但仍不允许重定向、凭据、交互认证和 Git 配置注入。

### 朋友拿源码后的最短启动流程

1. 复制环境模板：

   ```bash
   cp .env.docker.example .env.docker
   ```

2. 修改 `.env.docker`：

   - 把 `POSTGRES_PASSWORD` 改成新密码
   - 把 `JWT_SECRET` 改成强随机串
   - 模型渠道、API key、搜索 provider、网页提取服务和 MinerU OCR 不要写进 `.env.docker`，启动后在管理员网页配置

3. 构建镜像：

   ```bash
   scripts/docker-build.sh build
   ```

4. 启动整套服务：

   ```bash
   scripts/docker-build.sh up
   ```

5. 打开浏览器：

   ```text
   http://127.0.0.1:8088
   ```

6. 进入管理后台配置渠道和模型。管理员保存配置后只影响新请求，正在进行的流式 run 不会中途切换凭据。

官方模板默认把源码和运行数据分开。若源码目录名为 `EffChat`，启动后父目录结构为：

```text
parent/
├── EffChat/
│   ├── .env.docker
│   ├── docker-compose.yml
│   ├── backend/
│   └── frontend/
└── data/
    ├── postgres/
    └── storage/
        ├── attachments/
        │   ├── originals/
        │   ├── extracted/
        │   └── ocr-staging/
        ├── avatars/
        ├── fonts/
        └── skills/
```

不要把原生 `docker compose up -d --build` 当作等价部署入口。它不会执行
`docker-build.sh` 的占位 secret 检查、统一 `DATA_DIR` 解析、BUILD_REF 生成、
storage layout 升级、显式 migration 与健康等待。原生 `docker compose config`
可用于只读诊断；构建、升级和启动统一使用 `scripts/docker-build.sh`。

默认 Web 入口：

```text
http://127.0.0.1:8088
```

可通过 `.env.docker` 的 `WEB_PORT` 修改宿主机端口。
默认后端入口：

```text
http://127.0.0.1:18080
```

可通过 `.env.docker` 的 `BACKEND_PORT` 修改宿主机端口。

### 只想先把系统跑起来

如果朋友只是想确认“前后端 + 数据库 + 迁移”能否启动，不需要先配模型 key。此时服务可以起来，但聊天请求会因为没有可用模型渠道而失败。
如果想完整体验对话，至少要在管理员后台配置一个渠道和一个模型。

## 新库初始化

Compose 启动时会先确保存在：

```text
schema_migrations(version, checksum, applied_at)
```

然后按文件名顺序执行：

```text
backend/migrations/production/*.sql
```

当前生产基线是 `backend/migrations/production/001_schema.sql`，它只引用 schema-only 的 `backend/migrations/init.sql`，用于创建表、索引、触发器和视图。生产迁移不写入模型、系统配置、用户组或测试数据；这些运行时默认值由 Go 代码兜底，管理员显式保存后才会落库。

已有数据库再次 `up` 时，已有指纹的 `schema_migrations` 记录会先校验文件内容指纹，再跳过，不会重放初始化数据，也不会覆盖管理员改过的模型列表。旧库第一次接入指纹时只能一次性信任当前迁移文件并回填，之后任一不匹配都会失败。后端启动会要求当前生产迁移版本已经存在；`/health` 同时返回版本、构建标识和 schema 版本，便于确认实际运行的镜像与数据库一致。

构建标识按来源自动生成：干净 Git 工作树使用短 commit SHA；有在制源码时使用 `SHA-dirty-内容指纹`；不包含 `.git` 的交付源码使用 `source-内容指纹`。内容指纹只覆盖实际源码和构建配置，排除环境变量、数据、上传、依赖及构建产物。可在构建前执行 `scripts/docker-build.sh build-ref` 查看将要注入的值；发布系统显式传入非空 `BUILD_REF` 时以调用方值为准。

Compose 默认由内置 Nginx 覆盖并传递 `X-Real-IP`，因此模板将 `TRUST_PROXY_HEADERS` 设为 `true`，认证限流可按浏览器真实地址区分。后端端口只绑定回环地址；如果其他部署方式允许流量绕过可信代理，应将该变量设为 `false`，或确保唯一入口代理会清洗客户端提供的转发头。

模型渠道、API key、搜索服务、网页提取服务和 MinerU OCR 不在 `.env.docker` 中填写；启动后由管理员在网页后台配置。后台使用步骤见 [管理员配置指南.md](管理员配置指南.md)。

如果需要彻底重建新库：

```bash
CONFIRM_RESET=DELETE_EFFCHAT_DATA scripts/docker-build.sh reset-db
```

该命令会删除 `${DATA_DIR:-../data}/postgres`、`${DATA_DIR:-../data}/storage` 和遗留的 `${DATA_DIR:-../data}/uploads`，包括数据库与全部受管文件。

## 常用命令

```bash
scripts/docker-build.sh build    # 构建 backend 和 web 镜像
scripts/docker-build.sh up       # 构建并启动整套服务
scripts/docker-build.sh config   # 渲染并校验 compose 配置
scripts/docker-build.sh build-ref # 查看将注入 /health 的构建标识
scripts/docker-build.sh logs     # 跟随日志
scripts/docker-build.sh down     # 停止服务，不删除 DATA_DIR
```

## 数据目录

默认使用：

```env
DATA_DIR=../data
```

相对路径按源码目录解析，因此默认数据位于源码同级目录，而不是 Git 工作树内。
受控导出部署中对应 `runtime/src` 与 `runtime/data`。迁移源码或发布目录不等于
迁移数据；不要在 PostgreSQL 运行时复制整个 `DATA_DIR` 或 `postgres` 目录。

如果服务器数据盘另有路径，可以改成绝对路径：

```env
DATA_DIR=/srv/effchat/data
```

## 备份与隔离恢复

```bash
scripts/backup-restore.sh backup
scripts/backup-restore.sh verify /path/to/effchat-YYYYMMDDTHHMMSSZ
scripts/backup-restore.sh restore \
  /path/to/effchat-YYYYMMDDTHHMMSSZ \
  /path/to/empty-restore-root \
  /path/to/.env.docker
```

`backup` 会记录当前正在运行的 Web、backend 和提取器并优雅停止它们，避免 `pause` 把跨数据库与文件系统的在途事务冻结在中间状态；随后从仍运行的 PostgreSQL 生成 custom-format `pg_dump`，同时归档稳定的 `storage`。只有数据库 dump、storage tar、受保护的逐文件 SHA-256 清单、migration 版本/checksum 账本、Compose checksum、应用版本、build ref、schema 和 PostgreSQL major 全部成功并由同一验证器复核后，临时目录才会原子发布为一个版本化备份集；失败会删除临时工件，并且无论成功或失败都只恢复原先运行的服务。默认输出到 `${DATA_DIR}/backups`，可用 `BACKUP_ROOT` 指向受保护的异机挂载；自定义目录的父目录必须预先存在。

备份不包含 `.env.docker`、数据库密码、JWT secret 或后台渠道密钥，但 database dump 本身包含消息、用户、渠道密钥等数据库内容。运维方仍需为备份目录配置最小权限、静态加密、异机副本、保留周期和安全销毁。

`restore` 不覆盖或切换现有部署，只接受空、非符号链接且与活动 `DATA_DIR` 无重叠的目标，并通过原子目录锁阻止两个恢复任务写入同一根目录。第三个参数提供恢复栈所需的基础设施 secret；脚本不会复制该文件，而是强制用独立 `COMPOSE_PROJECT_NAME`、显式 Docker network、目标 `DATA_DIR` 和 Docker 分配的 loopback 动态端口覆盖冲突字段。恢复顺序固定为：验证全部工件与路径 → 在临时目录安全解包并逐文件复核 storage → 启动隔离 PostgreSQL 并确认空库/major → `pg_restore` → 核对备份 migration 账本 → 运行当前统一 migration runner → 核对受管文件引用 → 等待四个服务并检查内部 health。任一步失败都只停止隔离 project（不使用 `-v`）并清理本次创建的目标内容。

成功后命令会输出隔离 Web URL，并在目标根写入不含 secret 的 `restore-manifest`。此时仍需通过浏览器完成登录、会话读取、文件预览与下载验收；完成后停止隔离栈但保留恢复数据：

```bash
scripts/backup-restore.sh stop-restore \
  /path/to/empty-restore-root \
  /path/to/.env.docker
```

确认 `restore-manifest`、浏览器验收和目标路径无误后，再由运维方删除该隔离目录。脚本从不删除活动 volume，也不会自动把隔离数据切换为生产数据。

### 从旧 uploads 布局升级

`scripts/docker-build.sh up` 会在启动新后端前自动执行一次存储迁移：停止旧 Web/Backend，把旧 `data/uploads` 复制到职责分离的 `data/storage`，校验复制结果，并在 PostgreSQL 事务中更新附件、头像、字体和 Skills 路径。迁移只会清理新 `storage` 中没有数据库引用的复制残留，不会改动旧 `uploads`；旧目录默认作为回滚副本保留。

可单独检查和验证：

```bash
scripts/storage-layout.sh plan
scripts/storage-layout.sh verify
```

`verify` 对仍可用的 `staged` / `formal` 附件、头像、字体和 Skills 保持严格磁盘存在性校验。已由用户删除并进入 `cleanup_claimed` 保留期的附件仍会校验所有数据库路径位于受管 `storage/` 内，但允许尚未生成或已被幂等清理的候选文件不存在；到期后的正式 cleanup 会删除剩余字节并将记录收口为 `storage_removed`。

只有迁移满 7 天、验证通过并显式确认后，才允许删除旧副本：

```bash
CONFIRM_STORAGE_FINALIZE=DELETE_LEGACY_UPLOADS scripts/storage-layout.sh finalize
```

迁移失败时工具会自动恢复数据库路径；旧 `uploads` 仍在保留期内且 marker 为 `migrated` 时，可执行 `scripts/storage-layout.sh rollback`，然后启动上一版本源码。`finalize` 成功后 marker 会永久转为 `finalized`，此时旧文件已删除，工具会在停止服务或修改数据库前拒绝 rollback。不要手工移动单个文件、修改 marker 或直接批量替换数据库路径。

## 主机公共反代

项目自身不提供生产反代配置。如果服务器已经有公共 Nginx/Caddy/网关，推荐把域名直接转发到宿主机的 Web 端口：

```text
https://chat.example.com -> http://127.0.0.1:8088
```

前端容器会继续在 Docker 网络内把 `/api/` 转给后端，不需要主机公共反代再单独配置后端路由。`BACKEND_PORT` 仍默认绑定在 `127.0.0.1:18080`，用于本机调试和健康检查，不会直接对公网开放。

如果你有意让主机公共反代绕过前端容器、把 `/api/` 直接转到后端端口，则需要保留这些头：

```nginx
proxy_set_header Host $http_host;
proxy_set_header X-Forwarded-Host $http_host;
proxy_set_header X-Forwarded-Proto $scheme;
proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
```

SSE 请求需要关闭代理缓冲：

```nginx
proxy_buffering off;
proxy_read_timeout 3600s;
```

如果前端和 API 是不同公网域名或不同端口，需要在 `.env.docker` 设置精确前端来源：

```text
CORS_EXTRA_ORIGINS=https://chat.example.com
```

常规推荐路径是公共反代只指向 `WEB_PORT`；这样浏览器看到的是同源请求，一般不需要额外 CORS 配置。
