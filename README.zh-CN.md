# EffChat

<p align="center">
  <img src="frontend/public/pwa-512x512.png" width="104" alt="EffChat logo">
</p>

<p align="center">
  <strong>一个安静、自托管的 AI 对话工作台，让模型、文件、会话和运行数据都由你掌握。</strong>
</p>

<p align="center">
  <strong>简体中文</strong> · <a href="README.md">English</a>
</p>

<p align="center">
  <a href="https://github.com/huoguojun123/EffChat/actions/workflows/ci.yml"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/huoguojun123/EffChat/ci.yml?branch=main&label=CI"></a>
  <a href="https://github.com/huoguojun123/EffChat/releases"><img alt="Release" src="https://img.shields.io/github/v/release/huoguojun123/EffChat?include_prereleases&sort=semver"></a>
  <a href="https://hub.docker.com/r/gjhuo/effchat"><img alt="Docker pulls" src="https://img.shields.io/docker/pulls/gjhuo/effchat"></a>
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/badge/license-Apache--2.0-blue"></a>
</p>

> [!WARNING]
> EffChat 目前处于 beta 阶段。升级前请先备份数据；预发布版本可能调整 migration、配置兼容性或公开 API。

## 为什么是 EffChat

EffChat 把 AI 工作台中真正常用的部分放在一起，同时保持个人部署可以理解和维护。你可以在自己掌控的基础设施上使用对话、持久化运行、可编辑记忆、受治理的工具和本地文件管线。

## 功能亮点

- 💬 **可靠对话** —— 浏览器断开后运行仍可继续，刷新后可以恢复。
- 🔁 **回答版本** —— 比较、选择、重试或删除同一轮中的多个回答。
- 🧠 **单会话记忆** —— 记忆属于当前会话，可查看、修改、撤销。
- 🗜️ **上下文压缩** —— 减少模型看到的上下文，但不删除你仍可阅读的历史记录。
- 📎 **文档理解** —— 在受管文件流程中提取 PDF、Office、Markdown、文本、表格和图片。
- 🧰 **Tools 与 Skills** —— 让 Agent 搜索、读取文件、提取网页，并展示清晰的运行状态。
- 🌐 **联网搜索与网页提取** —— 独立配置 Tavily、Brave、Exa、博查、SearXNG、Firecrawl、Jina 或本地 Basic 兜底。
- 🤖 **多协议模型** —— 支持 OpenAI-compatible、OpenAI Responses、Anthropic native 和 Google native 渠道。
- 🛡️ **管理员治理** —— 在一个后台管理模型、服务、配额、用户组、文件、字体、Tools、Skills 和用量。
- 📱 **桌面端与 PWA** —— 响应式对话、可安装应用壳、明暗主题、Markdown、数学公式、Mermaid 和代码预览。

## 界面预览

<p align="center">
  <a href="docs/assets/screenshots/chat-workspace.png"><img src="docs/assets/screenshots/chat-workspace.png" alt="EffChat 对话工作台" width="1080"></a>
</p>
<p align="center"><sub>对话工作台：流式回答、Markdown、Mermaid、工具调用和会话组织。</sub></p>

<p align="center">
  <a href="docs/assets/screenshots/file-and-tools.png"><img src="docs/assets/screenshots/file-and-tools.png" alt="EffChat 文件与工具" width="560"></a>
</p>
<p align="center"><sub>文件与工具在同一条对话中协同工作。</sub></p>

<p align="center">
  <a href="docs/assets/screenshots/admin-settings.png"><img src="docs/assets/screenshots/admin-settings.png" alt="EffChat 管理后台" width="1080"></a>
</p>
<p align="center"><sub>在管理后台配置模型渠道、联网服务和连接检查。</sub></p>

截图使用演示内容或脱敏设置，点击图片可以查看原始尺寸。

## 快速开始

### 交互式安装

适合个人部署。安装脚本会询问目录、Web 端口和 PostgreSQL 来源，然后生成一个私有 `.env` 和一个 Compose 文件。再次运行并选择更新时，会保留已识别 EffChat 部署中的数据和凭据。

```bash
curl -fsSL https://raw.githubusercontent.com/huoguojun123/EffChat/main/scripts/install.sh | bash
```

### 使用已发布镜像的 Docker Compose

适合服务器或家庭实验室。`web`、`backend`、`py-extractor` 和一次性 `migrate` 共用一个 EffChat 应用镜像，数据库使用官方 PostgreSQL 镜像。

```bash
git clone https://github.com/huoguojun123/EffChat.git
cd EffChat
cp .env.docker.example .env.docker
# 至少设置 POSTGRES_PASSWORD 和 JWT_SECRET
docker compose --env-file .env.docker -f docker-compose.registry.yml pull
docker compose --env-file .env.docker -f docker-compose.registry.yml up -d
```

打开 `http://localhost:8088`。全新实例注册的首个账号会成为不可转让的超级管理员。

### 从源码构建

```bash
git clone https://github.com/huoguojun123/EffChat.git
cd EffChat
cp .env.docker.example .env.docker
scripts/docker-build.sh up
```

本地开发和部署使用相同的 `.env.docker`、数据目录与 PostgreSQL 契约。升级、备份、恢复、反向代理和外部 PostgreSQL 见 [Docker Compose 部署](docs/deployment.md)。

## 配置你的实例

EffChat 自带应用和本地文档管线，但不内置模型额度或商业服务额度。进入管理后台，按需要配置：

- **模型：** 协议、Base URL、API Key、模型标识和能力。
- **联网搜索：** Tavily、Brave、Exa、博查的自有 Key，或自部署的 SearXNG JSON 地址。
- **网页提取：** Firecrawl、Jina Reader、Tavily Extract、Exa Extract，以及无需凭据的 Basic 兜底。
- **文档：** PDF、DOCX、PPTX、XLSX、CSV、Markdown、文本和图片默认走本地流程；MinerU OCR 可选。
- **治理：** Tools、Skills、配额、用户组、文件限制、记忆容量、字体和用量。

凭据应保存在管理后台或私有环境文件中，不要写入提交、截图、Issue 或示例配置。完整字段说明见 [管理员配置](docs/administration.md)。

## 文档

| 文档 | 适用场景 |
| --- | --- |
| [部署](docs/deployment.md) | 安装、更新、备份、恢复和 Compose 运维 |
| [管理员配置](docs/administration.md) | 模型、搜索、提取、文件、Tools、Skills、配额和字体 |
| [架构](ARCHITECTURE.md) | Agent、流式恢复、文件、记忆、数据库和运行边界 |
| [贡献指南](CONTRIBUTING.md) | 本地开发、测试和 Pull Request |
| [变更记录](CHANGELOG.md) | 版本变化和兼容性说明 |
| [安全策略](SECURITY.md) | 私密漏洞报告 |
| [第三方声明](THIRD_PARTY_NOTICES.md) | 依赖、字体、提示词、图片和容器许可证 |

## 社区

- [Linux.do](https://linux.do)
- [GitHub Issues](https://github.com/huoguojun123/EffChat/issues)
- [安全公告](https://github.com/huoguojun123/EffChat/security/advisories/new)

## 贡献

欢迎提交 Bug、文档改进和聚焦明确的 Pull Request。请先阅读 [贡献指南](CONTRIBUTING.md)。Issue、测试和截图中不要包含真实凭据、私有部署细节、生产日志或用户数据。

## 许可证

EffChat 源码采用 [Apache License 2.0](LICENSE)。第三方组件、字体、提示词、图标、截图、基础镜像和商标仍适用各自条款，详见 [NOTICE](NOTICE) 与 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
