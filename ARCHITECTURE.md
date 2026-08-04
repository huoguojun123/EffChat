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

模型渠道的 provider、模型家族与 wire protocol 分开解析。`openai_compatible` 继续走 Chat Completions 兼容协议；`openai_responses` 通过官方 Eino Responses 组件接入 `/v1/responses`。主对话保留 `schema.AgenticMessage` 到 Eino `NewTypedChatModelAgent`，由稳定版 Eino 的 typed ReAct graph 下发本地 Tool schema、执行 Tool 并回送 function result；仅在 Agent 事件出口转换为 EffChat 既有 SSE、RunHub、usage 和 PostgreSQL 消息契约。标题、probe、压缩、记忆维护和网页提炼等不执行本地 Tool 的 utility 调用仍可使用经典消息 adapter，不复制 ReAct、SSE parser 或第二套运行时。Responses 请求固定 `store=false`，不使用 `previous_response_id`、Conversations API、hosted tools 或 MCP runtime。

末轮用户消息在助手零输出或仅有错误提示时可编辑重试。后端不原地更新消息，而是在准入事务中创建新用户消息、复用原附件、软隐藏旧尾部并保留旧运行与用量事实；前端复用现有 SSE 和 `message_start` 完成新 ID 对账。

会话压缩只改变 Agent 实际携带的上下文，不改变用户可回看的消息历史。完整加载、冷加载窗口、turn 索引和分页继续包含压缩前的 user/assistant/tool 消息；active checkpoint 作为逻辑 divider 插在它所压缩的最后一个 user turn 之后，并且只随包含该锚点的一个页面返回。刷新、重新登录和分页因此保持历史可见，而 Agent context 仍由独立的压缩上下文查询过滤已压缩消息并注入 checkpoint。撤销只撤销 checkpoint 对后续模型上下文的作用，不承担恢复 UI 可见性的职责。

会话详情、消息分页/turn/window、Markdown 导出，以及 Run/记忆入口的会话归属查询共享同一隐私边界：不存在与无权访问均公开为 `session_not_found`，但 PostgreSQL 和扫描故障必须保留到带 request ID 的 retryable 5xx，不能在 service 层压成 404。消息 window 的目标 turn 不存在使用独立 404；本地 cursor/limit/window 参数错误使用稳定 400。

会话列表的 `limit/offset/folder_id` 同样是显式查询契约：limit 只接受 1–100，offset 必须非负，folder scope 只接受 `all`、`unfiled` 或正整数 ID；非法值统一返回 `session_list_query_invalid` 400，不静默回退默认分页。该校验不改变 `has_more/next_offset`、置顶排序、folder 归属或前端加载更多行为，repository 故障仍使用带 request ID 的 retryable `session_list_failed` 5xx。

全局对话检索只接受 2–120 字符 query、`all/unfiled/folder` scope、folder scope 的正整数 ID 和 1–50 的 limit；非法值统一返回 `conversation_search_query_invalid` 400，不再以裸 error 或默认 limit 隐藏错误。检索仍只读取当前用户未删除会话及可见 selected answer，repository query/scan/iteration 故障统一为带 request ID 的 retryable `conversation_search_failed` 5xx；前端既有 debounce、迟到响应 owner 和结果跳转行为不变。

Run active/status/resume/cancel 入口先复用会话归属边界，再对 run id 和 replay cursor 做稳定 400 校验。durable reservation 或 RunHub 中的 run 缺失、跨会话或跨用户都收敛为 `run_not_found` 404，quota repository 和 stream writer 故障为带 request ID 的 retryable 5xx。RunHub 使用 typed not-found sentinel 仅支撑 HTTP 分类；SSE replay/gap、多订阅、terminal snapshot、停止幂等和 durable-first 生命周期不变。

accepted run 的 HTTP fallback 与 durable terminal 使用同一公共错误 payload builder。取消或 setup-timeout 的 terminal 持久化失败、以及 execution owner 建立后 SSE writer 无法打开时，响应必须保留 request ID；后者还返回 run ID，提示客户端稍后恢复。该关联信息只用于追踪和恢复，不改变后台 worker 继续执行、terminal 重试、replay 或 exactly-once 所有权。

run admission 已发现 durable terminal，或 BeginExecution 已被另一 observer/worker 占有但当前连接又无法建立 SSE 时，409 fallback 同样保留 request ID、稳定 `retryable=false`，execution-owned 还返回原 run ID。客户端应刷新或按 run ID 恢复，不能通过重试 admission 创建第二个执行 owner；reservation、replay 和 terminal 事实不变。

accepted worker 建立后首次 RunHub 订阅失败的 SSE error 保留 request ID、run ID 和 `retryable=true`，提示稍后恢复；既有 run replay 发生 scope/not-found 时保留同样关联字段并使用 `retryable=false`。内部 RunHub cause 只进入日志，不能进入 SSE；这两条旁路不取消 worker、不重放 provider，也不改变 durable terminal。

回答版本切换复用会话归属与 answer attempt 事务边界：无效 ID 为 400，会话或 attempt 缺失为 404，目标不属于最后一轮或没有可选择输出为 409，repository/transaction 故障为带 request ID 的 retryable 5xx。该公共错误契约不改变选择 revision、可见消息导航或选择成功后的单会话记忆重整行为。

会话 create/update/delete mutation 复用同一边界：受控字段、folder 和生成参数校验返回 `session_invalid` 400；初始归属查询与 update/delete 的 `RowsAffected == 0` 都把缺失或竞态删除收敛为 `session_not_found` 404。模型、渠道、默认模型和用户组读取必须区分“确实不存在/不可用”与 repository 故障；前者使用稳定模型业务码，运行依赖暂不可用为 retryable 503，数据库、扫描和事务故障为带 request ID 的 retryable 5xx，任何分支都不得把 wrapped error 原文写入 JSON。

空请求创建会话只使用管理员配置的全局默认模型，不按目录排序猜测模型。`GET /sessions/readiness` 复用与真实创建相同的默认模型、用户权限和渠道可运行校验：配置缺失或安全的模型状态返回 `200 + ready=false` 与稳定 code，repository 故障仍是可追踪 5xx。前端在 readiness 未知或 blocked 时不发送空创建请求；侧栏与空会话入口共享一个 busy/error owner，重复点击不会产生并发创建。系统尚无 runnable public 模型时允许空默认作为 bootstrap 状态；一旦存在可运行的公共模型，Admin 清空 `default_model_id` 会在写事务前被拒绝并保留旧值。

前端消息正文只维护一个当前窗口 generation。切换会话、账户 reset、latest/full reload、around 跳转和显式窗口替换都会先递增 generation、失效旧分页并释放 loading；older/newer 同一时刻只有一个方向拥有分页请求，响应还必须匹配 session、generation、方向 owner 和原 cursor 才能合并。RunHub/SSE terminal 对账捕获同一 generation，历史窗口不会被迟到 durable sync 改写；full reload 替换 durable 行，只保留尚待服务端对应记录接管的本地 optimistic 消息。滚动锚点同样绑定 generation，窗口替换后旧 observer 或 timeout 不能继续调整 scrollTop。

会话文件夹 list/create/update/delete 使用独立的资源边界：ID 与名称校验为稳定 400，不存在、无权访问或 mutation rows-affected 竞态统一为 `session_folder_not_found` 404，repository 查询、扫描和写入故障为带 request ID 的 retryable 5xx。列表必须在返回前检查 `rows.Err()`，不能把中途数据库故障伪装成部分成功；该公共错误契约不改变复合 PATCH 字段的更新语义。

send/preflight/retry/manual compaction 在 run accepted 前沿用相同公共错误契约：会话缺失为 404，账号失效为 401，消息输入为 400/413，retry 尾部竞态、已产生回答和附件失效为稳定 409；用户、Skill、历史、压缩任务状态、run reservation 和 SSE writer 的内部故障只返回带 request ID 的 retryable 5xx。manual compaction 一旦被 RunHub 接受，setup 阶段的会话/账号消失和 preserve target 竞态会写入同一 durable terminal 公共码，刷新和 replay 不会退化成内部错误原文。撤销压缩把缺失 checkpoint 归为 404，把非 manual 或已有新消息归为 409，repository/事务故障归为可追踪 5xx。

checkpoint 虽以 `role=user` 进入 Eino 消息序列，但它是系统生成的上下文治理记录，不是用户发送消息。每日消息配额、准入检查和 Admin 今日用量统一排除 `metadata.compaction_summary=true`；普通用户消息即使后来被 retry 或删除，仍按既有消费语义计数。

压缩模型是输出受限且结果会持久化为后续上下文的 utility consumer。它复用当前会话的模型和渠道，但在克隆的任务请求上关闭可选 thinking，避免 reasoning 抢占摘要预算；收流后、checkpoint 落库前还会复用主聊天的 inline `<think>` 分离边界。摘要抽取器同时容忍兼容网关把 opening `<analysis>` 移入 reasoning 字段、却在 content 中留下 orphan `</analysis>` 与 `<summary>` 包装的情况，防止隐藏推理进入 durable summary。原始会话请求保持不变，主聊天的 thinking 配置不受影响。

## 工具与联网

- 工具通过 Eino Tool interface 挂载，不做 MCP runtime。
- 管理后台可启停现有工具并配置超时；工具自身返回的受控业务失败保持结构化结果。repository、持久化或受管文件 I/O 等内部失败必须作为 wrapped Go error 交给 Tool governance，不能伪装成成功 envelope 内的 `error` 字段；治理边界也会把带显式公共 `message` 的结构化失败改写为该公共文案。模型、RunHub 和消息树只接收稳定失败，原始 repository、路径或 provider 原因仅进入受控内部诊断；已有 `error_code` 的网页 typed 失败继续由网页工具自己的分类器负责。
- 搜索链路由管理员为 Tavily、Brave、Exa、博查和 SearXNG 独立配置并排序；按顺序成功即停止。
- 网页提取链路由管理员为 Firecrawl、Jina、Tavily 和 Exa 独立配置并排序；Basic 固定为最后兜底。
- 网页提炼复用统一的流式模型消费契约：固定时限只等待首个有效输出，首包后完整收流。任务请求显式关闭 DeepSeek V4 thinking，并在结果边界剥离仍被兼容网关写入正文流首的 `<think>` 块，避免隐藏推理占满工具正文预算；抓取成功但提炼不可用或正文仍需截断时返回带原因的 degraded 结果。
- 网页提炼开关与模型 ID 属于内容外发 policy：成功解析的值成为进程内 last-known-good snapshot；短暂查询/解析故障只复用该快照，冷启动无可信值时保守关闭二次提炼，不构造 utility model，也不把 crawler 正文发给模型。accepted runtime snapshot v4 固定实际生效策略、`ready` / `disabled` / `unavailable` 状态和已解析的模型/渠道依赖，执行阶段不重新读取 live config。
- 工具调用日志不做持久化后台页面；排障依靠容器日志和用量统计。

## 文件与 OCR

- 文件元数据在 `files` 表，解析文本保存在受管理的数据目录。
- 文件 list/download/preview/OCR refresh 的公共读取契约区分本地参数 400、用户域内缺失 404、解析未完成 409 与 repository/受管存储/sidecar 读取的带 request ID 5xx；缺失与无权访问保持不可区分。`ApiError` 保留后端 `code/retryable/request_id`，文件预览按稳定 code 展示缺失或暂无正文，并只在公共协议允许重试时提供重试入口，不再解析中英文错误字符串。列表扫描统一检查 `rows.Err()`，中途数据库故障不能伪装成部分成功结果。
- 文件上传准入与持久化复用同一公共错误协议：缺少 multipart/file/session 参数为稳定 400，文件与解析输出超限为 413，声明 MIME 不一致为 400，白名单或解析器不支持为 415，损坏/无可读正文为 422，会话文件数达到上限为 409；会话不存在或无权访问统一为 404，但 session/repository、受管存储、OCR queue 和 metadata 故障必须返回带 request ID 的 retryable 5xx。extractor owner 用 sentinel 区分用户内容、资源上限与依赖故障，Python sidecar 的响应正文、内部路径和上游原因不会传播到 Go 公共错误或 request log；metadata 创建失败会补偿删除本轮刚写入的原件、OCR buffer 或 extracted sidecar。
- 前端上传预校验的 limits 接口与真实上传入口读取同一个 fail-closed policy；策略在冷启动不可用时二者都返回 `file_policy_unavailable`、带 request ID 的 retryable 503，不能由只读入口退化为无关联信息的裸错误。last-known-good、degraded 标记和实际上传最终裁决保持不变。
- 人工 OCR retry 在 mutation 前分别校验附件 policy、Go/Python runtime 依赖和 MinerU 渠道配置；管理员未启用返回稳定 409，控制面或 runtime 暂不可读返回带 request ID 的 retryable 503。文件不存在或无权访问统一为 404，repository mutation 故障为可追踪 5xx。`RestartOCR` 提交新的 pending generation 后才复核受管原件：过期或确实缺失会用 `FailOCR` 补偿闭合为 failed 并返回 409，越界路径、非缺失型文件系统错误或补偿失败返回稳定可追踪 5xx；只有复核成功才唤醒 recovery runner。
- 用户删除附件只先提交数据库 tombstone/cleanup claim，不在请求内删除受管字节。无效参数为 400，缺失或无权访问统一为 404，不可用生命周期状态为 409；lookup、受管路径校验和删除事务故障返回带 request ID 的 retryable 5xx。删除事务继续负责 fencing OCR worker，并在 formal attachment 上同步写入历史消息 tombstone；物理清理由管理员维护入口按租约完成。
- 管理员批量 cleanup 在任何 claim 前先完成只读统计，随后分别过期 OCR source、按 lease claim 文件、删除受管字节并用 claim token finalize；单文件失败不会中止同批其他文件。顶层参数错误为 400，repository 阶段故障为带 request ID 的 retryable 5xx；200 部分成功响应的每项失败都包含稳定 `code/error/retryable`，并在存在失败时携带 request ID。物理删除或 finalize 失败会尝试立即释放 claim；释放本身失败使用独立 code，避免把延迟重试的 lease 状态隐藏在泛化错误中。
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
- 所有记忆写入共用后端高置信 credential guard：明确的 password/token/API key/Authorization 赋值、已知 token 前缀、带密码的数据库 URL 和 private-key block 在事务前被拒绝，错误不回显原值；普通 UUID、commit SHA、项目编号和数字标识不按 secret 处理。`do_not_remember` 只保存类别。旧版本已落库内容在 Agent、压缩/整理模型、memory Tool view、change history 与 undo 边界统一脱敏，避免继续上送或复制精确值。
- 记忆正文与启停设置是两个独立 mutation：正文保存继续通过 memory PUT 原子提交 sections、enabled 与 `expected_updated_at`，并写入可撤销的 change history；checkbox 只通过 session PATCH 更新 `memory_enabled`，不得携带当前编辑草稿、创建 memory change 或推进正文 CAS baseline。前端在成功后只接受 enabled，保留 dirty sections 和原 `updated_at`，因此后台并发更新仍会在之后显式保存时触发现有 409 冲突；启停失败则保留旧开关和草稿。
- 自动维护、手动重试和整理复用当前会话的 provider、model 与渠道，但拥有独立的结构化输出预算，并关闭可选 thinking。这样不会让 reasoning token 抢占记忆正文容量，也不会让 Anthropic manual thinking 把任务上限扩到契约之外。
- 输出预算是跨 provider 的保守容量契约，不是 tokenizer 精确换算。它按“字符上限 + 4,096 JSON/结构余量”向上取整到 4,096 token：4K/8K/12K/16K 字符分别需要 8,192/12,288/16,384/20,480 输出 token。
- 已知 `ModelMaxOutput` 小于所需预算时，后端在调用 provider 前失败并保留原记忆；能力元数据为 `0` 时表示未知，后端仍申请完整任务预算，不凭空猜测模型上限。
- 记忆维护只走完整流式消费。provider 以 `length`、`max_tokens`、`max_output_tokens` 等原因达到输出上限时，本次结果一律不解析、不保存，并在 `model_task_runs` 记录 `memory_output_limit`；能力不足记录 `memory_output_budget_insufficient`。
- 用户手动整理和重试使用独立的 durable `memory_maintenance` RunHub run 与 SSE 观察链。固定 timeout 只守护首个有效模型输出；浏览器断线、关闭弹窗或刷新不会取消已经 accepted 的任务，重新打开弹窗会从活动 run 的游标继续观察。
- 手动维护先生成并校验候选记忆，再在同一 PostgreSQL 事务中提交 durable terminal、记忆 CAS、`session_memory_changes` 和 `model_task_runs`。`session_memory_changes.run_id` 使 ambiguous terminal commit 的恢复重试不会重复写入记忆历史。
- 记忆 REST 与维护任务的 HTTP 准入遵循公共错误契约：内容或 change id 校验为 400，缺失 change 为 404，CAS/不可撤销状态为 409，不可用维护服务为 503；memory repository、响应重建和 stream 初始化故障为带 request ID 的 retryable 5xx。任务一旦进入 RunHub，后续失败继续由既有 SSE/terminal 公共事件收口，原始错误只进入内部 task run 与日志。
- 记忆 GET/PUT/undo、记忆维护与回答版本切换共享同一个 session ID 解析边界；非正整数统一返回 `session_id_invalid` 400、`retryable=false`，不能因入口不同退化为裸错误。该共享校验不改变会话所有权查询、记忆 CAS、RunHub 维护任务或回答选择事务。
- 记忆弹窗的异步读取和 mutation 绑定局部 `{sessionId, dialogGeneration, operationId}` owner。打开、关闭、切换会话或重新加载会推进 generation；同一窗口内的新操作递增 operation ID。迟到的 load/save/compact/retry/undo success、error、finally 和 quiet reload 只能更新仍拥有该 owner 的窗口，不能覆盖另一会话的 sections、changes、task runs、pending、success/error 或 saving。`onEnabledChange` 与 `onSeenChange` 显式携带请求 session ID，不能在响应到达时读取当前活动会话；后端现有 memory CAS、RunHub terminal 和草稿基线不因前端竞态修复而改变。

## 数据库与迁移

- `backend/migrations/production/*.sql` 是 fresh install 与已有库升级共用的唯一生产链；`init.sql` 只是 001 引用的不可变历史 schema 基线，不能独立启动应用。
- Compose migrate 与 `init_db.sh` 共用 `build_migration_script.sh`：同一 session advisory lock 串行完整链，每条 migration SQL 与 checksum 账本行在同一事务提交。
- 已知 legacy/空 checksum 只做精确一次性 reconcile，未知漂移拒绝启动；后端 schema gate 要求最新 production migration 已登记。
- 不保留测试数据迁移；用户、会话、消息、文件、Skills、字体等真实数据不应被迁移脚本清空。

## 管理后台

- 模型与服务：模型、渠道、搜索/提取/OCR 服务。
- 治理与用量：用量、用户组、用户、工具。用量支持今天、7 天、30 天快捷范围，以及最长 90 天的自定义日期范围；历史范围不改变“今日用户组限额”。
- Admin Usage 查询只接受空值默认 7 天、`today/7d/30d` 预设，或成对的 RFC3339 `start_at/end_at` 自定义范围；非法组合、日期和超过 90 天的窗口统一返回 `invalid_usage_range` 400 且不可重试，不再静默回退到 7 天。repository 聚合故障继续使用带 request ID 的 retryable `usage_summary_failed` 5xx；该 HTTP 契约不改变 OCR 事件时间口径或前端 query generation owner。
- 提示与知识：底层提示词、提示词库、Skills。
- 系统：实例状态、系统配置、字体、文件清理。

管理员模型 probe 保持 `200 + ok=false` 的状态型失败协议，避免单个模型检测失败升级为管理后台全局异常；请求字段缺失为稳定 400，probe runtime 缺失为带 request ID 的 retryable 503。setup 和 provider stream 故障只返回稳定 code、retryable 与经 Agent 分类的安全 message/diagnostic，原始渠道、数据库、URL、密钥、响应正文和路径只进入内部日志。probe 只验证最小文本连通性，并且只有完整收流后的正文在去除首尾空白后精确等于 `OK` 才返回 `ok=true`；nil、空文本、`NOT OK` 或任何附加文本都以 `model_probe_unexpected_output` 返回 `200 + ok=false`，不能把“上游返回了某些内容”误报为模型可用。

models.dev 单模型目录查询对空 ID 返回 `models_dev_model_invalid` 400，对缺失 provider 或 model 返回稳定 404 code，均携带 `retryable=false` 且不回显用户输入；目录获取失败继续为带 request ID 的 retryable 502。该公共错误契约不改变缓存、目录导入、能力来源、退役标记或候选日 freshness 门禁，后者仍由模型目录生命周期独立负责。

模型目录的持久事实仍由 PostgreSQL `models` 表拥有；backend 启动会用该表整体替换进程内 registry，空表也不会回退到编译期内置型号。migration `046_model_catalog_lifecycle.sql` 为每条管理员模型记录增加 `catalog_source`、`catalog_checked_at` 和 `lifecycle_status`：既有与普通手工创建记录默认是 `manual` / `unknown`，渠道 `/models` 与 models.dev 只产生默认 disabled 的候选并携带各自来源及核对时间。目录中出现一个 ID 不等于账号可运行，因此除显式 preview/experimental 标记外不从名称猜测 active/deprecated/retired；管理员记录不会因目录消失而被删除。Admin 模型列表和编辑器显示来源、核对时间与 lifecycle；从渠道导入或应用 models.dev 能力时保留对应来源，管理员手工修改 capability/lifecycle 时切换为 `manual` 并清除旧目录核对时间，显示名称、可见组和排序等非能力字段不改变来源。migration `047_model_temperature_profile.sql` 以 `configurable`、`omit`、`fixed` 三种 typed policy 持久化 temperature 请求契约；固定策略要求 0–2 的显式值，其他策略禁止残留值。migration `048_model_openai_request_profile.sql` 进一步为 OpenAI-compatible 模型提供可空且有范围约束的 `top_p`、`n`、`presence_penalty`、`frequency_penalty` 固定值；空值代表完全省略字段，Admin 不接受任意 provider 参数 JSON。统一 ChatModel 构造在 accepted runtime snapshot 中固化这些模型级请求约束：temperature 的 `configurable` 保留会话值，`omit` 传 `nil`，`fixed` 使用管理员值；OpenAI-compatible typed profile 只在对应 adapter 边界映射为 wire 字段。主对话、压缩、记忆维护、网页 refinement 与最小 probe 复用同一解析，不修改会话中保存的用户偏好。发布候选日只在官方目录与受控 models.dev 同时给出一致 ID/capability 时更新编译期保守兜底；该更新仍不 seed 数据库、不自动启用或替换管理员模型。

Skill 的用户可见列表、会话启用、管理员 CRUD、文件读取、导入预览和 Git/Zip 更新共享领域错误出口：本地输入与 archive/selection 校验为稳定 400/413，Skill 或 Skill file 缺失为 404，无权启用为 403，来源或候选状态冲突为 409；Git 来源执行故障为带 request ID 的 retryable 502，repository、受管包存储和预览重建故障为带 request ID 的 retryable 5xx。服务层的 typed error 只携带刻意公开的文案，Git 输出、URL、数据库、路径和 wrapped cause 只进入内部日志。preview、create/update/delete 和 package owner 切换不得忽略 repository/path 查询失败；本契约不改变多 Skill 导入的 batch transaction 边界。

Prompt Group list/create/update/delete 复用独立的资源错误边界：ID 与名称校验为稳定 400，同一用户内大小写不敏感的重名为 409，缺失或跨用户访问为 404，repository/transaction 故障为带 request ID 的 retryable 5xx。rename 继续在同一 Context-aware 事务中同步 `prompts.group_name`，delete 继续把所属 Prompt 移回默认分组；本公共错误契约不改变 Prompt catalog 分页或前端编辑器所有权。

个人与共享 Prompt CRUD 复用同一领域错误出口：ID、分页、标题、正文与分组字段的本地约束为稳定 400，缺失 Prompt 或不可访问分组为 404，个人入口修改可见共享 Prompt 为 403，repository、transaction、rows iteration 与 rows-affected 故障为带 request ID 的 retryable 5xx。共享 Prompt 仍只能由管理员入口创建、更新和删除；个人私有 Prompt 与共享库的可见性隔离不变。本契约不把有界 page 当完整 catalog，不改变前端 editor owner，也不改变 partial PATCH 的完整对象写回语义；这些分别继续由 P2-27、P2-23 与 P2-47 收口。

管理员 User Group list/create/update/delete 复用稳定资源错误边界：ID、名称、描述、等级与配额限制校验为 400，名称重名及撤销/删除最后默认组的 invariant 冲突为 409，缺失资源为 404，repository/transaction 故障为带 request ID 的 retryable 5xx。默认组 advisory-lock 与事务保护继续由 repository 持有；本契约不改变 effective group 解析、request context 传播或 partial PATCH 并发所有权。

管理员 User list/create/update/reset-password/set-group 共享用户管理错误边界：分页、ID、用户名、邮箱、昵称、角色、权限、密码与 group ID 校验为稳定 400，用户或目标分组缺失为 404，用户名/邮箱重名及最后活动管理员 invariant 为 409，repository/transaction/密码哈希故障为带 request ID 的 retryable 5xx。账号角色、状态或密码变化仍沿既有事务递增 auth version、取消活动 run；本契约不改变 request context、字段级 PATCH 或 profile/avatar 文件所有权。

个人 profile 读取、资料更新与改密共享账户错误边界：邮箱、昵称和新密码的本地约束为稳定 400，当前用户缺失为 404，邮箱重名为 409，repository、事务和密码哈希故障为带 request ID 的 retryable 5xx；错误旧密码继续作为不泄漏账户内部状态的受控 400。密码在 bcrypt 前按 6–72 bytes 校验，资料更新在 repository 约束 owner 保留 unique 与 rows-affected 分类，改密成功仍沿既有事务递增 auth version、取消数据库 run 与 RunHub run。本契约不改变头像文件生命周期、Settings 草稿所有权、HTTP request context 或字段级 PATCH/lost-update 语义。

头像 upload/delete/serve 使用独立的文件与账户错误边界：缺少文件、无效图片和大小超限为稳定 400/413，用户缺失为 404，读取、处理、受管目录/文件写入与 repository 故障为带 request ID 的 retryable 5xx。已写入新文件后若 profile 读取或头像更新失败，handler 继续补偿删除本请求新文件。本公共错误契约不改变头像的并发 swap、旧文件删除 owner 或 profile partial PATCH 策略；这些仍由 P2-47 收口。

字体 list/upload/update/select/delete/file 使用独立的资源与存储错误边界：ID、slot、metadata、multipart、内容类型和大小校验为稳定 400/413，字体缺失为 404，已停用字体不可选择为 409；repository、配置解析、请求体读取、受管目录/文件写入和已登记字体文件缺失等内部故障为带 request ID 的 retryable 5xx。repository 列表与 mutation 必须检查 iterator/rows-affected 错误，配置中的非法字体 ID 不能静默退回系统默认。本公共错误契约不改变字体槽位的多键事务与并发 owner、显式 null/legacy 回退或 FontAsset partial PATCH 语义；这些仍分别由 P2-29、P2-30 与 P2-47 收口。

注册与登录共享认证错误边界：注册用户名、邮箱、昵称、密码和 preferences 的本地约束为稳定 400，用户名或邮箱重名为 409，登录的未知账号与错误密码统一为 `invalid_credentials` 401，待审核或停用账号为受控 401，限流为带 `Retry-After` 的 retryable 429；repository、注册事务、密码哈希与 token 签发故障为带 request ID 的 retryable 5xx。注册在数据库查询和 bcrypt 前完成可判定输入校验，repository 在实际 registration unique constraint owner 保留 conflict 分类；首用户管理员、后续用户待审批和现有限流计数/重置算法不变。

认证 middleware 在所有受保护 API 前重新读取当前活动账号与 auth version，不信任 token 中的用户名或角色。缺少/非法 Authorization header、无效 token、非法 claims 和已失效账号使用稳定 401 code，非管理员访问为稳定 403；账号状态 repository 故障为带 request ID 的 retryable 5xx，并保留底层 cause 供内部诊断而不进入响应。该契约不改变 JWT 七天有效期、legacy token 的 auth version 兼容或账号变更后的 run 取消行为。

`/admin/status` 只展示当前部署容器可见的版本、build ref、schema、Go 运行时、cgroup 内存、受管存储、PostgreSQL 和文档提取器状态。依赖探测短超时且相互独立，单项失败仍返回其余状态；页面只在进入或手动刷新时请求。它不读取 Docker Socket、宿主机监控信息、环境变量、服务地址、密钥或绝对路径。

管理员保存渠道、模型、外部服务或工具配置后，只影响新请求；已经运行中的 SSE / Agent run 不会中途切换凭据。

模型、渠道、外部服务、系统配置与 Tool 配置的普通 JSON 失败使用稳定的 `error`、`code`、`retryable` 契约。纯本地字段、模板或排序校验返回可修改的 400，资源不存在返回 404，模型重复创建返回 409，外部服务 probe 失败返回 502；repository、registry 和其他内部故障返回带 request ID 的 5xx。默认模型校验也必须保留数据库/渠道读取故障的内部分类，不能把运行故障伪装成“模型不存在”。只有受控本地校验文案可以公开，SQL、内部路径、provider 原文和凭据化 URL 只保留在服务端诊断。

## 发布入口

- 快速部署见 [Docker Compose 部署](docs/03-实施计划/Docker-Compose-部署.md)。
- 管理配置见 [管理员配置指南](docs/03-实施计划/管理员配置指南.md)。
- 公开导出见 [开源发布检查清单](docs/03-实施计划/开源发布检查清单.md)。
