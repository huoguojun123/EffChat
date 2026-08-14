# 开源发布检查清单

> 本清单用于公开仓库的首次开放与后续版本发布。
>
> 当前 beta 版本线：`v0.4.0-beta.1`；源码仓库仍为 private，稳定版与 `latest` 尚未发布。

## 首次公开

### 法律与治理

- [x] 项目所有者已确定 Apache-2.0，根目录包含完整 `LICENSE`、`NOTICE` 和第三方声明。
- [x] README 明确当前为 beta/pre-release。
- [x] `SECURITY.md` 提供 GitHub 私密漏洞报告入口。
- [x] `CONTRIBUTING.md` 说明环境、测试、数据安全和 PR 规则。
- [x] 已配置 Issue、PR 模板和 Dependabot。
- [ ] 默认提示词、图标、图片和其他素材的来源允许公开。
- [x] 第三方依赖许可证没有已知冲突。
- [x] 三个应用镜像从实际分发依赖生成第三方许可正文/版权归档，依赖缺少许可材料或精确版本 fallback 时构建失败。

### 内容与泄漏

- [x] 仓库不包含真实 `.env`、密钥、用户数据、数据库 dump、上传文件、日志、备份或测试报告。
- [x] 仓库不包含私有主机、生产域名、本机绝对路径或内部项目文档。
- [x] 示例值和测试 fixtures 均为不可用的模拟信息。
- [x] 已对当前目录和完整 Git 历史执行 Gitleaks。
- [x] 所有扫描命中均已人工确认或修复。

参考命令：

```bash
find . -name '.env*' -o -name '*.env' -o -name '.DS_Store'

rg -n --hidden \
  --glob '!.git/**' \
  --glob '!package-lock.json' \
  '/Users/|/home/|BEGIN (RSA |OPENSSH |EC )?PRIVATE KEY|Authorization: Bearer|sk-[A-Za-z0-9]{12,}'

gitleaks dir .
gitleaks git .
```

### GitHub

- [x] CI 覆盖 Go 测试与 vet、前端 lint/test/build、Compose 渲染和容器构建。
- [x] Gitleaks 在 pull request 和 `main` push 时运行。
- [x] tag workflow 在 GHCR 登录前校验 tag commit 可达 `origin/main`，且同一 commit 的 CI 与 Gitleaks 全部成功。
- [x] 三个应用镜像用一次多架构构建同时写入 GHCR 与 Docker Hub commit staging manifest；全部成功后才在两个 registry 统一晋级版本与 `sha-` tag，并生成 SBOM 与 provenance。
- [x] `main` 已启用分支保护，禁止 force push/删除并要求 PR、对话解决及七项 CI/Gitleaks 检查通过；仓库公开后继续沿用同一规则。
- [ ] GitHub 私密漏洞报告已启用。
- [ ] 仓库描述已核对；topics、主页和从 private 改为 public 仍待首次开放收口。

## 每次提交或 Pull Request

- [ ] 改动只处理一个明确问题。
- [ ] 代码、migration、测试和公开文档保持同步。
- [ ] 没有新增真实环境、数据、内部路径或无授权素材。
- [ ] `git diff --check` 通过。
- [ ] 目标模块测试通过。
- [ ] 部署文件变更后，示例环境变量可以渲染 Compose。
- [ ] 可见 UI 变更已分别检查桌面端和移动端。

## 每次版本发布

### 版本与迁移

- [ ] 目标 tag 对应公开 `main` 上的已合并 commit。
- [ ] 目标 commit 的 Backend、Frontend、Python extractor、PostgreSQL integration、Compose/container 与 Gitleaks check 均为 completed/success。
- [ ] 应用版本、Git tag、build ref 和 changelog 一致。
- [ ] tag 尚未存在，不覆盖或移动旧 tag。
- [ ] migration 已通过空库、升级库和重复执行验证。
- [ ] 发布说明列出新增、修复、已知问题、迁移和回滚方式。

### 自动化验证

```bash
cd backend
go test ./...
go vet ./...

cd ../frontend
npm ci
npm run lint
npm test
npm run build

cd ..
scripts/test-postgres.sh
docker compose --env-file .env.docker.example config
scripts/check-image-licenses.sh effchat-backend:local effchat-web:local effchat-py-extractor:local
git diff --check
```

- [ ] CI 全部通过。
- [ ] Gitleaks 对当前目录和完整历史通过。
- [ ] 依赖漏洞与许可证扫描已审阅。
- [ ] 三个应用镜像构建成功。
- [ ] 三个应用镜像的 component manifest、许可正文和 SHA-256 离线校验通过。
- [ ] `linux/amd64` 与 `linux/arm64` manifest 均存在。
- [ ] GHCR 与 Docker Hub 的镜像 digest 一致，且 build ref、SBOM 和 provenance 可追溯到 tag。
- [ ] 预发布 tag 没有更新 `latest`。

### 部署冒烟

- [ ] 使用发布源码或 tag 对应镜像完成本地部署。
- [ ] PostgreSQL 与受管存储未被覆盖或删除。
- [ ] 所有容器 healthy。
- [ ] `/health` 返回目标版本、build ref 和 schema。
- [ ] 登录、会话读取、发送、断线恢复和文件入口通过。
- [ ] 上一版本镜像及数据库恢复方案可用。

## 发布结论

以下信息有任一项未知时，结论必须为“阻塞”：

```text
公开 commit：
版本 tag：
schema：
镜像 digest：
CI：
Gitleaks：
依赖审计：
本地部署：
回滚版本：
已知问题：
```
