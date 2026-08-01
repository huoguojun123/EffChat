# EffChat 架构文档

适用版本：pre-release 0.3.4

EffChat 是一个面向 2-5 人自托管的小团队 agent workbench。当前主线是 Web-first：React 前端、Go API、PostgreSQL、Python extractor sidecar，以及基于 Eino 的统一 Agent。

## 技术栈

- Backend：Go 1.26.5、Gin、Eino v0.9.13、eino-ext openai/claude/gemini adapters。Claude adapter v0.1.20 通过 `backend/third_party/eino-claude` 保留一处可审计的上游 race 修正，待官方发布等价修复后移除本地 replace。
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
5. Agent 通过 SSE 输出 `content_delta`、`thinking_delta`、`tool_call_start`、`tool_call_result`、`message_complete` 等事件。只有尚未产生任何有效输出的瞬时模型故障可以自动重试一次；一旦已有文本、thinking 或工具输出，后续传输故障不再重放 provider 调用，而是把现有回答作为 `incomplete` 结果收口。每次真实 provider 调用分别记录 usage，但同一个 run 只形成一条用户消息、一组回答消息和一个 selected answer attempt。Provider SDK 不得在该生命周期之下另行隐藏重试；Anthropic native 的 SDK 默认重试已在共享 HTTP transport 关闭，状态码与首包前连接故障统一回到 EffChat 的 retry、usage 和 SSE 所有权。只有正文、thinking/reasoning 或可执行工具调用属于部分输出；provider 的 role、usage、finish 等空 metadata 不得解除首包门禁，也不得在超时后落成空白 `incomplete` 助手消息。
6. 后端即使前端 SSE 断开，也尽量继续跑完当前 run 并落库。
7. terminal 决策形成后，RunHub 会冻结迟到输出和取消；瞬时数据库或连接故障使用独立的有界单次上下文重试同一原子提交，只有数据库返回 canonical terminal 后才向所有订阅者发布一次终态。进程在恢复期间退出时，启动 reconciliation 将遗留的 durable running run 转成可重试的 `server_restarted` 终态。
8. 前端在重连、刷新或 run 完成后从 DB 同步真实消息，保留仍未落库的本地 pending 消息。

末轮用户消息在助手零输出或仅有错误提示时可编辑重试。后端不原地更新消息，而是在准入事务中创建新用户消息、复用原附件、软隐藏旧尾部并保留旧运行与用量事实；前端复用现有 SSE 和 `message_start` 完成新 ID 对账。

会话压缩只改变 Agent 实际携带的上下文，不改变用户可回看的消息历史。完整加载、冷加载窗口、turn 索引和分页继续包含压缩前的 user/assistant/tool 消息；active checkpoint 作为逻辑 divider 插在它所压缩的最后一个 user turn 之后，并且只随包含该锚点的一个页面返回。刷新、重新登录和分页因此保持历史可见，而 Agent context 仍由独立的压缩上下文查询过滤已压缩消息并注入 checkpoint。撤销只撤销 checkpoint 对后续模型上下文的作用，不承担恢复 UI 可见性的职责。

会话详情、消息分页/turn/window、Markdown 导出，以及 Run/记忆入口的会话归属查询共享同一隐私边界：不存在与无权访问均公开为 `session_not_found`，但 PostgreSQL 和扫描故障必须保留到带 request ID 的 retryable 5xx，不能在 service 层压成 404。消息 window 的目标 turn 不存在使用独立 404；本地 cursor/limit/window 参数错误使用稳定 400。

会话 create/update/delete mutation 复用同一边界：受控字段、folder 和生成参数校验返回 `session_invalid` 400；初始归属查询与 update/delete 的 `RowsAffected == 0` 都把缺失或竞态删除收敛为 `session_not_found` 404。模型、渠道、默认模型和用户组读取必须区分“确实不存在/不可用”与 repository 故障；前者使用稳定模型业务码，运行依赖暂不可用为 retryable 503，数据库、扫描和事务故障为带 request ID 的 retryable 5xx，任何分支都不得把 wrapped error 原文写入 JSON。

send/preflight/retry/manual compaction 在 run accepted 前沿用相同公共错误契约：会话缺失为 404，账号失效为 401，消息输入为 400/413，retry 尾部竞态、已产生回答和附件失效为稳定 409；用户、Skill、历史、压缩任务状态、run reservation 和 SSE writer 的内部故障只返回带 request ID 的 retryable 5xx。manual compaction 一旦被 RunHub 接受，setup 阶段的会话/账号消失和 preserve target 竞态会写入同一 durable terminal 公共码，刷新和 replay 不会退化成内部错误原文。撤销压缩把缺失 checkpoint 归为 404，把非 manual 或已有新消息归为 409，repository/事务故障归为可追踪 5xx。

checkpoint 虽以 `role=user` 进入 Eino 消息序列，但它是系统生成的上下文治理记录，不是用户发送消息。每日消息配额、准入检查和 Admin 今日用量统一排除 `metadata.compaction_summary=true`；普通用户消息即使后来被 retry 或删除，仍按既有消费语义计数。

压缩模型是输出受限且结果会持久化为后续上下文的 utility consumer。它复用当前会话的模型和渠道，但在克隆的任务请求上关闭可选 thinking，避免 reasoning 抢占摘要预算；收流后、checkpoint 落库前还会复用主聊天的 inline `<think>` 分离边界。摘要抽取器同时容忍兼容网关把 opening `<analysis>` 移入 reasoning 字段、却在 content 中留下 orphan `</analysis>` 与 `<summary>` 包装的情况，防止隐藏推理进入 durable summary。原始会话请求保持不变，主聊天的 thinking 配置不受影响。

## 工具与联网

- 工具通过 Eino Tool interface 挂载，不做 MCP runtime。
- 管理后台可启停现有工具并配置超时；工具自身返回的受控业务失败保持结构化结果，Tool endpoint 抛出的内部错误只向模型和消息树公开稳定 `code/message/retryable`，原始 repository、路径或 provider 原因仅进入受控内部诊断。
- 搜索链路由管理员为 Tavily、Brave、Exa、博查和 SearXNG 独立配置并排序；按顺序成功即停止。
- 网页提取链路由管理员为 Firecrawl、Jina、Tavily 和 Exa 独立配置并排序；Basic 固定为最后兜底。
- 网页提炼复用统一的流式模型消费契约：固定时限只等待首个有效输出，首包后完整收流。任务请求显式关闭 DeepSeek V4 thinking，并在结果边界剥离仍被兼容网关写入正文流首的 `<think>` 块，避免隐藏推理占满工具正文预算；抓取成功但提炼不可用或正文仍需截断时返回带原因的 degraded 结果。
- 网页提炼开关与模型 ID 属于内容外发 policy：成功解析的值成为进程内 last-known-good snapshot；短暂查询/解析故障只复用该快照，冷启动无可信值时保守关闭二次提炼，不构造 utility model，也不把 crawler 正文发给模型。accepted runtime snapshot v4 固定实际生效策略、`ready` / `disabled` / `unavailable` 状态和已解析的模型/渠道依赖，执行阶段不重新读取 live config。
- 工具调用日志不做持久化后台页面；排障依靠容器日志和用量统计。

## 文件与 OCR

- 文件元数据在 `files` 表，解析文本保存在受管理的数据目录。
- 文件 list/download/preview/OCR refresh 的公共读取契约区分本地参数 400、用户域内缺失 404、解析未完成 409 与 repository/受管存储/sidecar 读取的带 request ID 5xx；缺失与无权访问保持不可区分。`ApiError` 保留后端 `code/retryable/request_id`，文件预览按稳定 code 展示缺失或暂无正文，并只在公共协议允许重试时提供重试入口，不再解析中英文错误字符串。列表扫描统一检查 `rows.Err()`，中途数据库故障不能伪装成部分成功结果。
- 文件上传准入与持久化复用同一公共错误协议：缺少 multipart/file/session 参数为稳定 400，文件与解析输出超限为 413，声明 MIME 不一致为 400，白名单或解析器不支持为 415，损坏/无可读正文为 422，会话文件数达到上限为 409；会话不存在或无权访问统一为 404，但 session/repository、受管存储、OCR queue 和 metadata 故障必须返回带 request ID 的 retryable 5xx。extractor owner 用 sentinel 区分用户内容、资源上限与依赖故障，Python sidecar 的响应正文、内部路径和上游原因不会传播到 Go 公共错误或 request log；metadata 创建失败会补偿删除本轮刚写入的原件、OCR buffer 或 extracted sidecar。
- 人工 OCR retry 在 mutation 前分别校验附件 policy、Go/Python runtime 依赖和 MinerU 渠道配置；管理员未启用返回稳定 409，控制面或 runtime 暂不可读返回带 request ID 的 retryable 503。文件不存在或无权访问统一为 404，repository mutation 故障为可追踪 5xx。`RestartOCR` 提交新的 pending generation 后才复核受管原件：过期或确实缺失会用 `FailOCR` 补偿闭合为 failed 并返回 409，越界路径、非缺失型文件系统错误或补偿失败返回稳定可追踪 5xx；只有复核成功才唤醒 recovery runner。
- 图片保留原图；文档类文件不承诺长期保留原始 PDF/Word。
- PDF 当前策略是 MinerU 优先，本地 Python 解析兜底。
- MinerU 由管理员后台配置 Token、Base URL 和并发限制；结果只读取 Markdown 文本。
- 暂存附件抽屉通过 Radix Dialog Portal 脱离聊天 composer 的 stacking context；模态层统一拥有 overlay、焦点约束、Escape 关闭和安全区，关闭后焦点返回实际触发入口。上传队列、附件选择与发送协议仍由原有 ChatInput 状态负责。
- 上传大小、会话文件数、MIME allowlist、附件提取开关、timeout 和输出上限共用 typed policy reader。长期安装缺少较新的配置行时采用公开配置 schema 的权威默认值并建立可信快照；查询失败或非法存储值不会被误判为缺失。已成功读取的严格值在暂时故障中继续生效并标记 `policy_degraded`；冷启动无可信值时上传/处理入口返回稳定 503，不恢复调用方宽默认。空 allowlist 和空元素在管理员写入边界直接拒绝。
- `attachment_extract_enabled=false` 同时约束新上传、OCR pending 和人工 retry：尚未提交的 pending 不读取原件、不占用 OCR quota、也不调用 inspect/`StartMinerUOCR`，而是保留状态并退避；人工 retry 在改变数据库状态前返回 `attachment_extract_disabled`。已经提交给远端的 task 属于不可撤回边界，仍可 poll 并在可信输出上限下收尾，但不会重新提交。
- OCR recovery claim 使用数据库单调 `ocr_lease_generation` 作为 fencing identity。attempt、quota reservation、task/progress、retry、fail、complete 和 source cleanup 都必须携带当前 generation；租约过期后旧 worker 的迟到 mutation 会稳定失败。解析结果先写 generation 独占临时 sidecar，repository 在行锁下再次验证 owner 后才以同文件系统原子 rename 发布正式结果；旧 worker 和失败补偿只清理自己的临时文件，不删除共享正式 sidecar。
- OCR 未完成前文件不能发送进消息，但用户可以删除文件；删除后迟到 OCR 结果必须丢弃。
- 管理后台“清理遗留文件”只清理超过 cutoff、未被未删除消息引用、也不绑定活跃会话的文件。

## 单会话记忆

- 运行时只维护每个会话一张固定 section 的结构化记忆卡；字符硬上限由 `memory_max_chars` 和存储层共同校验，不扩展为跨会话画像、RAG 或独立 timeline。
- 自动维护、手动重试和整理复用当前会话的 provider、model 与渠道，但拥有独立的结构化输出预算，并关闭可选 thinking。这样不会让 reasoning token 抢占记忆正文容量，也不会让 Anthropic manual thinking 把任务上限扩到契约之外。
- 输出预算是跨 provider 的保守容量契约，不是 tokenizer 精确换算。它按“字符上限 + 4,096 JSON/结构余量”向上取整到 4,096 token：4K/8K/12K/16K 字符分别需要 8,192/12,288/16,384/20,480 输出 token。
- 已知 `ModelMaxOutput` 小于所需预算时，后端在调用 provider 前失败并保留原记忆；能力元数据为 `0` 时表示未知，后端仍申请完整任务预算，不凭空猜测模型上限。
- 记忆维护只走完整流式消费。provider 以 `length`、`max_tokens`、`max_output_tokens` 等原因达到输出上限时，本次结果一律不解析、不保存，并在 `model_task_runs` 记录 `memory_output_limit`；能力不足记录 `memory_output_budget_insufficient`。
- 用户手动整理和重试使用独立的 durable `memory_maintenance` RunHub run 与 SSE 观察链。固定 timeout 只守护首个有效模型输出；浏览器断线、关闭弹窗或刷新不会取消已经 accepted 的任务，重新打开弹窗会从活动 run 的游标继续观察。
- 手动维护先生成并校验候选记忆，再在同一 PostgreSQL 事务中提交 durable terminal、记忆 CAS、`session_memory_changes` 和 `model_task_runs`。`session_memory_changes.run_id` 使 ambiguous terminal commit 的恢复重试不会重复写入记忆历史。
- 记忆 REST 与维护任务的 HTTP 准入遵循公共错误契约：内容或 change id 校验为 400，缺失 change 为 404，CAS/不可撤销状态为 409，不可用维护服务为 503；memory repository、响应重建和 stream 初始化故障为带 request ID 的 retryable 5xx。任务一旦进入 RunHub，后续失败继续由既有 SSE/terminal 公共事件收口，原始错误只进入内部 task run 与日志。

## 数据库与迁移

- `backend/migrations/production/*.sql` 是 fresh install 与已有库升级共用的唯一生产链；`init.sql` 只是 001 引用的不可变历史 schema 基线，不能独立启动应用。
- Compose migrate 与 `init_db.sh` 共用 `build_migration_script.sh`：同一 session advisory lock 串行完整链，每条 migration SQL 与 checksum 账本行在同一事务提交。
- 已知 legacy/空 checksum 只做精确一次性 reconcile，未知漂移拒绝启动；后端 schema gate 要求最新 production migration 已登记。
- 不保留测试数据迁移；用户、会话、消息、文件、Skills、字体等真实数据不应被迁移脚本清空。

## 管理后台

- 模型与服务：模型、渠道、搜索/提取/OCR 服务。
- 治理与用量：用量、用户组、用户、工具。用量支持今天、7 天、30 天快捷范围，以及最长 90 天的自定义日期范围；历史范围不改变“今日用户组限额”。
- 提示与知识：底层提示词、提示词库、Skills。
- 系统：实例状态、系统配置、字体、文件清理。

`/admin/status` 只展示当前部署容器可见的版本、build ref、schema、Go 运行时、cgroup 内存、受管存储、PostgreSQL 和文档提取器状态。依赖探测短超时且相互独立，单项失败仍返回其余状态；页面只在进入或手动刷新时请求。它不读取 Docker Socket、宿主机监控信息、环境变量、服务地址、密钥或绝对路径。

管理员保存渠道、模型、外部服务或工具配置后，只影响新请求；已经运行中的 SSE / Agent run 不会中途切换凭据。

模型、渠道、外部服务、系统配置与 Tool 配置的普通 JSON 失败使用稳定的 `error`、`code`、`retryable` 契约。纯本地字段、模板或排序校验返回可修改的 400，资源不存在返回 404，模型重复创建返回 409，外部服务 probe 失败返回 502；repository、registry 和其他内部故障返回带 request ID 的 5xx。默认模型校验也必须保留数据库/渠道读取故障的内部分类，不能把运行故障伪装成“模型不存在”。只有受控本地校验文案可以公开，SQL、内部路径、provider 原文和凭据化 URL 只保留在服务端诊断。

## 发布入口

- 快速部署见 [Docker Compose 部署](docs/03-实施计划/Docker-Compose-部署.md)。
- 管理配置见 [管理员配置指南](docs/03-实施计划/管理员配置指南.md)。
- 公开导出见 [开源发布检查清单](docs/03-实施计划/开源发布检查清单.md)。
