package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
	"github.com/huoguojun123/EffChat/internal/tool"
	modelusage "github.com/huoguojun123/EffChat/internal/usage"
	"github.com/huoguojun123/EffChat/pkg/streaming"
)

// EinoAgent Eino ADK 驱动的 Agent
type EinoAgent struct {
	channelService *service.ChannelService
	toolService    *service.ToolConfigService

	// 对话压缩只按上下文 token 触发。
	// 早期版本还把 raw message 数交给 Eino ContextMessages，但工具调用链会产生大量
	// assistant/tool 过程消息，导致“128K token 尚未达到却提前压缩”。这里彻底移除
	// 消息数阈值，避免配置层再出现“轮数”这个误导性概念。
	compressMaxTokens  int
	configRepo         *repository.ConfigRepository
	memoryRepo         *repository.SessionMemoryRepository
	taskRunRepo        *repository.ModelTaskRunRepository
	fileRepo           *repository.FileRepository
	usageService       *modelusage.Service
	quotaService       *service.QuotaService
	memoryAutoClaims   sync.Map
	backgroundMu       sync.Mutex
	backgroundDraining bool
	memoryTasks        sync.WaitGroup
	backgroundCtx      context.Context
	backgroundCancel   context.CancelFunc
}

// NewEinoAgent 创建 Eino Agent 实例。
// 渠道、API key、搜索与爬虫服务都在管理员后台持久化配置。
// 每轮在 accepted 边界解析一次运行配置，并在真正执行前校验依赖未变化；
// 管理员保存的新配置只影响下一轮，不会静默改写已接收的 run。
// compressMaxTokens 为对话压缩 token 触发阈值（<=0 关闭压缩）。
func NewEinoAgent(channelService *service.ChannelService, toolService *service.ToolConfigService, compressMaxTokens int, configRepo *repository.ConfigRepository, memoryRepo *repository.SessionMemoryRepository, taskRunRepo *repository.ModelTaskRunRepository, fileRepo *repository.FileRepository, usageService *modelusage.Service, quotaService *service.QuotaService) *EinoAgent {
	// 全局把 eino ADK 内置提示词切到中文：压缩摘要的 preamble / continue-instruction /
	// system instruction 默认是英文（"This session is being continued..."），与中文界面不符。
	// 这是进程级开关，设一次即可；失败仅记日志，不影响 agent 构造。
	if err := adk.SetLanguage(adk.LanguageChinese); err != nil {
		log.Printf("[eino] 设置 ADK 中文提示词失败（保持英文）: %v", err)
	}
	backgroundCtx, backgroundCancel := context.WithCancel(context.Background())
	return &EinoAgent{
		channelService:    channelService,
		toolService:       toolService,
		compressMaxTokens: compressMaxTokens,
		configRepo:        configRepo,
		memoryRepo:        memoryRepo,
		taskRunRepo:       taskRunRepo,
		fileRepo:          fileRepo,
		usageService:      usageService,
		quotaService:      quotaService,
		backgroundCtx:     backgroundCtx,
		backgroundCancel:  backgroundCancel,
	}
}

func (a *EinoAgent) resolveSearchRuntimeConfig() service.SearchRuntimeConfig {
	cfg, _ := a.resolveSearchRuntimeConfigWithState()
	return cfg
}

func (a *EinoAgent) resolveSearchRuntimeConfigWithState() (service.SearchRuntimeConfig, service.SearchRuntimeConfigState) {
	if a == nil || a.channelService == nil {
		return service.NewChannelService(nil).ResolveSearchRuntimeConfigWithState()
	}
	return a.channelService.ResolveSearchRuntimeConfigWithState()
}

func hasSearchProvider(cfg service.SearchRuntimeConfig) bool {
	if strings.TrimSpace(cfg.SearchProvider) != "" {
		return true
	}
	return len(cfg.SearchProviders) > 0
}

// ChatRequest Agent 聊天请求
type ChatRequest struct {
	UserID            int64
	SessionID         int64
	Messages          []*model.Message
	ModelID           string
	Provider          string
	SystemName        string
	SystemPrompt      string
	Temperature       *float64
	TemperaturePolicy string
	TemperatureValue  *float64
	MaxTokens         int
	SchemaVersion     string
	MessageFormat     string
	SessionTitle      string
	SessionMetadata   []byte
	UserName          string
	UserNickname      string
	UserDisplayName   string
	UserRole          string
	UserPreferences   []byte
	EnabledSkills     []SkillInstruction
	PromptTime        time.Time

	RuntimeResolved          bool
	RuntimeChannel           *model.AIChannel
	RuntimePromptTemplate    string
	RuntimeMemory            *string
	RuntimeMemoryState       service.RuntimeConfigState
	RuntimeToolConfig        service.ToolRuntimeConfigSet
	RuntimeToolConfigState   service.RuntimeConfigState
	RuntimeSearchConfig      service.SearchRuntimeConfig
	RuntimeSearchConfigState service.SearchRuntimeConfigState
	// RuntimeExtractSummary* are the refinement policy and concrete utility
	// dependencies accepted with the run. ModelInfo and channel stay in memory;
	// the durable snapshot contains only their checksum, never channel secrets.
	// PrepareChat must consume these values after snapshot validation instead of
	// reopening live configuration and silently changing the content-sharing
	// boundary between durable admission and execution.
	RuntimeExtractSummaryEnabled   bool
	RuntimeExtractSummaryModel     string
	RuntimeExtractSummaryState     service.RuntimeConfigState
	RuntimeExtractSummaryModelInfo *modelbank.ModelInfo
	RuntimeExtractSummaryChannel   *model.AIChannel
	ContextWindow                  int
	ModelMaxOutput                 int
	Vision                         bool
	ToolUse                        bool
	SearchImpl                     modelbank.SearchImpl

	// Reasoning 来自模型配置，用于 auto thinking_format 判断双模式模型是否应开启思考。
	Reasoning bool
	// ThinkingFormat 是模型配置里的思考参数格式覆盖项。auto 表示按 model_id 推断。
	ThinkingFormat string
	// ThinkingEffort 是本轮消息选择的思考投入。空/auto 表示使用当前格式的默认档位。
	ThinkingEffort string
	// SuppressThinking is reserved for latency- and cost-bounded utility calls.
	// Adapters still select the model family's correct token-limit field, but
	// they must not attach optional thinking budgets that can expand or consume
	// the utility output allowance before the requested payload is produced.
	SuppressThinking bool

	// SearchMode 搜索模式：off / auto / on，默认 auto
	SearchMode modelbank.SearchMode
	// PreferModelNativeSearch 是否偏好模型原生搜索（默认 true）
	// true = 有原生搜索先用原生，不够时再改用应用搜索工具
	PreferModelNativeSearch bool

	// MemoryEnabled 会话记忆开关：true 时挂载 memory 工具并注入会话笔记
	MemoryEnabled bool

	// SkipUsage 用于管理员模型探测这类“控制面”调用。
	// 这些调用真实访问上游模型，但不属于用户对话、压缩或工具链业务用量；
	// 如果记入用量页，管理员调试错误配置时会把统计表污染得很难看。
	SkipUsage bool
}

type SkillInstruction struct {
	ID          string
	Name        string
	Description string
	Files       []model.SkillFile
}

// ChatResponse Agent 聊天响应
type ChatResponse struct {
	// Messages 本轮对话产生的全部消息，按生成顺序排列。
	// 含 assistant（可能带 tool_calls）、tool 结果、最终 assistant，
	// 由调用方逐条持久化以保证工具调用链完整可回放。
	Messages        []map[string]interface{}
	FinishReason    string
	Usage           *Usage
	DurationMs      int64
	TokensPerSecond float64
	Incomplete      bool

	// Canceled 标记本轮是被用户主动停止（context 取消）后保留的部分结果。
	// 调用方据此把 run 标记为 canceled 而非 completed。
	Canceled bool
}

// CompressionCheckpoint 压缩检查点：一次压缩产生的摘要及边界信息。
type CompressionCheckpoint struct {
	// SummaryData 摘要消息的 message_data（Eino schema.Message JSON）。
	SummaryData []byte
	// CompressBefore 边界：ID < 此值的消息应被标记为已压缩。
	// 实际值为压缩前最后一条消息的 ID + 1（即本轮新消息的第一条 ID）。
	// 如果拿不到精确边界，使用 0 表示"压缩本轮之前的所有消息"。
	CompressBefore int64
	Provider       string
	ModelID        string
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	CachedTokens     int `json:"cached_tokens,omitempty"`
	ReasoningTokens  int `json:"reasoning_tokens,omitempty"`
}

// PreparedChatRun owns only immutable setup output. In particular it must not
// retain the bounded setup context: the durable run context exclusively owns
// Runner execution, provider streaming, tools, retries, and cancellation.
//
// Fields remain private so only EinoAgent can execute or mutate the prepared
// plan; handlers may carry the opaque value across the setup boundary.
type PreparedChatRun struct {
	agent     *adk.ChatModelAgent
	messages  []*schema.Message
	writer    streaming.EventWriter
	provider  string
	modelID   string
	startedAt time.Time
}

// StreamChat keeps the historical single-context API for utility callers.
// Durable RunHub execution uses PrepareChat followed by RunPreparedChat so its
// bounded setup child cannot leak into provider streaming.
func (a *EinoAgent) StreamChat(ctx context.Context, req *ChatRequest, writer streaming.EventWriter) (*ChatResponse, error) {
	prepared, err := a.PrepareChat(ctx, req, writer)
	if err != nil {
		return nil, err
	}
	return a.RunPreparedChat(ctx, prepared)
}

// PrepareChat resolves and constructs everything required before model
// execution. The caller may cancel setupCtx as soon as this method succeeds;
// no returned value retains it.
func (a *EinoAgent) PrepareChat(setupCtx context.Context, req *ChatRequest, writer streaming.EventWriter) (*PreparedChatRun, error) {
	if setupCtx == nil {
		return nil, errors.New("chat setup context is required")
	}
	if req == nil {
		return nil, errors.New("chat request is required")
	}
	if writer == nil {
		return nil, errors.New("chat event writer is required")
	}
	startedAt := time.Now()
	plan := a.buildRuntimeContextPlan(req)
	toolRuntime := plan.toolRuntime
	searchDecision := plan.searchDecision
	searchRuntime := plan.searchRuntime

	// 2. 根据 provider 创建对应的 ChatModel（按搜索决策挂载原生搜索能力）
	chatModel, err := a.buildChatModel(setupCtx, req, searchDecision)
	if err != nil {
		return nil, err
	}

	// 3. 创建 ChatModelAgent
	memoryOn := req.MemoryEnabled && a.memoryRepo != nil && req.SessionID > 0 && toolRuntime.IsEnabled("memory") && runtimeMemoryAvailable(req.RuntimeMemoryState)
	var tools []einoTool.BaseTool

	// memory 工具：仅在会话开启记忆开关时挂载，让模型跨轮记住关键事实。
	if memoryOn {
		limits, err := a.memoryLimitsContext(setupCtx)
		if err != nil {
			return nil, fmt.Errorf("resolve memory limits: %w", err)
		}
		tools = append(tools, tool.NewMemoryToolWithMaxChars(a.memoryRepo, req.SessionID, req.UserID, limits.MaxChars))
	}
	// 文件工作区工具：文本附件默认只给模型一份清单，不再全文注入上下文。
	// 模型应先 file_list/file_search 定位，再 file_read 读取片段，避免大文件一轮塞爆。
	// 权限校验在 FileRepository.GetReadableFileForAgent 中完成，避免模型猜 file_id 越界读取。
	if a.fileRepo != nil && req.UserID > 0 && req.SessionID > 0 {
		tools = appendToolIfEnabled(tools, toolRuntime, "file_list", tool.NewFileListTool(a.fileRepo, req.UserID, req.SessionID))
		tools = appendToolIfEnabled(tools, toolRuntime, "file_search", tool.NewFileSearchTool(a.fileRepo, req.UserID, req.SessionID))
		tools = appendToolIfEnabled(tools, toolRuntime, "file_read", tool.NewFileReadTool(a.fileRepo, req.UserID, req.SessionID))
	}

	// Skill 工作区工具：新版 Skill 只把元数据放进提示词，正文落盘并由模型按需读取。
	// 这里传入的 EnabledSkills 已经由 SkillService 按用户组等级、启停状态和会话选择过滤；
	// 工具本身再只允许读取这份白名单里的文件，避免模型猜测路径越界读取未启用 Skill。
	if len(req.EnabledSkills) > 0 {
		workspace := make([]tool.SkillWorkspaceItem, 0, len(req.EnabledSkills))
		for _, skill := range req.EnabledSkills {
			workspace = append(workspace, tool.SkillWorkspaceItem{
				ID:          skill.ID,
				Name:        skill.Name,
				Description: skill.Description,
				Files:       skill.Files,
			})
		}
		tools = appendToolIfEnabled(tools, toolRuntime, "skill_list", tool.NewSkillListTool(workspace))
		tools = appendToolIfEnabled(tools, toolRuntime, "skill_search", tool.NewSkillSearchTool(workspace))
		tools = appendToolIfEnabled(tools, toolRuntime, "skill_read", tool.NewSkillReadTool(workspace))
	}

	//   - UseModelNativeSearch: 模型原生搜索（internal 透明 / params 传参）
	//   - UseApplicationTool:   应用搜索工具兜底（web_search / web_extract）
	if searchDecision.UseApplicationTool {
		if toolRuntime.IsEnabled("web_search") && hasSearchProvider(searchRuntime) {
			webSearchTool := tool.NewWebSearchTool(
				tool.WebSearchConfig{
					Provider:     searchRuntime.SearchProvider,
					Providers:    searchRuntime.SearchProviders,
					SearXNGURL:   searchRuntime.SearXNGURL,
					TavilyAPIKey: searchRuntime.TavilySearchAPIKey,
					TavilyURL:    searchRuntime.TavilySearchURL,
					BraveAPIKey:  searchRuntime.BraveSearchAPIKey,
					BraveURL:     searchRuntime.BraveSearchURL,
					ExaAPIKey:    searchRuntime.ExaSearchAPIKey,
					ExaURL:       searchRuntime.ExaSearchURL,
					BochaAPIKey:  searchRuntime.BochaSearchAPIKey,
					BochaURL:     searchRuntime.BochaSearchURL,
					MaxResults:   5,
					Timeout:      toolRuntime.Timeout("web_search"),
				},
			)
			tools = append(tools, webSearchTool)
		}
		if toolRuntime.IsEnabled("web_extract") {
			// 网页提炼：默认用独立小模型把抓取正文按 goal 提炼成要点（仿 Claude Code），
			// 避免整页正文塞满上下文。配置关闭或小模型构造失败时降级到截断（工具内默认 4000）。
			summarizer, summaryEnabled, err := a.buildExtractSummarizer(setupCtx, req)
			if err != nil {
				return nil, fmt.Errorf("prepare web extract refinement: %w", err)
			}
			webExtractTool := tool.NewWebExtractTool(tool.WebExtractConfig{
				CrawlerImpl:      searchRuntime.CrawlerImpl,
				CrawlerProviders: searchRuntime.CrawlerProviders,
				FirecrawlAPIKey:  searchRuntime.FirecrawlAPIKey,
				FirecrawlBaseURL: searchRuntime.FirecrawlBaseURL,
				JinaAPIKey:       searchRuntime.JinaAPIKey,
				JinaBaseURL:      searchRuntime.JinaBaseURL,
				TavilyAPIKey:     searchRuntime.TavilyExtractAPIKey,
				TavilyBaseURL:    searchRuntime.TavilyExtractURL,
				ExaAPIKey:        searchRuntime.ExaExtractAPIKey,
				ExaBaseURL:       searchRuntime.ExaExtractURL,
				Timeout:          toolRuntime.Timeout("web_extract"),
				Summarizer:       summarizer,
				SummaryEnabled:   summaryEnabled,
			})
			tools = append(tools, webExtractTool)
		}
	}

	mountedTools := make(map[string]bool, len(tools))
	for _, mountedTool := range tools {
		info, err := mountedTool.Info(setupCtx)
		if err != nil {
			return nil, fmt.Errorf("failed to inspect mounted tool: %w", err)
		}
		if info == nil || strings.TrimSpace(info.Name) == "" {
			return nil, errors.New("mounted tool has no name")
		}
		mountedTools[info.Name] = true
	}

	instruction, err := buildInstruction(a.configRepo, req, searchDecision, mountedTools)
	if err != nil {
		return nil, err
	}
	// 注入当前会话记忆（若有）：仅在会话开启记忆时带上，且不参与历史压缩，确保跨轮稳定可见。
	if memoryOn {
		if req.RuntimeMemory != nil {
			instruction = appendMemoryInstruction(instruction, *req.RuntimeMemory)
		} else if runtimeMemoryAvailable(req.RuntimeMemoryState) {
			mem, err := a.memoryRepo.Get(req.SessionID)
			if err == nil {
				instruction = appendMemoryInstruction(instruction, mem)
			}
		}
	}

	agentConfig := &adk.ChatModelAgentConfig{
		Model:         chatModel,
		Instruction:   instruction,
		MaxIterations: defaultAgentMaxIterations,
		ModelRetryConfig: transientModelRetryConfig(func(trace ModelRetryTrace) {
			if writer == nil {
				return
			}
			_ = writer.WriteEvent(streaming.EventModelRetry, streaming.ModelRetryEvent{
				Attempt:     trace.Attempt,
				MaxAttempts: trace.MaxAttempts,
				DelayMs:     trace.Delay.Milliseconds(),
				Category:    string(trace.Category),
			})
		}),
	}

	var toolBudget *toolBudgetMiddleware
	if len(tools) > 0 {
		toolBudget = newToolBudgetMiddleware(req)
		agentConfig.ToolsConfig = adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: tools,
				// 模型有时会调用未挂载的工具：跨轮切换搜索开关后历史里残留的
				// web_search/web_extract 调用被重放，或模型凭训练习惯臆造工具名。
				// 默认行为是直接报错并中断整条流；这里改为优雅降级——返回一条
				// 工具结果告知模型该工具不可用，让它据此改用其他方式继续作答。
				UnknownToolsHandler: unknownToolHandler,
			},
		}
		agentConfig.ToolsConfig.ToolCallMiddlewares = []compose.ToolMiddleware{toolGovernanceMiddleware(toolRuntime, a.quotaService, a.usageService, toolBudget)}
	}
	// 注：UseModelNativeSearch 的 params 型传参逻辑由各 provider adapter 处理
	// （如 Gemini 的 google_search、Qwen 的 enable_search），internal 型无需处理。
	// 应用工具是否被实际调用，交由模型根据系统指令自主决定：原生优先，不足再兜底。

	if len(tools) > 0 {
		agentConfig.Handlers = append(agentConfig.Handlers, toolBudget)
	}

	agent, err := adk.NewChatModelAgent(setupCtx, agentConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create agent: %w", err)
	}

	// 5. 转换历史消息为 Eino 格式
	visionCapable := req.Vision
	if !req.RuntimeResolved {
		visionCapable = modelbank.GetOrDefault(req.ModelID, req.Provider).Capabilities.Vision
	}
	einoMessages, err := convertToEinoMessages(req.Messages, visionCapable)
	if err != nil {
		return nil, fmt.Errorf("failed to convert messages: %w", err)
	}

	return &PreparedChatRun{
		agent:     agent,
		messages:  einoMessages,
		writer:    writer,
		provider:  req.Provider,
		modelID:   req.ModelID,
		startedAt: startedAt,
	}, nil
}

// RunPreparedChat executes a successfully prepared chat with the durable run
// context. Runner construction intentionally lives here because Eino lazily
// builds its execution graph on the first Run call.
func (a *EinoAgent) RunPreparedChat(runCtx context.Context, prepared *PreparedChatRun) (*ChatResponse, error) {
	if runCtx == nil {
		return nil, errors.New("durable run context is required")
	}
	if prepared == nil || prepared.agent == nil {
		return nil, errors.New("prepared chat run is required")
	}

	// 6. 创建 Runner 并执行
	// 必须显式开启 EnableStreaming，否则 ADK 默认走非流式 Invoke/Generate，
	// 上游 OpenAI 兼容网关会收到非流式请求，前端也只能在 message_complete 时整块拿到内容。
	runner := adk.NewRunner(runCtx, adk.RunnerConfig{
		Agent:           prepared.agent,
		EnableStreaming: true,
	})
	iter := runner.Run(runCtx, prepared.messages)

	emit := func(event string, data interface{}) error {
		_ = prepared.writer.WriteEvent(event, data)
		return nil
	}

	// 7. 处理流式事件
	produced := make([]map[string]interface{}, 0, 3)
	var usage *Usage
	finishReason := string(FinishReasonUnknown)
	canceled := false

	for {
		event, ok := iter.Next()
		if !ok {
			break
		}

		// 错误处理：WillRetryError 表示框架正在重试，跳过即可
		if event.Err != nil {
			var willRetry *adk.WillRetryError
			if errors.As(event.Err, &willRetry) {
				continue
			}
			if service.RunCancelCauseFromContext(runCtx) != "" && runCtx.Err() != nil {
				canceled = true
				finishReason = "canceled"
				break
			}
			return partialChatResponse(produced, interruptedFinishReason(finishReason), usage), sanitizeModelRuntimeError(prepared.provider, prepared.modelID, event.Err)
		}

		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}
		mv := event.Output.MessageOutput

		// 工具结果事件：单独处理，不混入助手正文
		if mv.Role == schema.Tool {
			toolMsg, err := mv.GetMessage()
			if err != nil {
				return nil, fmt.Errorf("failed to read tool result: %w", err)
			}
			if err := emit(streaming.EventToolCallResult, streaming.ToolCallResultEvent{
				ToolCallID: toolMsg.ToolCallID,
				Result:     toolMsg.Content,
			}); err != nil {
				return nil, err
			}
			produced = append(produced, messageToData(toolMsg))
			continue
		}

		// 助手事件
		fullMsg, err := a.consumeAssistantEvent(runCtx, mv, emit)
		if err != nil {
			if fullMsg != nil {
				produced = append(produced, messageToData(fullMsg))
			}
			if service.RunCancelCauseFromContext(runCtx) != "" && runCtx.Err() != nil {
				canceled = true
				finishReason = "canceled"
				break
			}
			return partialChatResponse(produced, interruptedFinishReason(finishReason), usage), err
		}
		if fullMsg == nil {
			continue // 重试中被丢弃的部分流
		}

		// 工具调用：拼接完成后发 tool_call_start
		for _, tc := range fullMsg.ToolCalls {
			if err := emit(streaming.EventToolCallStart, streaming.ToolCallStartEvent{
				ToolCallID: tc.ID,
				ToolName:   tc.Function.Name,
			}); err != nil {
				return nil, err
			}
		}

		normalizedFinish := normalizeProviderFinishReason("", len(fullMsg.ToolCalls) > 0)
		if fullMsg.ResponseMeta != nil {
			normalizedFinish = normalizeProviderFinishReason(fullMsg.ResponseMeta.FinishReason, len(fullMsg.ToolCalls) > 0)
			if u := fullMsg.ResponseMeta.Usage; u != nil {
				usage = usageFromTokenUsage(u)
			}
		}

		finishReason = string(normalizedFinish.Canonical)
		messageData := messageToData(fullMsg)
		applyNormalizedFinishReason(messageData, normalizedFinish)
		produced = append(produced, messageData)
	}

	if usage == nil {
		usage = &Usage{}
	}
	duration := time.Since(prepared.startedAt)
	var tokensPerSecond float64
	if duration > 0 && usage.CompletionTokens > 0 {
		tokensPerSecond = float64(usage.CompletionTokens) / duration.Seconds()
	}
	if canceled {
		produced = canonicalizePartialProducedMessages(produced)
	} else {
		produced = canonicalizeProducedMessages(produced)
	}
	if !canceled && !hasDisplayableAssistantOutput(produced) {
		if fallback, ok := materializeReasoningOnlyAssistantOutput(produced); ok {
			log.Printf("[eino] reasoning-only assistant output materialized: provider=%s model=%s finish_reason=%s prompt_tokens=%d completion_tokens=%d reasoning_tokens=%d",
				prepared.provider, prepared.modelID, finishReason, usage.PromptTokens, usage.CompletionTokens, usage.ReasoningTokens)
			if err := emit(streaming.EventContentDelta, streaming.ContentDeltaEvent{Delta: fallback}); err != nil {
				return nil, err
			}
		} else {
			return nil, newModelEmptyResponseError(prepared.provider, prepared.modelID, finishReason, usage)
		}
	}
	attachRuntimeMeta(produced, duration.Milliseconds(), tokensPerSecond)

	return &ChatResponse{
		Messages:        produced,
		FinishReason:    finishReason,
		Usage:           usage,
		DurationMs:      duration.Milliseconds(),
		TokensPerSecond: tokensPerSecond,
		Incomplete:      completionIsIncomplete(FinishReason(finishReason)),
		Canceled:        canceled,
	}, nil
}

func partialChatResponse(produced []map[string]interface{}, finishReason string, usage *Usage) *ChatResponse {
	if len(produced) == 0 {
		return nil
	}
	return &ChatResponse{
		Messages:     canonicalizePartialProducedMessages(produced),
		FinishReason: finishReason,
		Usage:        usage,
	}
}

func interruptedFinishReason(finishReason string) string {
	if finishReason == "" || finishReason == string(FinishReasonStop) || finishReason == string(FinishReasonUnknown) {
		return "error"
	}
	return finishReason
}
