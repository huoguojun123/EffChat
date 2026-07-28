# EffChat 架构文档

适用版本：pre-release 0.3.4

EffChat 是一个面向 2-5 人自托管的小团队 agent workbench。当前主线是 Web-first：React 前端、Go API、PostgreSQL、Python extractor sidecar，以及基于 Eino 的统一 Agent。

## 技术栈

- Backend：Go 1.26.5、Gin、Eino v0.9.13、eino-ext openai/claude/gemini adapters。
- Frontend：Vite、React 19、TypeScript、Tailwind CSS v4、Shadcn/Radix 基础组件、Zustand。
- Database：PostgreSQL 17。
- Extractor：Python sidecar，负责本地文档解析和 MinerU OCR 代理。
- Deploy：Docker Compose，包含 `postgres`、`migrate`、`backend`、`py-extractor`、`web`。

## 当前边界

- `.env.docker` 只保存基础设施配置：数据库、JWT、端口、数据目录、反代来源、内部服务地址和日志轮转。
- 模型渠道、模型 API key、搜索服务、网页提取服务和 MinerU OCR 都在管理员网页持久化配置。
- 不包含代码执行沙盒、Shell 工具、浏览器自动化、完整 RBAC、成本账单和 Skills marketplace。
- `message_format` / `schema_version` 仍保留兼容字段，但 0.3.4 运行时按当前 Eino `schema.Message` 主线处理，不提供 `/api/v2` 双协议运行时。

## 运行时组件

```text
Browser
  |
  | HTTP + SSE
  v
web (Nginx + React assets)
  |
  | /api/* internal proxy
  v
backend (Gin)
  |
  +-- PostgreSQL
  +-- py-extractor
  +-- model channels configured in DB
  +-- external services configured in DB
```

## 后端分层

- `cmd/server`：启动、配置加载、数据库连接、路由注册、CORS 和优雅停机。
- `internal/handler`：HTTP 参数解析、鉴权上下文读取、响应格式。
- `internal/service`：会话、模型、渠道、限额、文件和标题生成等业务编排；文件分段预览由共享 `file_preview_service` 负责 cursor 与 UTF-8 边界。
- `internal/repository`：PostgreSQL 查询和事务；全局对话检索位于独立 `conversation_search_repository`，不继续扩大消息主仓库。
- `internal/agent`：Eino ReAct Agent、模型适配、消息转换、工具挂载、发送前压缩和流式事件消费。
- `internal/tool`：现有工具实现，包括文件、Skills、memory、web_search、web_extract。
- `internal/extractor`：Go 后端访问 Python extractor 的客户端。
- `internal/usage`：消息、模型 token、工具、搜索、提取和 OCR 用量统计。

## 前端分层

- `src/components/chat`：聊天区、输入框、模型选择、文件抽屉和附件上传。
- `src/components/message`：用户/助手消息、Markdown、代码块、思考过程、工具调用树。
- `src/components/workspace`：HTML/SVG/Mermaid/Graphviz/思维导图预览器，以及保留但不再由普通入口触发的旧右侧工作区。
- `src/components/admin`：受权限保护的 `/admin/:section` 独立页面；按模型与服务、治理与用量、提示与知识、系统分组，按当前栏目懒加载数据。
- `src/stores`：Zustand 状态，包括认证、会话消息、模型列表、UI 状态和系统信息。
- `src/api`：REST API 封装和文件 blob 鉴权下载。
- `src/lib/sseProtocol.ts` / `runReconciliation.ts`：纯 SSE 帧解析、错误归一化和有界运行对账；`useSSE` 继续负责 React 生命周期、发送、停止、重试与 store 协调。

## 发送消息链路

1. 前端 `ChatInput` 使用受限自动增长的紧凑输入框；只保存每个会话的草稿文本，不持久化像素高度。
2. `useSSE.sendMessage` 调用 `POST /api/v1/sessions/:id/messages/stream`。
3. 后端在进入 Agent 前校验会话模型、渠道、用户组限额和并发 run。
4. `EinoAgent.StreamChat` 从 DB 实时解析渠道、模型能力、工具治理和外部服务配置。
5. Agent 通过 SSE 输出 `content_delta`、`thinking_delta`、`tool_call_start`、`tool_call_result`、`message_complete` 等事件。
6. 后端即使前端 SSE 断开，也尽量继续跑完当前 run 并落库。
7. 前端在重连、刷新或 run 完成后从 DB 同步真实消息，保留仍未落库的本地 pending 消息。

末轮用户消息在助手零输出或仅有错误提示时可编辑重试。后端不原地更新消息，而是在准入事务中创建新用户消息、复用原附件、软隐藏旧尾部并保留旧运行与用量事实；前端复用现有 SSE 和 `message_start` 完成新 ID 对账。

## 工具与联网

- 工具通过 Eino Tool interface 挂载，不做 MCP runtime。
- 管理后台可启停现有工具并配置超时；错误以结构化工具结果显示在消息内。
- 搜索链路由管理员为 Tavily、Brave、Exa、博查和 SearXNG 独立配置并排序；按顺序成功即停止。
- 网页提取链路由管理员为 Firecrawl、Jina、Tavily 和 Exa 独立配置并排序；Basic 固定为最后兜底。
- 工具调用日志不做持久化后台页面；排障依靠容器日志和用量统计。

## 文件与 OCR

- 文件元数据在 `files` 表，解析文本保存在受管理的数据目录。
- 图片保留原图；文档类文件不承诺长期保留原始 PDF/Word。
- PDF 当前策略是 MinerU 优先，本地 Python 解析兜底。
- MinerU 由管理员后台配置 Token、Base URL 和并发限制；结果只读取 Markdown 文本。
- OCR 未完成前文件不能发送进消息，但用户可以删除文件；删除后迟到 OCR 结果必须丢弃。
- 管理后台“清理遗留文件”只清理超过 cutoff、未被未删除消息引用、也不绑定活跃会话的文件。

## 数据库与迁移

- `backend/migrations/init.sql` 是全新库 schema 快照。
- `backend/migrations/production/*.sql` 是唯一生产增量链。
- Docker Compose 通过 `migrate` 一次性容器执行 production 链，并记录到 `schema_migrations`。
- 不保留测试数据迁移；用户、会话、消息、文件、Skills、字体等真实数据不应被迁移脚本清空。

## 管理后台

- 模型与服务：模型、渠道、搜索/提取/OCR 服务。
- 治理与用量：用量、用户组、用户、工具。用量支持今天、7 天、30 天快捷范围，以及最长 90 天的自定义日期范围；历史范围不改变“今日用户组限额”。
- 提示与知识：底层提示词、提示词库、Skills。
- 系统：实例状态、系统配置、字体、文件清理。

`/admin/status` 只展示当前部署容器可见的版本、build ref、schema、Go 运行时、cgroup 内存、受管存储、PostgreSQL 和文档提取器状态。依赖探测短超时且相互独立，单项失败仍返回其余状态；页面只在进入或手动刷新时请求。它不读取 Docker Socket、宿主机监控信息、环境变量、服务地址、密钥或绝对路径。

管理员保存渠道、模型、外部服务或工具配置后，只影响新请求；已经运行中的 SSE / Agent run 不会中途切换凭据。

## 发布入口

- 快速部署见 [Docker Compose 部署](docs/03-实施计划/Docker-Compose-部署.md)。
- 管理配置见 [管理员配置指南](docs/03-实施计划/管理员配置指南.md)。
- 公开导出见 [开源发布检查清单](docs/03-实施计划/开源发布检查清单.md)。
