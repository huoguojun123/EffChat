# EffChat 架构文档

适用版本：pre-release 0.3.4

EffChat 是一个面向 2-5 人自托管的小团队 agent workbench。当前主线是 Web-first：React 前端、Go API、PostgreSQL、Python extractor sidecar，以及基于 Eino 的统一 Agent。

## 技术栈

- Backend：Go 1.26.5、Gin、Eino v0.9.13、eino-ext openai/claude/gemini adapters。Claude adapter v0.1.20 通过 `backend/third_party/eino-claude` 保留一处可审计的上游 race 修正，待官方发布等价修复后移除本地 replace。
- Frontend：Vite、React 19、TypeScript、Tailwind CSS v4、Shadcn/Radix 基础组件、Zustand。
- Database：PostgreSQL 17。
- Extractor：Python sidecar，负责本地文档解析和 MinerU OCR 代理；镜像以固定
  `10001:10001` 运行，应用与依赖层保持 root-owned，只使用容器内标准 `/tmp`
  处理临时文件，不挂载宿主机活动存储。普通 PDF/Office/CSV 解析通过线程池
  离开 uvicorn event loop，并限制为 2 个并发解析槽；排队 5 秒仍无空位返回稳定
  `extractor_busy`，health 不占解析槽。Office ZIP 在构造标准库 archive reader 前预检 EOCD，
  并限制条目数、central directory、单项/总解压大小和压缩比（4,096 项、4 MiB
  directory、单项 32 MiB、总计 64 MiB、100:1），CSV 只接受
  逗号、Tab、分号和竖线，并限制单 field 1,048,576 字符、100,000 行、256
  列、500,000 个 cell 及当前输出字节预算，避免小压缩包或异常表格占满
  512 MiB sidecar。
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

外观设置的主题预览只引用 `globals.css` 的语义 token，不在 TypeScript 中复制 hex 色板；浅色/深色主题和强调色仍通过既有浏览器存储键保存。根级外观 View Transition 在活动 Radix dialog 内被跳过，以保持设置弹窗的交互树稳定；普通页面切换继续使用受控过渡。桌面紧凑断点（包括 1080p/125% 下约 1536×864 CSS viewport）只收窄侧栏、管理导航、设置框和欢迎引文，不改变用户可调的聊天正文字号；移动端保持抽屉式侧栏和无横向溢出。

聊天区域的通用 shell 与当前是否已有 active session 解耦：欢迎页、readiness 检查中、检查失败和无可用模型状态都继续渲染唯一的侧栏 opener，使移动抽屉或桌面持久化收起后的历史会话、账号菜单和设置仍可到达。模型选择、文件、导出、输入框、会话文件抽屉和拖放上传只在 active session 存在时渲染；隐藏侧栏继续由 `aria-hidden`、`inert` 和 pointer-events 共同隔离，不复制第二套 opener 或侧栏状态。

`/chat/:sessionId` 只接受不带符号、前导零、小数或指数形式的正十进制安全整数。非法参数在发起会话详情请求前以 replace 导航回根页并清空 active session；合法但当前列表未加载的 ID 仍沿既有鉴权详情查询确认存在性，查询失败同样回到可达的空会话 shell。

移动端高频管理、文件和暂存附件动作使用至少约 44 CSS px 的实际命中盒，同时保持 14–16 px 图标视觉尺寸；原生 list/范围按钮显式复用 `focus-visible` ring，搜索输入提供稳定 accessible name。Radix Dialog 要么提供 `DialogDescription`，要么显式关闭描述关联，避免把 console warning 当作无害噪声。会话记忆继续保存英文 section key 与原始 title，但已知分区在中文界面使用中文展示标签，所有记忆时间统一以 `zh-CN` 24 小时格式显示。

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

Session 的数据库实体继续以 JSONB bytes 保存 `metadata`，但 HTTP wire boundary 必须把它序列化为 JSON object。Create、Get 和 List 使用同一模型级契约，前端刷新或重新进入会话后仍可直接读取 `skills_enabled` 等结构化字段；反序列化兼容旧版本曾返回的 base64 string，只用于读取历史响应，不再对外生成旧 shape。该修复不改变数据库 JSONB、Skill 权限过滤或 Agent 运行时读取路径。

会话 Skill 快速切换由前端每个 session 的局部 mutation coordinator 串行提交。同一 session 在途时只保留最新 desired set，旧 success/failure 不得覆盖后续点击；成功必须消费服务端权限过滤后的 canonical `skills_enabled`，最终失败重新读取会话 canonical 状态。coordinator 空闲后从当前 store 快照重新建立基线，组件卸载或账户 reset 会失效全部 owner，迟到响应不能写入新账户。该前端所有权不改变后端 JSONB 定点更新、权限过滤或 Agent preflight，也不承诺跨标签页同字段 CAS；若未来需要多客户端确定性，应增加窄 `skills_enabled` revision，而不是扩展为全 Session CRDT。

会话列表的 `limit/offset/folder_id` 同样是显式查询契约：limit 只接受 1–100，offset 必须非负，folder scope 只接受 `all`、`unfiled` 或正整数 ID；非法值统一返回 `session_list_query_invalid` 400，不静默回退默认分页。该校验不改变 `has_more/next_offset`、置顶排序、folder 归属或前端加载更多行为，repository 故障仍使用带 request ID 的 retryable `session_list_failed` 5xx。

全局对话检索只接受 2–120 字符 query、`all/unfiled/folder` scope、folder scope 的正整数 ID 和 1–50 的 limit；非法值统一返回 `conversation_search_query_invalid` 400，不再以裸 error 或默认 limit 隐藏错误。检索仍只读取当前用户未删除会话及可见 selected answer，repository query/scan/iteration 故障统一为带 request ID 的 retryable `conversation_search_failed` 5xx；前端既有 debounce、迟到响应 owner 和结果跳转行为不变。

Run active/status/resume/cancel 入口先复用会话归属边界，再对 run id 和 replay cursor 做稳定 400 校验。durable reservation 或 RunHub 中的 run 缺失、跨会话或跨用户都收敛为 `run_not_found` 404，quota repository 和 stream writer 故障为带 request ID 的 retryable 5xx。RunHub 使用 typed not-found sentinel 仅支撑 HTTP 分类；SSE replay/gap、多订阅、terminal snapshot、停止幂等和 durable-first 生命周期不变。

accepted run 的 HTTP fallback 与 durable terminal 使用同一公共错误 payload builder。取消或 setup-timeout 的 terminal 持久化失败、以及 execution owner 建立后 SSE writer 无法打开时，响应必须保留 request ID；后者还返回 run ID，提示客户端稍后恢复。该关联信息只用于追踪和恢复，不改变后台 worker 继续执行、terminal 重试、replay 或 exactly-once 所有权。

run admission 已发现 durable terminal，或 BeginExecution 已被另一 observer/worker 占有但当前连接又无法建立 SSE 时，409 fallback 同样保留 request ID、稳定 `retryable=false`，execution-owned 还返回原 run ID。客户端应刷新或按 run ID 恢复，不能通过重试 admission 创建第二个执行 owner；reservation、replay 和 terminal 事实不变。

accepted worker 建立后首次 RunHub 订阅失败的 SSE error 保留 request ID、run ID 和 `retryable=true`，提示稍后恢复；既有 run replay 发生 scope/not-found 时保留同样关联字段并使用 `retryable=false`。内部 RunHub cause 只进入日志，不能进入 SSE；这两条旁路不取消 worker、不重放 provider，也不改变 durable terminal。

每个用户 turn 的 answer attempts 都保留独立输出树；不论该 turn 后面是否已经产生新消息，用户都可在仍有可选择输出的 attempts 之间切换。选择只更新该 user message 的 selected attempt 与会话级 `answer_selection_revision`，后续 Agent、压缩和记忆上下文继续只读取 selected 输出，不复制消息或改写其他 turn。删除当前 attempt 会在同一 session row lock 事务中硬删除其 assistant/tool 输出；若删除的是 selected attempt，事务先选择相邻的可用 attempt 并推进 revision，每个 turn 至少保留一个可选择 attempt。无效 ID 为 400，会话或 attempt 缺失为 404，不可选择或只剩最后一个为 409，repository/transaction 故障为带 request ID 的 retryable 5xx；选择或 replacement 成功后复用单会话记忆重整。

会话 create/update/delete mutation 复用同一边界：受控字段、folder 和生成参数校验返回 `session_invalid` 400；初始归属查询与 update/delete 的 `RowsAffected == 0` 都把缺失或竞态删除收敛为 `session_not_found` 404。模型、渠道、默认模型和用户组读取必须区分“确实不存在/不可用”与 repository 故障；前者使用稳定模型业务码，运行依赖暂不可用为 retryable 503，数据库、扫描和事务故障为带 request ID 的 retryable 5xx，任何分支都不得把 wrapped error 原文写入 JSON。

空请求创建会话只使用管理员配置的全局默认模型，不按目录排序猜测模型。`GET /sessions/readiness` 复用与真实创建相同的默认模型、用户权限和渠道可运行校验：配置缺失或安全的模型状态返回 `200 + ready=false` 与稳定 code，repository 故障仍是可追踪 5xx。前端在 readiness 未知或 blocked 时不发送空创建请求；侧栏与空会话入口共享一个 busy/error owner，重复点击不会产生并发创建。系统尚无 runnable public 模型时允许空默认作为 bootstrap 状态；一旦存在可运行的公共模型，Admin 清空 `default_model_id` 会在写事务前被拒绝并保留旧值。

前端消息正文只维护一个当前窗口 generation。切换会话、账户 reset、latest/full reload、around 跳转和显式窗口替换都会先递增 generation、失效旧分页并释放 loading；older/newer 同一时刻只有一个方向拥有分页请求，响应还必须匹配 session、generation、方向 owner 和原 cursor 才能合并。RunHub/SSE terminal 对账捕获同一 generation，历史窗口不会被迟到 durable sync 改写；full reload 替换 durable 行，只保留尚待服务端对应记录接管的本地 optimistic 消息。滚动锚点同样绑定 generation，窗口替换后旧 observer 或 timeout 不能继续调整 scrollTop。

会话文件夹 list/create/update/delete 使用独立的资源边界：ID 与名称校验为稳定 400，不存在、无权访问或 mutation rows-affected 竞态统一为 `session_folder_not_found` 404，repository 查询、扫描和写入故障为带 request ID 的 retryable 5xx。列表必须在返回前检查 `rows.Err()`，不能把中途数据库故障伪装成部分成功；该公共错误契约不改变复合 PATCH 字段的更新语义。

侧栏会话与文件夹置顶分别按对象 ID 维护局部 operation generation。乐观更新、服务端 canonical `pinned_at`、失败回滚和列表 reload 只有在仍拥有当前对象时才能提交；失败只恢复该对象的置顶字段，不能覆盖其他对象或该对象的标题、归属等并发变化。同一对象的 PATCH 按用户意图顺序串行送达服务端，不同对象仍可并行，避免旧请求最后落库后在刷新时恢复旧意图；已被更新意图或账户 reset 取代的旧失败不再写 UI error。列表请求会保留其发起后出现或仍在途的 pin intent，账户 reset 会失效全部 owner。`PATCH /sessions/:id` 成功返回更新后的 Session，使客户端无需追加一个可能乱序的读取；该协调只覆盖侧栏 pin 字段，不扩展为通用 mutation framework 或跨标签页 CAS。

send/preflight/retry/manual compaction 在 run accepted 前沿用相同公共错误契约：会话缺失为 404，账号失效为 401，消息输入为 400/413，retry 尾部竞态、已产生回答和附件失效为稳定 409；用户、Skill、历史、压缩任务状态、run reservation 和 SSE writer 的内部故障只返回带 request ID 的 retryable 5xx。manual compaction 一旦被 RunHub 接受，setup 阶段的会话/账号消失和 preserve target 竞态会写入同一 durable terminal 公共码，刷新和 replay 不会退化成内部错误原文。撤销压缩把缺失 checkpoint 归为 404，把非 manual 或已有新消息归为 409，repository/事务故障归为可追踪 5xx。

checkpoint 虽以 `role=user` 进入 Eino 消息序列，但它是系统生成的上下文治理记录，不是用户发送消息。每日消息配额、准入检查和 Admin 今日用量统一排除 `metadata.compaction_summary=true`；普通用户消息即使后来被 retry 或删除，仍按既有消费语义计数。

压缩模型是输出受限且结果会持久化为后续上下文的 utility consumer。它复用当前会话的模型和渠道，但在克隆的任务请求上关闭可选 thinking，避免 reasoning 抢占摘要预算；收流后、checkpoint 落库前还会复用主聊天的 inline `<think>` 分离边界。压缩控制契约只放在首条 system message，原始历史保持各自角色，末尾仅追加被 system 明确排除出待总结数据的应用控制标记；模型不得归因、复述或解释该标记，也不得把 EffChat 的标签或七节格式归因为用户请求。摘要抽取器同时容忍兼容网关把 opening `<analysis>` 移入 reasoning 字段、却在 content 中留下 orphan `</analysis>` 与 `<summary>` 包装的情况，防止隐藏推理进入 durable summary。原始会话请求保持不变，主聊天的 thinking 配置不受影响。

## 工具与联网

- 工具通过 Eino Tool interface 挂载，不做 MCP runtime。
- 管理后台可启停现有工具并配置超时；运行时、Admin 和数据库共享固定 Tool catalog。未知 key 默认关闭，Agent 在把 Eino ToolInfo 交给模型前反向校验其名称属于治理目录；新增 Tool 若没有同步 catalog 与 migration，会在 setup 阶段失败而不是成为不可见的可调用能力。数据库约束拒绝目录外 key，增量 migration 会清理已废弃的历史配置。
- Tool/Skill catalog mutation 使用追加式治理事件记录 actor、before/after、原因、包版本和回滚来源；管理员的 metadata、package/import 与 delete 均在同一数据库事务中写入治理事件。事件只保存非正文状态；Skill 包正文继续位于受管存储，import record 的不可变文件引用保留未来版本。legacy active package（含 builtin）首次进入治理路径时会建立文件引用快照，删除保留 tombstone。回滚必须确认当前状态仍等于原事件的 after，并校验 retained files manifest 后恢复受管文件引用，再提交目标状态与新的反向事件；不能改写历史，也不能重新依赖 Git/Zip 上游。
- 工具自身返回的受控业务失败保持结构化结果。repository、持久化或受管文件 I/O 等内部失败必须作为 wrapped Go error 交给 Tool governance，不能伪装成成功 envelope 内的 `error` 字段；治理边界也会把带显式公共 `message` 的结构化失败改写为该公共文案。模型、RunHub 和消息树只接收稳定失败，原始 repository、路径或 provider 原因仅进入受控内部诊断；已有 `error_code` 的网页 typed 失败继续由网页工具自己的分类器负责。
- 搜索链路由管理员为 Tavily、Brave、Exa、博查和 SearXNG 独立配置并排序；按顺序成功即停止。
- 网页提取链路按管理员保存的顺序执行已启用的 Firecrawl、Jina、Tavily 和 Exa，第一个取得有效正文的 provider 即停止；Basic 不进入外部服务配置，始终追加为最后一个本地兜底。provider 规范化必须保留第三方相对顺序、移除提前或重复出现的 Basic，并在末尾只补一个 Basic。
- Basic 的 URL 请求、重定向、压缩前与解压后各 2 MiB 响应上限、SSRF 和 DNS rebinding 边界继续由 Go backend 独占；解析后的正文最多保留 512,000 字符。HTML 交给 `go-readability` 提取正文并渲染为普通文本，只保留必要段落、代码和表格换行；`text/plain` 按声明编码解码，二进制或空正文继续进入既有 provider fallback。系统 resolver 只有在全部答案均为 fake-IP 代理使用的 `198.18.0.0/15` 时才由 Basic 专用 resolver 查询独立公网 DNS；真实私网、混合地址、普通 DNS 错误、重定向和拨号重解析仍由原 SSRF policy 拒绝。
- Basic 正文未超过当前 `detail` 的最终上限时直接返回，不调用小模型。超限正文先复用 Bleve Unicode segmenter 与 Aikit BM25 对段落做 goal（缺省时用标题）相关性排序；仅在候选仍需提炼且 refinement policy 可用时交给既有流式小模型。管理员关闭提炼属于正常本地策略，不标记 degraded；模型开启但不可用、冷却、超时、空结果或失败时，返回按 BM25 选中的原文段落及邻近上下文并记录稳定原因。无有效查询时从多个文档区段取样，只有单段仍超限才做最终字符边界；候选筛选本身不使成功摘要误报 `truncated`。`detail=source` 永远不经过模型改写，只返回带截断状态的相关原文片段。第三方 fallback 成功后仍按既有 policy 执行 refinement。网页提炼复用统一的流式模型消费契约：固定时限只等待首个有效输出，首包后完整收流。任务请求显式关闭 DeepSeek V4 thinking，并在结果边界剥离仍被兼容网关写入正文流首的 `<think>` 块，避免隐藏推理占满工具正文预算。前端工具树把 clean、degraded/truncated 和 hard error 分别呈现为成功、安静 warning 和错误；warning 通过稳定中文文案说明提炼或截断原因，同时保留 fallback 正文和来源链接，流式、RunHub 恢复与历史消息共享同一 renderer。
- Basic 只记录不含内容的结构化诊断：`success`、`challenge`、`empty`、`unsupported_content`、`http_status`、`fetch_error`、`decode_error`、`parse_error`，以及实际调用提炼模型时的 `basic_refine_called`。日志只包含状态、解析器、响应/正文大小、耗时、截断状态和 URL/goal 字符长度，不记录完整 URL、域名、正文、查询、Key 或用户信息。
- 网页提炼开关与模型 ID 属于内容外发 policy：成功解析的值成为进程内 last-known-good snapshot；短暂查询/解析故障只复用该快照，冷启动无可信值时保守关闭二次提炼，不构造 utility model，也不把 crawler 正文发给模型。accepted runtime snapshot v4 固定实际生效策略、`ready` / `disabled` / `unavailable` 状态和已解析的模型/渠道依赖，执行阶段不重新读取 live config。
- 工具调用日志不做持久化后台页面；排障依靠容器日志和用量统计。

## 文件与 OCR

- 文件元数据在 `files` 表，解析文本保存在受管理的数据目录。
- 单文件上传采用一个部署级可兑现上限：`PY_EXTRACTOR_MAX_UPLOAD_BYTES` 同时约束 web proxy、backend 和本地 extractor，Nginx 只额外预留 multipart framing。管理员的 `file_upload_max_size_mb` 是该硬顶以内的产品策略；历史过大值运行时收紧但不自动改写。MinerU 的 200 MB/200 页是其异步 OCR 上游约束，不扩大 EffChat 的上传承诺。
- 受管附件是私有用户内容：`storage/attachments` 及其用户/月目录使用 `0700`，图片原件、解析文本和 OCR staging 文件使用 `0600`。backend 启动入口保留所有受管 storage 的既有 owner 归一化，但只递归收紧 attachments 的 mode，不改变 avatars、fonts 或 skills 的模式语义；API 会话/用户鉴权仍是读取边界，文件 mode 是宿主机防御纵深。
- PDF、DOCX、XLSX 与 CSV 的表格共用一个 GFM serializer。cell 先转义用户 HTML，
  再按顺序保留反斜杠、转义 pipe，并把真实 CR/LF/CRLF 编码为安全的 `&#10;`；
  remark-gfm 因而维持原行列，当前 `remark-breaks` 将解码后的换行渲染为 `<br>`。
  下载和 Agent 读取继续消费同一份无原始 HTML 注入的 Markdown sidecar。
- 文件 list/download/preview/OCR refresh 的公共读取契约区分本地参数 400、用户域内缺失 404、解析未完成 409 与 repository/受管存储/sidecar 读取的带 request ID 5xx；缺失与无权访问保持不可区分。`ApiError` 保留后端 `code/retryable/request_id`，文件预览按稳定 code 展示缺失或暂无正文，并只在公共协议允许重试时提供重试入口，不再解析中英文错误字符串。列表扫描统一检查 `rows.Err()`，中途数据库故障不能伪装成部分成功结果。图片下载返回原始图片名和 MIME；已解析的 Office/PDF/文本附件下载受管 extracted sidecar，响应使用 `<原名>.txt` 与 `text/plain`，UI 明确标为“下载提取文本”并以响应 `Content-Disposition` 为准，不把 sidecar 伪装成仍被保留的原件。鉴权下载复用公共网络/timeout/401/404 错误边界；下载入口拥有取消和请求代次，旧请求不能覆盖当前反馈。鉴权图片的 object URL 还携带当前 `fileId + generation` owner：切换、清空或卸载会中止旧请求并只释放该请求创建的 URL，任何新文件的 committed render 都只能显示自己的 loading/success/error，不能短暂复用上一文件的 URL 或错误。
- 文件上传准入与持久化复用同一公共错误协议：缺少 multipart/file/session 参数为稳定 400，文件与解析输出超限为 413，声明 MIME 不一致为 400，白名单或解析器不支持为 415，损坏/无可读正文为 422，会话文件数达到上限为 409；会话不存在或无权访问统一为 404，但 session/repository、受管存储、OCR queue 和 metadata 故障必须返回带 request ID 的 retryable 5xx。extractor owner 用 sentinel 区分用户内容、资源上限与依赖故障，Python sidecar 的响应正文、内部路径和上游原因不会传播到 Go 公共错误或 request log；metadata 创建失败会补偿删除本轮刚写入的原件、OCR buffer 或 extracted sidecar。
- 浏览器附件上传使用原生 XHR 暴露真实传输字节进度，并为每个文件维护 queued/uploading/processing/failed/cancelled 状态。单项取消通过 AbortSignal 传播到 request context；上传 repository 的 duplicate/count/create 查询均响应取消，已持久化但客户端未接受的 staged 文件立即进入既有 `cleanup_claimed` 生命周期，避免“UI 已取消、附件仍可用”。失败或取消任务在当前会话 epoch 内保留用户原始 `File` 以便重试，切换会话或卸载会取消请求并释放内存引用。
- 暂存附件列表由窄的 `{sessionId, sessionEpoch, listGeneration, attachmentRevision, errorGeneration}` owner 管理。初始加载和手动刷新只有在仍是最新请求、会话生命周期未变化且期间没有上传完成、删除、发送认领或 OCR mutation 时才能替换快照和裁剪 selection；迟到的列表、OCR、删除、发送回调和错误 timer 不得写入 A→B→A 后的新会话生命周期。
- 前端上传预校验的 limits 接口与真实上传入口读取同一个 fail-closed policy；策略在冷启动不可用时二者都返回 `file_policy_unavailable`、带 request ID 的 retryable 503，不能由只读入口退化为无关联信息的裸错误。last-known-good、degraded 标记和实际上传最终裁决保持不变。
- 人工 OCR retry 在 mutation 前分别校验附件 policy、Go/Python runtime 依赖和 MinerU 渠道配置；管理员未启用返回稳定 409，控制面或 runtime 暂不可读返回带 request ID 的 retryable 503。文件不存在或无权访问统一为 404，repository mutation 故障为可追踪 5xx。`RestartOCR` 提交新的 pending generation 后才复核受管原件：过期或确实缺失会用 `FailOCR` 补偿闭合为 failed 并返回 409，越界路径、非缺失型文件系统错误或补偿失败返回稳定可追踪 5xx；只有复核成功才唤醒 recovery runner。
- 用户删除附件只先提交数据库 tombstone/cleanup claim，不在请求内删除受管字节。无效参数为 400，缺失或无权访问统一为 404，不可用生命周期状态为 409；lookup、受管路径校验和删除事务故障返回带 request ID 的 retryable 5xx。删除事务继续负责 fencing OCR worker，并在 formal attachment 上同步写入历史消息 tombstone；物理清理由管理员维护入口按租约完成。
- 部署存储校验对仍可用的 `staged` / `formal` 附件、头像、字体和 Skills 保持严格路径与磁盘存在性检查。`cleanup_claimed` 附件在保留期内只要求全部候选路径仍位于受管 `storage/`：解析或 OCR 在删除前可能尚未生成派生文件，清理重试也允许目标已经不存在；到期 cleanup 继续幂等删除现存字节并收口为 `storage_removed`。
- 管理员批量 cleanup 在任何 claim 前先完成只读统计，随后分别过期 OCR source、按 lease claim 文件、删除受管字节并用 claim token finalize；单文件失败不会中止同批其他文件。顶层参数错误为 400，repository 阶段故障为带 request ID 的 retryable 5xx；200 部分成功响应的每项失败都包含稳定 `code/error/retryable`，并在存在失败时携带 request ID。物理删除或 finalize 失败会尝试立即释放 claim；释放本身失败使用独立 code，避免把延迟重试的 lease 状态隐藏在泛化错误中。
- 图片保留原图；文档类文件不承诺长期保留原始 PDF/Word。
- PDF 当前策略是 MinerU 优先，本地 Python 解析兜底。
- PPTX 本地解析按 slide/shape 原顺序递归 group；普通文本与原生 table 进入同一
  sidecar，table 复用共享 GFM serializer 并累计 `table_count`。Chart、SmartArt/
  diagram 和 OLE 不伪装成已提取内容，只返回去重的有界 warning。
- DOCX 本地解析按 `w:body` 顶层 child 原顺序交替输出 paragraph 与 table；空段落
  不制造正文，table 继续复用共享 GFM serializer，避免“全部段落后再附全部表格”
  破坏条件、单位、注释与数据之间的相邻关系。
- XLSX 本地解析同时打开只读 formula view 与 `data_only` cached-value view，按
  worksheet/cell 位置配对而不执行公式。公式 source 始终保留；存在 workbook
  cached value 时同时输出稳定 `[cached value: ...]` 标记，缺少缓存时输出
  `[no cached value]`，共享公式和数组公式成员继续可搜索且不会与真正空 cell
  混淆。worksheet 结果仍复用共享 GFM serializer，Office archive 资源边界不变。
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
- 记忆维护只走完整流式消费。provider 以 `length`、`max_tokens`、`max_output_tokens` 等原因达到输出上限时，本次结果一律不解析、不保存，并在 `model_task_runs` 记录 `memory_output_limit`；能力不足记录 `memory_output_budget_insufficient`。手动压缩并行触发的记忆维护属于 best-effort enrichment：记忆输出上限、patch 校验或 CAS 冲突只记录各自任务失败，不得取消仍可用的压缩 checkpoint；压缩自身失败才终止压缩 Run。
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
- OCR 用量与今日限额压力统一以 `files.ocr_started_at` 作为执行事件时间：`created_at` 仅表示上传事件，`ocr_started_at IS NULL` 的排队文件不计入 OCR 文件数、页数或失败数；跨日上传但当日开始的文件按开始日计入，开始后失败仍按开始日计入，并与 OCR quota admission 使用同一口径。
- Admin Usage 查询只接受空值默认 7 天、`today/7d/30d` 预设，或成对的 RFC3339 `start_at/end_at` 自定义范围；非法组合、日期和超过 90 天的窗口统一返回 `invalid_usage_range` 400 且不可重试，不再静默回退到 7 天。repository 聚合故障继续使用带 request ID 的 retryable `usage_summary_failed` 5xx；前端把范围 effect、手工刷新和自定义日期变化纳入同一 generation/query owner，迟到响应只能更新仍匹配当前范围的 data、label、error 和 loading。该 UI owner 不改变 OCR 事件时间口径或后端 HTTP 契约。
- 提示与知识：底层提示词、提示词库、Skills。
- 系统：实例状态、系统配置、字体、文件清理。

管理员模型 probe 保持 `200 + ok=false` 的状态型失败协议，避免单个模型检测失败升级为管理后台全局异常；请求字段缺失为稳定 400，probe runtime 缺失为带 request ID 的 retryable 503。setup 和 provider stream 故障只返回稳定 code、retryable 与经 Agent 分类的安全 message/diagnostic，原始渠道、数据库、URL、密钥、响应正文和路径只进入内部日志。probe 只验证最小文本连通性，并且只有完整收流后的正文在去除首尾空白后精确等于 `OK` 才返回 `ok=true`；nil、空文本、`NOT OK` 或任何附加文本都以 `model_probe_unexpected_output` 返回 `200 + ok=false`，不能把“上游返回了某些内容”误报为模型可用。

models.dev 单模型目录查询对空 ID 返回 `models_dev_model_invalid` 400，对缺失 provider 或 model 返回稳定 404 code，均携带 `retryable=false` 且不回显用户输入；目录获取失败继续为带 request ID 的 retryable 502。该公共错误契约不改变缓存、目录导入、能力来源、退役标记或候选日 freshness 门禁，后者仍由模型目录生命周期独立负责。

模型目录的持久事实仍由 PostgreSQL `models` 表拥有；backend 启动会用该表整体替换进程内 registry，空表也不会回退到编译期内置型号。migration `046_model_catalog_lifecycle.sql` 为每条管理员模型记录增加 `catalog_source`、`catalog_checked_at` 和 `lifecycle_status`：既有与普通手工创建记录默认是 `manual` / `unknown`，渠道 `/models` 与 models.dev 只产生默认 disabled 的候选并携带各自来源及核对时间。目录中出现一个 ID 不等于账号可运行，因此除显式 preview/experimental 标记外不从名称猜测 active/deprecated/retired；管理员记录不会因目录消失而被自动删除。**管理员显式 DELETE 是独立的硬删除操作，直接移除 `models` 行；启用/禁用仍由独立的 enabled mutation 负责。硬删除后历史消息仍保留其 model_id 字符串，但新的模型目录和 registry 不得重新显示该数据库记录。** Admin 模型列表和编辑器显示来源、核对时间与 lifecycle；从渠道导入或应用 models.dev 能力时保留对应来源，管理员手工修改 capability/lifecycle 时切换为 `manual` 并清除旧目录核对时间，显示名称、可见组和排序等非能力字段不改变来源。migration `047_model_temperature_profile.sql` 以 `configurable`、`omit`、`fixed` 三种 typed policy 持久化 temperature 请求契约；固定策略要求 0–2 的显式值，其他策略禁止残留值。migration `048_model_openai_request_profile.sql` 进一步为 OpenAI-compatible 模型提供可空且有范围约束的 `top_p`、`n`、`presence_penalty`、`frequency_penalty` 固定值；空值代表完全省略字段，Admin 不接受任意 provider 参数 JSON。统一 ChatModel 构造在 accepted runtime snapshot 中固化这些模型级请求约束：temperature 的 `configurable` 保留会话值，`omit` 传 `nil`，`fixed` 使用管理员值；OpenAI-compatible typed profile 只在对应 adapter 边界映射为 wire 字段。主对话、压缩、记忆维护、网页 refinement 与最小 probe 复用同一解析，不修改会话中保存的用户偏好。发布候选日只在官方目录与受控 models.dev 同时给出一致 ID/capability 时更新编译期保守兜底；该更新仍不 seed 数据库、不自动启用或替换管理员模型。管理员 partial update 在 repository transaction 内先 `SELECT ... FOR UPDATE` 重读 canonical row，再只把请求携带的字段应用到该行并校验跨字段约束；不同字段的并发请求因此可以顺序合成，禁用模型也复用窄 `enabled` patch，不再把 service 层旧快照全字段写回。

Skill 的用户可见列表、会话启用、管理员 CRUD、文件读取、导入预览和 Git/Zip 更新共享领域错误出口：本地输入与 archive/selection 校验为稳定 400/413，Skill 或 Skill file 缺失为 404，无权启用为 403，来源或候选状态冲突为 409；Git 来源执行故障为带 request ID 的 retryable 502，repository、受管包存储和预览重建故障为带 request ID 的 retryable 5xx。服务层的 typed error 只携带刻意公开的文案，Git 输出、URL、数据库、路径和 wrapped cause 只进入内部日志。preview、create/update/delete 和 package owner 切换不得忽略 repository/path 查询失败。

Git Skill 的自定义 HTTPS 来源在启动 Git 前只解析一次，并把规范化 host、443 端口和全部已验证公网 A/AAAA 地址固化为不可变 transport plan。`ls-remote`、partial clone、tree scan 与可能继续获取 blob 的 sparse checkout 共用该计划；Git 通过 `http.curloptResolve` 固定实际 peer，仍以原 hostname 完成 TLS SNI 和证书校验，且不允许 redirect、credentials、交互认证、代理或继承的 `GIT_*`/global/system config 改写传输。任一 DNS 候选为私网、loopback、link-local、benchmark 或其他保留地址时整次请求失败，不回退到系统 DNS。GitHub、GitLab、Bitbucket 和 Codeberg 的精确主机名是显式 CDN 轮转例外，不做地址固定，但仍受 HTTPS/443、无 redirect、无代理、无配置注入和无交互认证约束；子域名与相似域名不会继承该例外。

一次 Git/Zip 多选导入可同时创建和覆盖多个 Skill，但浏览器只提交一个 import 请求，并用 `source_path -> target skill id` 明确重复候选的覆盖目标。服务在同一 `packageMu` 临界区先准备整批 content-addressed package root 和 manifest；repository 再用一个 SQL transaction 提交全部 active package、immutable import record 与 governance event。任一包写入或 SQL/commit 失败都不会切换任何 active Skill，未引用的新根由既有延迟清理回收；只有整批 commit 成功后旧根才进入宽限清理。相同目标与 package checksum 的重试是 no-op，不改变 `updated_at` 或追加 history，响应分别列出 created、updated 与 unchanged id。独立单 Skill 更新入口继续使用原单项事务，不引入任务队列或通用 batch framework。

Skill metadata PATCH 与 manual/Git/Zip package replacement 在 repository transaction 内锁定并重读 canonical Skill，再按字段 owner 合并：package 请求拥有 source、checksum、entry 和 active files，只有请求显式携带的 name、description、enabled、min group level 才覆盖管理员 metadata；未携带字段保留锁内 canonical 值。治理事件的 before/after、immutable import record、active package/files 切换仍在同一事务提交，`packageMu`、content-addressed root 和延迟清理边界不变。不同 owner 的并发请求因此按锁顺序合成，同字段保持 last-write-wins；锁等待取消不提交，也不会产生治理历史。

Skill 管理编辑器为实体选择维护单调 generation，为当前草稿维护 revision 与已确认 baseline；加载、保存、Git/Zip 预览和候选正文读取只有在实体、generation 及所需 revision 仍匹配时才能更新对应编辑器或弹窗。A→B→A 不能恢复旧 owner，旧请求的 `finally` 也不能清除新请求的 busy 状态。保存期间产生的新编辑继续保持 dirty，较早 revision 的成功只推进其 baseline；切换或关闭 dirty 草稿必须显式确认。服务端已经提交的保存、导入或更新仍必须汇入共享 Skill catalog，但迟到响应不得关闭、报错或覆盖后来打开的编辑器与弹窗。

模型管理复用同一局部 editor ownership 契约：对象、渠道、新建和离开模型管理都会失效旧 generation；models.dev 单项能力、目录匹配、渠道模型发现、保存、导入、启停和删除分别只提交仍拥有对应 editor/request 的局部 UI。服务端已经完成的模型 mutation 始终收敛共享目录；保存期间继续修改只确认已提交 revision，不覆盖新草稿。模型创建从临时 key 过渡到持久 model id 时保留同一 revision 历史，而不是把新输入误判为已保存。dirty 模型切换、换渠道或返回渠道概览必须确认放弃。

渠道表单在组件挂载时建立独立 generation，卸载立即 fencing 旧 save/delete callback。每次可持久化字段变化推进 draft revision 并通知父级导航 guard；旧保存成功只确认其提交 revision，保留保存期间的新 Base URL、Key 或启停草稿。渠道 mutation 已在服务端完成时仍更新共享渠道目录并刷新模型，但只有当前 owner 可以清理草稿、错误、saving 或触发 `onSaved/onDeleted` 导航，因此 A 的迟到保存不能把已经切到 B 的管理界面拉回 A。

Users/Groups、External Services 与 admin/user Prompt 编辑器复用相同的局部 generation/revision/operation contract。服务端已经提交的 mutation 继续收敛共享 catalog，但迟到 success、error、delete、probe 或 `finally` 只能更新仍拥有原实体和草稿代次的界面；普通资料与密码使用独立 revision，Prompt 与分组 mutation 也不能互相确认草稿。dirty 对象切换、新建和关闭必须确认放弃，较早 revision 的成功只能推进对应 baseline，不能覆盖或关闭后来编辑。

AdminPage 只汇总各编辑器已经判定的 dirty 布尔值，不接管其草稿。该汇总统一守护桌面/移动栏目导航、返回聊天、浏览器历史与 `beforeunload`；选择继续编辑时保留当前组件和 generation，明确放弃后才继续路由并由卸载失效旧 owner。没有草稿的 Usage、Status、Tools、Fonts 等即时操作页不制造离开确认。

Settings 的 profile/password tab 各自建立 editor generation 和 draft revision，并把 dirty 状态提升到弹窗级离开门禁。切 tab、取消、关闭弹窗或转入 Prompt Manager 使用同一确认语义；Appearance 是即时偏好，不制造草稿。资料或改密保存捕获提交 snapshot，输入在请求期间或成功后的延迟关闭窗口继续变化时，旧响应不得覆盖新资料、清空新密码或关闭弹窗。已经提交的 profile 结果仍更新共享认证用户；头像文件生命周期和后端 partial PATCH 并发边界保持独立。

Prompt Group list/create/update/delete 复用独立的资源错误边界：ID 与名称校验为稳定 400，同一用户内大小写不敏感的重名为 409，缺失或跨用户访问为 404，repository/transaction 故障为带 request ID 的 retryable 5xx。rename 继续在同一 Context-aware 事务中同步 `prompts.group_name`，delete 继续把所属 Prompt 移回默认分组；本公共错误契约不改变 Prompt catalog 分页或前端编辑器所有权。

个人与共享 Prompt CRUD 复用同一领域错误出口：ID、分页、标题、正文与分组字段的本地约束为稳定 400，缺失 Prompt 或不可访问分组为 404，个人入口修改可见共享 Prompt 为 403，repository、transaction、rows iteration 与 rows-affected 故障为带 request ID 的 retryable 5xx。共享 Prompt 仍只能由管理员入口创建、更新和删除；个人私有 Prompt 与共享库的可见性隔离不变。Prompt PATCH 在 repository transaction 内锁内重读 canonical row，只更新请求显式携带的 title、content、nullable description、tags、group_id/group_name；个人与共享入口的可写字段保持分离，空 tags、`description:null` 与 `group_id:null` 都保留字段存在性。Prompt 应用只把正文写入当前会话，不发送或追踪 Prompt ID；个人、公开与共享列表因此只按 `updated_at DESC, id DESC` 稳定排序，schema 与 API 不保留无法增长的 popularity 计数。该边界不把有界 page 当完整 catalog，也不改变前端 editor owner；分页与编辑器分别继续由 P2-27、P2-23 收口。

Admin 用户以及个人、公开、共享 Prompt 列表统一返回真实 `total/has_more/next_offset`，并在既有业务排序后以 id 作为稳定 tie-breaker。需要本地搜索、分组或管理全集的 Admin Users、PromptManager 和 PromptPicker 会按 100 条 offset page 连续读取到服务端末页，再执行既有本地交互；响应声明继续但 offset 不推进时客户端立即失败，避免死循环或把不完整集合伪装成全集。PromptManager 的 load owner 包含 scope 与 account，PromptPicker 每次 open/account 变化都推进 request generation；新 owner 启动时清空旧账号 catalog，迟到的任一个人/公开 source success、failure 或 `finally` 均不得覆盖当前列表、默认选择、错误和 loading。该契约不引入全文搜索服务、游标、缓存层或通用 catalog framework。

管理员 User Group list/create/update/delete 复用稳定资源错误边界：ID、名称、描述、等级与配额限制校验为 400，名称重名及撤销/删除最后默认组的 invariant 冲突为 409，缺失资源为 404，repository/transaction 故障为带 request ID 的 retryable 5xx。默认组 advisory-lock 与事务保护继续由 repository 持有。partial update 在取得该 advisory lock 和目标 row lock 后重读 canonical group、应用请求携带的字段并校验完整配额与默认组 invariant；不同字段并发顺序合成，同字段采用明确的事务顺序 last-write-wins，不再把锁外旧对象全字段写回。`users.group_id` 是原始显式绑定；NULL 不复制某个历史组，也不等于 level 0，而是由 repository 在每次权限或配额读取时动态解析为当前唯一默认组。模型与 Skill 消费共享的 effective level，quota 消费同一 effective group 的限制字段；默认组切换、等级/限额更新及显式组删除后的 `ON DELETE SET NULL` 因而立即在各链一致生效。

管理员 User Group、用户/认证、External Service reorder、系统配置 batch、Session delete、OCR 原件过期清理和 Skill package mutation 的 HTTP 写事务都从 handler 传播 request context 到 repository；数据库锁等待、statement cancel 或 commit 失败优先归一为 context 错误，事务回滚且不提交。非 HTTP worker、legacy wrapper 与独立后台生命周期继续由各自 owner 使用 background context；这条取消契约不把所有 repository 机械改写成 HTTP context，也不替代各资源的字段并发所有权。

个人 profile、头像与管理员 User partial update 在 repository transaction 内锁定并重读 canonical user，只应用请求携带的 email、nickname、avatar URL、role、permissions 与 active 字段；不同字段并发按事务顺序合成，同字段采用 last-write-wins。只有 role/active mutation 获取跨用户管理员 invariant advisory lock，并在同一事务内保护最后活动管理员、递增 auth version 和取消活动 run；profile/avatar mutation 只锁目标用户行。HTTP profile/avatar update 传播 request context，锁等待取消不提交。

管理员 User list/create/update/reset-password/set-group 共享用户管理错误边界：分页、ID、用户名、邮箱、昵称、角色、权限、密码与 group ID 校验为稳定 400，用户或目标分组缺失为 404，用户名/邮箱重名及最后活动管理员 invariant 为 409，repository/transaction/密码哈希故障为带 request ID 的 retryable 5xx。用户响应同时返回可空的原始 `group_id` 与非空 `effective_group`；后者包含 id/name/level 及 inherited 标记，使 Admin Users 能明确显示“继承默认组 X（等级 N）”，而不是把 NULL 误称为最低级。账号角色、状态或密码变化仍沿既有事务递增 auth version、取消活动 run；本契约不改变 request context、字段级 PATCH 或 profile/avatar 文件所有权。

个人 profile 读取、资料更新与改密共享账户错误边界：邮箱、昵称和新密码的本地约束为稳定 400，当前用户缺失为 404，邮箱重名为 409，repository、事务和密码哈希故障为带 request ID 的 retryable 5xx；错误旧密码继续作为不泄漏账户内部状态的受控 400。密码在 bcrypt 前按 6–72 bytes 校验，资料更新在 repository 约束 owner 保留 unique 与 rows-affected 分类，改密成功仍沿既有事务递增 auth version、取消数据库 run 与 RunHub run。本契约不改变头像文件生命周期、Settings 草稿所有权、HTTP request context 或字段级 PATCH/lost-update 语义。

头像 upload/delete/serve 使用独立的文件与账户错误边界：缺少文件、无效图片和大小超限为稳定 400/413，用户缺失为 404，读取、处理、受管目录/文件写入与 repository 故障为带 request ID 的 retryable 5xx。upload 先写入 UUID 受管文件，再通过同一 user row transaction 原子 swap `avatar_url`；repository 返回该次 committed mutation 实际替换的旧 URL，handler 仅在数据库确认已无活动引用后删除该受管路径。事务提交前失败由请求直接回收自己的新文件；commit 结果不确定时，新旧候选都先重新查询数据库 owner，再只删除未引用路径。并发 profile/avatar 不会恢复旧 URL，双 avatar upload 最终只保留 canonical URL 对应文件；外部 URL、非法受管文件名与仍被其他账户引用的路径从不删除。

字体 list/upload/update/select/delete/file 使用独立的资源与存储错误边界：ID、slot、metadata、multipart、内容类型和大小校验为稳定 400/413，字体缺失为 404，已停用字体不可选择为 409；repository、配置解析、请求体读取、受管目录/文件写入和已登记字体文件缺失等内部故障为带 request ID 的 retryable 5xx。repository 列表与 mutation 必须检查 iterator/rows-affected 错误，配置中的非法字体 ID 不能静默退回系统默认。新槽位配置键缺失时才允许从 legacy 全局键兼容回退；键已存在且 JSON 值为 `null` 时表示该槽位明确使用系统默认，读取和 `/system/info` 必须保留该 `null`，前端也只能对 `undefined` 做 legacy 兼容。FontAsset metadata PATCH 在 transaction 内锁内重读 canonical asset，只更新显式 display name、family、weight、style、enabled；禁用后的 selection 清理与资产更新同事务，迟到旧快照不能恢复其他字段。

字体槽位选择由独立 repository 职责按 typed slot 提交，只更新目标配置键；中文槽位与 legacy 兼容键在同一 transaction 内镜像。目标字体行使用 `FOR UPDATE` 与禁用/删除串行化：生命周期 mutation 在同一 transaction 更新资产并只条件清除仍引用该字体的槽位，数据库失败时资产、selection 和物理文件都保持原状。Admin 的 update/delete 响应同时返回 committed `selected_font_ids`；前端按槽位维护 generation，只合并发起槽位，禁用/删除会 fence 旧选择响应，同 action 的旧 `finally` 不能释放新 busy，失败后通过有 generation 的字体列表请求恢复 canonical 状态。该边界不引入通用配置事务框架或任务队列。

聊天正文的中文与 Latin 字体 face 使用互斥 `unicode-range` 建立字形所有权：CJK/Han/Kana/Hangul/full-width 区间由中文槽位负责，ASCII、Latin 扩展与常用西文符号由英文槽位负责，因此同时包含 ASCII 的 CJK 字体不会遮蔽英文配置。两者范围外继续落到系统 serif；代码槽位保持独立 `--chat-code-font-family` 且不加字符范围，以支持源码中的 Unicode 标识符、字符串和注释。该路由只改变浏览器 glyph 选择，不改变字体文件、槽位持久化、显式 null 或生命周期事务。

注册与登录共享认证错误边界：注册用户名、邮箱、昵称、密码和 preferences 的本地约束为稳定 400，用户名或邮箱重名为 409，登录的未知账号与错误密码统一为 `invalid_credentials` 401，待审核或停用账号为受控 401，限流为带 `Retry-After` 的 retryable 429；repository、注册事务、密码哈希与 token 签发故障为带 request ID 的 retryable 5xx。注册在数据库查询和 bcrypt 前完成可判定输入校验，repository 在实际 registration unique constraint owner 保留 conflict 分类；首用户管理员、后续用户待审批和现有限流计数/重置算法不变。

认证 middleware 在所有受保护 API 前重新读取当前活动账号与 auth version，不信任 token 中的用户名或角色。缺少/非法 Authorization header、无效 token、非法 claims 和已失效账号使用稳定 401 code，非管理员访问为稳定 403；账号状态 repository 故障为带 request ID 的 retryable 5xx，并保留底层 cause 供内部诊断而不进入响应。该契约不改变 JWT 七天有效期、legacy token 的 auth version 兼容或账号变更后的 run 取消行为。

`/admin/status` 只展示当前部署容器可见的版本、build ref、schema、Go 运行时、cgroup 内存、受管存储、PostgreSQL 和文档提取器状态。依赖探测短超时且相互独立，单项失败仍返回其余状态；页面只在进入或手动刷新时请求。它不读取 Docker Socket、宿主机监控信息、环境变量、服务地址、密钥或绝对路径。

管理员保存渠道、模型、外部服务或工具配置后，只影响新请求；已经运行中的 SSE / Agent run 不会中途切换凭据。

模型、渠道、外部服务、系统配置与 Tool 配置的普通 JSON 失败使用稳定的 `error`、`code`、`retryable` 契约。纯本地字段、模板或排序校验返回可修改的 400，资源不存在返回 404，模型重复创建返回 409，外部服务 probe 失败返回 502；repository、registry 和其他内部故障返回带 request ID 的 5xx。默认模型校验也必须保留数据库/渠道读取故障的内部分类，不能把运行故障伪装成“模型不存在”。只有受控本地校验文案可以公开，SQL、内部路径、provider 原文和凭据化 URL 只保留在服务端诊断。

## 发布入口

- 快速部署见 [Docker Compose 部署](docs/03-实施计划/Docker-Compose-部署.md)。
- 部署脚本不自行解析或执行 `.env.docker`；secret、数据库身份和 `DATA_DIR` 均消费 `docker compose config --environment` 的最终插值结果，使引号、注释、转义及 shell/`--env-file` 优先级与实际容器环境和 bind mount 保持一致。
- storage layout marker 是显式生命周期状态：旧 marker 兼容解释为 `migrated`，成功清理 legacy uploads 后原子记录为 `finalized`；rollback 只允许 `migrated` 且 legacy/restore 工件仍存在的状态，并在停止服务或执行 SQL 前拒绝 finalized、未知或缺失工件。
- `docker-build.sh up` 和 `reset-db` 均先完成镜像构建，再进入服务、migration 和 storage 切换；最终服务使用 `up --no-build --wait`，构建失败不会先停止现有服务或删除 reset 目标。
- 根 Docker build context 通过 `.dockerignore` 同时排除根级与嵌套的 data、storage、uploads、backups、logs、数据库导出、测试报告和本地审查/控制目录；源码、lockfile、公开 env example 与许可证材料仍是显式构建输入。该边界只减少发送给本地或远程 builder 的内容，不能替代 Git/Gitleaks 扫描。
- 导出源码和 dirty Git 工作树的 `BUILD_REF` 由同一输入清单计算，覆盖 backend/frontend/extractor、构建脚本、Dockerfile、Compose、`.dockerignore` 与根许可证/第三方声明；env、运行数据、日志、构建产物和文件时间戳不进入标识。干净 Git 构建继续使用 commit SHA，显式发布 workflow 继续使用完整 GitHub SHA。
- CI job 均有有限 wall-clock timeout；PostgreSQL integration 的启动与稳定性 readiness 使用有限次数并在超时时输出容器日志。Compose/container job 直接执行 export、dotenv、storage finalization、部署顺序、backup/restore、Docker context、BUILD_REF、release gate 与 Nginx 契约，并用固定 digest 的 ShellCheck 拒绝 warning 级 shell 缺陷；Python extractor 的 34 包 hash lock 由独立、同样 hash 锁定的 pip-audit 环境执行漏洞扫描，scanner 非零退出原样阻断 job。独立 Playwright job 使用临时 bind-mounted PostgreSQL/storage、虚构首用户管理员和复用 extractor 镜像的确定性 OpenAI-compatible 流式 stub，验证真实 DOCX 提取/预览/下载、首包后停止并刷新恢复、手动压缩/撤销；readiness/登录失败直接报错，失败上传 trace 与 Compose 日志，退出时不删除 volume 并断言无 Compose 容器遗留。这些门禁不依赖真实用户数据或外部模型。
- 备份入口不会复制在线 PGDATA：它优雅停止当前正在运行的 Web/backend/提取器，避免把跨数据库与文件系统的在途事务冻结在中间状态；随后在同一静止窗口生成 PostgreSQL custom-format dump 与 storage tar，并在版本、build ref、schema、PostgreSQL major、Compose checksum、migration 账本、工件 SHA-256 和逐文件清单齐全且自验证通过后原子发布，最后只恢复原先运行的服务；`.env.docker` 和 secret 不进入备份。
- restore 只接受与活动 `DATA_DIR` 无重叠的空目录，并用原子目录锁独占该目标，强制生成独立 Compose project、显式网络和 Docker 动态 loopback 端口。它先验证并安全解包 storage，再恢复到空 PostgreSQL、核对备份 migration 账本并运行当前统一 runner，检查数据库受管路径与磁盘文件，最后等待四个服务健康并输出隔离 URL；失败只对该隔离 project 执行不带 volume 删除的 `down` 并清理脚本创建的目标内容。
- 管理配置见 [管理员配置指南](docs/03-实施计划/管理员配置指南.md)。
- 公开导出见 [开源发布检查清单](docs/03-实施计划/开源发布检查清单.md)。
- 三个应用镜像在构建阶段分别从实际 Go 编译图、前端生产依赖树和 Python 锁定安装集生成第三方许可归档；缺少许可正文且没有精确版本 fallback 时构建失败。最终镜像只携带对应组件归档，基础镜像 OS/runtime 包继续以其上游镜像声明为边界。`scripts/check-image-licenses.sh` 会从最终镜像离线复制归档并校验 manifest、文件完整性和 SHA-256。
