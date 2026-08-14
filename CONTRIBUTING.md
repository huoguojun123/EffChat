# 贡献指南

EffChat 是一个仍在积极迭代的轻量自托管项目。欢迎提交边界清晰的 Bug 修复、测试、文档纠正和功能改进。

## 开始之前

- 先搜索已有 Issue 和 Pull Request，避免重复工作。
- 大型功能或架构变化应先创建 Issue，确认问题、边界和维护成本。
- 不得提交真实凭据、用户数据、私有主机、本机绝对路径、数据库 dump、生成报告或部署备份。
- 一个 Pull Request 只解决一个明确问题；同一根因涉及的前后端、测试和文档可以放在一起。
- 遵循现有 Go backend 与 TypeScript/React frontend 的职责边界。

## 本地环境

需要安装：

- `backend/go.mod` 声明的 Go 版本；
- Node.js 22 或更高版本与 npm；
- Docker 与 Docker Compose。

启动完整环境：

```bash
cp .env.docker.example .env.docker
# 启动前替换 POSTGRES_PASSWORD 和 JWT_SECRET
scripts/docker-build.sh up
```

## 验证

根据改动范围执行对应检查：

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
docker compose --env-file .env.docker.example config
git diff --check
```

数据库或 migration 变更还必须执行：

```bash
scripts/test-postgres.sh
```

## Pull Request

- 标题和 Git commit 使用英文。
- 正文说明行为变化、修改原因、数据或兼容性影响以及验证结果。
- 非平凡行为必须补充能够复现根因并防止回归的测试。
- 行为、架构或部署要求变化时，同步更新 `README.md`、`ARCHITECTURE.md` 或对应公开文档。
- 可见 UI 变化应提供桌面端与移动端截图。
- 确认测试只使用虚构数据，公开文档不含私有部署信息。

提交贡献即表示你同意该贡献按照本项目许可证发布。
