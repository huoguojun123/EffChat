# Docker Compose 部署

本文档提供两条入口：个人用户优先使用一条命令安装；需要保留源码或自行管理完整配置时使用仓库内的 Docker Compose。两条路径都使用同一个 EffChat 应用镜像、同一组 migration 和同一数据生命周期规则。

统一镜像不等于单容器多进程。`web`、`backend`、`py-extractor` 和一次性 `migrate` 仍是独立容器角色，只是从同一个 `gjhuo/effchat` manifest 启动；PostgreSQL 始终使用官方 `postgres:17` 镜像，可由 Compose 专用启动，也可连接外部实例。

## 一条命令部署

在已经安装 Docker Engine 和 Docker Compose v2 的 Linux/macOS 主机上运行：

```bash
curl -fsSL https://raw.githubusercontent.com/huoguojun123/EffChat/main/scripts/install.sh | bash
```

安装器会提示安装目录、Web 端口和数据库来源。默认选择 `bundled`，自动生成 PostgreSQL/JWT secret 并启动专用 PostgreSQL；选择 `external` 时按提示填写 host、port、database、user、隐藏 password 和 SSL mode。随后脚本从同一测试版 tag 下载 Compose，拉取 Docker Hub 镜像并等待服务健康。默认安装到当前目录的 `effchat/`，访问 `http://127.0.0.1:8088`。指定其他目录时运行：

```bash
curl -fsSL https://raw.githubusercontent.com/huoguojun123/EffChat/main/scripts/install.sh | EFFCHAT_HOME=/srv/effchat bash
```

新安装接受不存在或完全为空的目标目录，生成以下活动布局：

```text
effchat/
├── .env
├── compose.yml
└── data/
```

再次对同一目录运行脚本并确认 `update` 即可升级。脚本识别当前 `.env + compose.yml`，也能把历史 `.env.docker`、三镜像 Compose 和宿主机 migration 布局收敛到同一结构；端口、项目名、未知配置、JWT、数据库凭据、数据路径和所有 data/storage/backups 保持不变。被替换的 Compose、环境入口和旧 migration 会进入带时间戳的 `deployment-backups/`。无法确认属于 EffChat 的目录会直接停止，不覆盖任意自定义部署。

应用 migration 已嵌入统一镜像，部署目录不再需要活动 `migrations/`。更新不会执行 `docker compose down -v`，不会删除 PostgreSQL、storage 或备份。

安装完成后，模型渠道、API key、搜索服务和网页提取 provider 在管理员后台配置，不写入安装命令或公开文件。

个人用户日常只需在安装目录执行：

```bash
docker compose ps
docker compose logs -f
docker compose pull
docker compose up -d
```

升级前请先按“备份与隔离恢复”完成备份；不要用 `docker compose down -v`。

## 标准 Docker Compose 部署

标准部署适合希望保留完整源码、自己选择版本、调整端口或接入现有运维流程的用户。下面的 registry 模板运行已发布镜像；源码构建仍使用 `scripts/docker-build.sh`。

## Registry 镜像部署

需要从公开 Docker Hub beta 镜像运行、而不在部署机编译源码时，使用 `docker-compose.registry.yml`。模板中的四个 EffChat 角色都引用 `${DOCKERHUB_NAMESPACE:-gjhuo}/effchat:${EFFCHAT_VERSION:-v0.4.1-beta.9}`；migration SQL 与 runner 随同一镜像发布，不再挂载宿主机源码。

## 组件

- `postgres`：可选的 PostgreSQL 17 专用服务，仅 `bundled-db` profile 启用，数据写入 `${DATA_DIR:-../data}/postgres`。
- `migrate`：一次性迁移容器，等待数据库健康后执行尚未记录到 `schema_migrations` 的生产迁移。
- `py-extractor`：隔离运行文档提取与 OCR 适配，保留独立内存限制和健康检查。
- `backend`：Go release 服务，受管文件写入 `${DATA_DIR:-../data}/storage`，默认只映射到宿主机 `127.0.0.1:18080`。
- `web`：Nginx 静态前端，内置 `/api/` 到 `backend:8080` 的容器内转发，默认只映射到宿主机 `127.0.0.1:8088`。

四个 EffChat 角色拥有相同 image ID，但继续使用各自 command、权限、资源上限、日志、依赖和健康探针；没有引入 supervisor 或常驻多进程容器。

## 首次部署

```bash
cp .env.docker.example .env.docker
```

### 需要改哪些环境变量

**必须改，否则不建议对外发：**

- `POSTGRES_PASSWORD`
- `JWT_SECRET`

默认模板已经设置 `COMPOSE_PROFILES=bundled-db`，因此上述 `POSTGRES_*` 同时用于专用 PostgreSQL 与应用连接。`DB_PASSWORD` 应与 `POSTGRES_PASSWORD` 保持一致；一键安装器会自动保证这一点。

### PostgreSQL 来源

**专用 PostgreSQL（默认）**

保持：

```env
COMPOSE_PROFILES=bundled-db
DATABASE_URL=
DB_HOST=postgres
```

**外部 PostgreSQL**

不要启用 `bundled-db`，并二选一：

```env
COMPOSE_PROFILES=
DATABASE_URL=postgres://effchat:encoded-password@db.example.com:5432/effchat?sslmode=require
```

或：

```env
COMPOSE_PROFILES=
DATABASE_URL=
DB_HOST=db.example.com
DB_PORT=5432
DB_USER=effchat
DB_PASSWORD=replace-with-a-strong-password
DB_NAME=effchat
DB_SSLMODE=require
```

`DATABASE_URL` 非空时优先于 `DB_*`。backend 与 migrate 消费同一组变量；外部数据库不可达、凭据错误或 migration 失败时不会偷偷回退到本地 PostgreSQL。PostgreSQL 是唯一数据库实现，本轮没有引入 SQLite 或双数据库迁移。

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

仓库内 `.env.docker` 完全遵循 Docker Compose 的 dotenv 语义：单引号、双引号、转义、行尾注释和当前 shell 覆盖均由 Compose 解析。`docker-build.sh` 与 `storage-layout.sh` 只读取 `docker compose config --environment` 的最终插值结果，不会 `source` 或自行解释 env 文件；因此 secret 占位检查、`DATA_DIR` 文件操作和实际 Compose 挂载使用同一值。可先执行 `scripts/docker-build.sh config` 检查最终配置。一键安装生成的 `.env` 使用 Compose 单引号编码，更新时保留未知键和 secret，并拒绝换行值。

`RUN_FIRST_OUTPUT_TIMEOUT` 支持 Go duration（如 `90s`、`25m`）或纯秒数，只限制模型返回首个有效文本、思考内容或具名工具调用前的等待。一旦有效输出开始，后端会继续读取流直到 EOF；用户停止、服务排空、账号或会话失效仍可取消任务。值为 `0` 时使用聊天 15 分钟、压缩 5 分钟的内建首包默认值。

`scripts/docker-build.sh up` 先构建统一应用镜像，再按所选数据库模式执行 migration 和启动服务；构建失败不会先停服务或改 schema/storage。`reset-db` 也先完成构建，再执行明确确认的删除和等待健康启动。

### Git Skill 的出站网络边界

管理员从 Git 导入 Skill 时，backend 只接受 HTTPS/443、无凭据且不跟随重定向的仓库地址。自定义主机的 DNS 必须返回真实公网 A/AAAA 地址；backend 会在 Go 预检后把这些地址固定到每个 Git 子进程，代理、`GIT_*`、global/system Git config 和 URL rewrite 不会被继承。loopback、私网、link-local、benchmark（包括 `198.18.0.0/15`）或混合 blocked DNS 响应会整体拒绝，这是防止 DNS rebinding/SSRF 的预期行为。

因此，使用本地代理的 Docker/OrbStack 网络如果把外部域名解析成 synthetic `198.18.0.0/15`，Git Skill 自定义来源会被拒绝；不要通过放宽地址策略或恢复任意代理来“修复”。应为部署容器提供真实公网 DNS 与 HTTPS 直连，或在受信网络边界单独配置经过审计的出站方案。GitHub、GitLab、Bitbucket、Codeberg 四个精确主机名保留 CDN 轮转例外，但仍不允许重定向、凭据、交互认证和 Git 配置注入。

## 源码构建部署（完整流程）

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

### 仅启动基础服务

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

模型渠道、API key、搜索服务、网页提取服务和 MinerU OCR 不在 `.env.docker` 中填写；启动后由管理员在网页后台配置。后台使用步骤见 [管理员配置](administration.md)。

如果需要彻底重建新库：

```bash
CONFIRM_RESET=DELETE_EFFCHAT_DATA scripts/docker-build.sh reset-db
```

该命令会删除 `${DATA_DIR:-../data}/postgres`、`${DATA_DIR:-../data}/storage` 和遗留的 `${DATA_DIR:-../data}/uploads`，包括数据库与全部受管文件。

## 常用命令

```bash
scripts/docker-build.sh build    # 构建统一 EffChat 应用镜像
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

备份不包含活动 `.env` / `.env.docker`、数据库密码、JWT secret 或后台渠道密钥，但 database dump 本身包含消息、用户、渠道密钥等数据库内容。运维方仍需为备份目录配置最小权限、静态加密、异机副本、保留周期和安全销毁。

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
