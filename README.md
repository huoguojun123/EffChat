# EffChat

EffChat 是一个面向小团队自托管的 AI agent workbench。目标是比重型聊天平台更轻、更快、更可控，同时保留多模型切换、Eino Agent、工具调用、文件工作区、Skills、流式输出和代码块工作区。

[GitHub 仓库](https://github.com/huoguojun123/EffChat)

> [!WARNING]
> EffChat 当前是 `pre-release 0.3.4`。数据迁移、配置兼容性和公开 API 仍可能变化，请在升级前备份 PostgreSQL 与受管存储。

## pre-release 0.3.4 定位

当前预发布版按“小团队自托管 agent workbench”收口：

- 管理员网页配置模型渠道、API key、搜索、网页提取服务和 MinerU 精准 OCR。
- Docker 部署继续使用 `.env.docker`；环境变量文件只保留数据库、JWT、端口、存储路径、加密密钥、Python extractor 内部地址等基础设施项。
- 聊天运行、回答重试和断线恢复使用持久化 run/attempt 事实；浏览器连接中断后，后端仍可继续生成并在恢复时对账同一轮回答。
- 现有工具支持后台启停、超时控制和上下文预算；错误作为结构化结果回传，降级单独计入管理统计；工具调用过程在消息内展示，Alpha 不做持久化工具审计日志。
- 文件工作区按需读取解析文本，PDF 可优先走 MinerU 精准 OCR 并由本地解析兜底，支持磁盘删除、管理员手动清理遗留文件和大文件多窗口搜索。
- 用户组支持每日消息数、每日模型 token 近似上限、并发 run、每日工具调用、每日搜索、每日网页提取和 OCR 限额。

## 暂不包含

Alpha 不包含代码执行沙盒、Shell 工具、浏览器自动化、完整 RBAC、成本账单和 Skills marketplace。这些能力会显著扩大安全与部署边界，后续会作为可选 sidecar 或路线图能力重新设计。

## 技术栈

- Backend: Go + Gin + Eino
- Frontend: Vite + React 19 + TypeScript + Tailwind CSS v4
- Database: PostgreSQL
- Extractor: Python sidecar
- Deploy: Docker Compose

## 快速开始

```bash
git clone https://github.com/huoguojun123/EffChat.git
cd EffChat
cp .env.docker.example .env.docker
# 编辑 .env.docker，至少替换 POSTGRES_PASSWORD 和 JWT_SECRET
scripts/docker-build.sh up
```

首次启动后，首个注册用户会自动成为管理员。进入管理后台配置渠道、模型、联网服务、工具、用户组限额、字体和文件清理。

详细部署步骤见 [Docker-Compose-部署.md](docs/03-实施计划/Docker-Compose-部署.md)。
管理员使用步骤见 [管理员配置指南.md](docs/03-实施计划/管理员配置指南.md)。

## 安全与贡献

- 安全漏洞请按 [SECURITY.md](SECURITY.md) 私下报告，不要公开创建 Issue。
- 开发环境、验证命令和 PR 规则见 [CONTRIBUTING.md](CONTRIBUTING.md)。
- 当前版本变化见 [CHANGELOG.md](CHANGELOG.md)。

## 许可证

EffChat 自有源码采用 [Apache License 2.0](LICENSE)。第三方依赖与素材继续
遵循各自许可证，主要依赖及非代码素材的声明见
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) 和 [NOTICE](NOTICE)。

## 开源发布

公开源码导出使用：

```bash
bash scripts/export-public-source.sh /path/to/public-export
```

发布前请按 [开源发布检查清单.md](docs/03-实施计划/开源发布检查清单.md) 做泄密和产物扫描。
