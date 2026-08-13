package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino-ext/components/model/claude"
	"github.com/cloudwego/eino-ext/components/model/gemini"
	"github.com/cloudwego/eino-ext/components/model/openai"
	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/modelstream"
	"github.com/huoguojun123/EffChat/internal/openairesponses"
	"github.com/huoguojun123/EffChat/internal/providerhttp"
	"github.com/huoguojun123/EffChat/internal/repository"
	modelusage "github.com/huoguojun123/EffChat/internal/usage"
	"google.golang.org/genai"
)

// TitleService 会话标题生成服务
type TitleService struct {
	sessionRepo        *repository.SessionRepository
	messageRepo        *repository.MessageRepository
	configRepo         *repository.ConfigRepository
	channels           *ChannelService
	usage              *modelusage.Service
	taskRuns           *repository.ModelTaskRunRepository
	claims             sync.Map
	backgroundMu       sync.Mutex
	backgroundDraining bool
	backgroundTasks    sync.WaitGroup
	backgroundCtx      context.Context
	backgroundCancel   context.CancelFunc
}

// NewTitleService 创建标题服务
func NewTitleService(sessionRepo *repository.SessionRepository, messageRepo *repository.MessageRepository, configRepo *repository.ConfigRepository, channels *ChannelService, usageServices ...*modelusage.Service) *TitleService {
	var usageService *modelusage.Service
	if len(usageServices) > 0 {
		usageService = usageServices[0]
	}
	backgroundCtx, backgroundCancel := context.WithCancel(context.Background())
	return &TitleService{
		sessionRepo:      sessionRepo,
		messageRepo:      messageRepo,
		configRepo:       configRepo,
		channels:         channels,
		usage:            usageService,
		backgroundCtx:    backgroundCtx,
		backgroundCancel: backgroundCancel,
	}
}

func (s *TitleService) SetTaskRunRepository(repo *repository.ModelTaskRunRepository) {
	if s != nil {
		s.taskRuns = repo
	}
}

func (s *TitleService) SystemName() string {
	if s == nil || s.configRepo == nil {
		return "EffChat"
	}
	return s.configRepo.GetString("system_name", "EffChat")
}

// GenerateTitle 生成会话标题
// 在用户第二条消息后触发
func (s *TitleService) GenerateTitle(ctx context.Context, sessionID, userID int64) error {
	if s == nil || sessionID <= 0 || userID <= 0 {
		return nil
	}
	if _, loaded := s.claims.LoadOrStore(sessionID, struct{}{}); loaded {
		return nil
	}
	defer s.claims.Delete(sessionID)

	controlCtx, controlCancel := titleControlContext(ctx)
	// 1. 检查会话是否已有自定义标题
	session, err := s.sessionRepo.GetByIDContext(controlCtx, sessionID, userID)
	if err != nil {
		controlCancel()
		return fmt.Errorf("session not found: %w", err)
	}

	// 如果标题不是"新对话"，说明用户已自定义，不自动生成
	if session.Title != "新对话" && session.Title != "New Conversation" {
		controlCancel()
		return nil
	}
	expectedAnswerSelectionRevision := session.AnswerSelectionRevision

	// 2. 获取会话的前两轮对话（user + assistant）
	messages, err := s.messageRepo.ListBySessionContext(controlCtx, sessionID)
	controlCancel()
	if err != nil {
		return fmt.Errorf("failed to load messages: %w", err)
	}

	if len(messages) < 2 {
		// 消息不足，使用第一条消息截断
		return s.useFallbackTitleBounded(ctx, sessionID, userID, messages, &expectedAnswerSelectionRevision)
	}

	// 3. 构造标题生成提示词
	conversationText := s.buildTitleSeed(messages)
	if strings.TrimSpace(conversationText) == "" {
		return s.useFallbackTitleBounded(ctx, sessionID, userID, messages, &expectedAnswerSelectionRevision)
	}

	// 4. 调用管理员配置的轻量模型生成标题
	generated, err := s.generateTitleWithModel(ctx, sessionID, userID, conversationText)
	if err != nil {
		// 生成失败，使用 fallback
		return s.useFallbackTitleBounded(ctx, sessionID, userID, messages, &expectedAnswerSelectionRevision)
	}

	// 5. 更新会话标题
	persistCtx, persistCancel := titleControlContext(ctx)
	updated, err := s.sessionRepo.UpdateAutomaticTitleAtAnswerRevision(persistCtx, sessionID, userID, generated.Title, true, expectedAnswerSelectionRevision)
	persistCancel()
	if err != nil {
		s.recordTitleTaskRun(ctx, sessionID, userID, generated.Profile, generated.StartedAt, repository.ModelTaskStatusFailed, err)
		return fmt.Errorf("failed to update title: %w", err)
	}
	if !updated {
		s.recordTitleTaskRun(ctx, sessionID, userID, generated.Profile, generated.StartedAt, repository.ModelTaskStatusSkipped, nil)
		return repository.ErrAnswerSelectionRevisionConflict
	}
	s.recordTitleTaskRun(ctx, sessionID, userID, generated.Profile, generated.StartedAt, repository.ModelTaskStatusSuccess, nil)

	return nil
}

func (s *TitleService) GenerateTitleAsync(sessionID, userID int64) {
	if s == nil || !s.startBackgroundTask() {
		return
	}
	go func() {
		defer s.backgroundTasks.Done()
		ctx := s.backgroundTaskContext()
		controlCtx, controlCancel := titleControlContext(ctx)
		shouldGenerate, err := s.ShouldGenerateTitle(controlCtx, sessionID, userID)
		controlCancel()
		if err != nil {
			log.Printf("[title_generation] evaluate trigger failed: session=%d err=%v", sessionID, err)
			return
		}
		if shouldGenerate {
			if err := s.GenerateTitle(ctx, sessionID, userID); err != nil && !errors.Is(err, repository.ErrAnswerSelectionRevisionConflict) {
				log.Printf("[title_generation] generate failed: session=%d err=%v", sessionID, err)
			}
		}
	}()
}

func (s *TitleService) startBackgroundTask() bool {
	s.backgroundMu.Lock()
	defer s.backgroundMu.Unlock()
	if s.backgroundDraining {
		return false
	}
	s.ensureBackgroundContextLocked()
	s.backgroundTasks.Add(1)
	return true
}

func (s *TitleService) backgroundTaskContext() context.Context {
	s.backgroundMu.Lock()
	defer s.backgroundMu.Unlock()
	s.ensureBackgroundContextLocked()
	return s.backgroundCtx
}

func (s *TitleService) ensureBackgroundContextLocked() {
	if s.backgroundCtx != nil {
		return
	}
	s.backgroundCtx, s.backgroundCancel = context.WithCancel(context.Background())
}

func (s *TitleService) DrainBackgroundTasks(ctx context.Context) bool {
	if s == nil {
		return true
	}
	s.backgroundMu.Lock()
	s.backgroundDraining = true
	cancel := s.backgroundCancel
	s.backgroundMu.Unlock()
	if cancel != nil {
		cancel()
	}
	done := make(chan struct{})
	go func() {
		s.backgroundTasks.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}

const (
	titleSeedMaxMessages  = 4
	titleSeedMaxPerMsg    = 200
	titleSeedMaxTotal     = 1000
	fallbackTitleMaxRunes = 15
	titleControlTimeout   = 10 * time.Second
)

// titleControlContext bounds repository, configuration, provider construction,
// and final persistence without placing a total deadline around model output.
func titleControlContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, titleControlTimeout)
}

// buildTitleSeed 构造标题生成的极小上下文。
//
// 标题模型只需要知道“这段对话大概在聊什么”，不应该看到附件清单、工具过程、
// 压缩摘要或长文档片段。这里按白名单取前几条 user/assistant 正文，并同时限制
// 单条长度和总长度，避免上传论文后标题生成 prompt 被顺手拖大。
func (s *TitleService) buildTitleSeed(messages []*model.Message) string {
	var builder strings.Builder
	used := 0

	for _, msg := range messages {
		role, content, ok := titleSeedMessageContent(msg)
		if !ok {
			continue
		}
		content = truncateTitleRunes(compactTitleWhitespace(content), titleSeedMaxPerMsg)

		if role == "user" {
			builder.WriteString("User: ")
		} else if role == "assistant" {
			builder.WriteString("Assistant: ")
		}

		builder.WriteString(content)
		builder.WriteString("\n\n")
		used++

		runes := []rune(builder.String())
		if len(runes) >= titleSeedMaxTotal {
			return string(runes[:titleSeedMaxTotal])
		}
		if used >= titleSeedMaxMessages {
			break
		}
	}

	return builder.String()
}

func titleSeedMessageContent(msg *model.Message) (string, string, bool) {
	if msg == nil || msg.HasToolCalls || len(msg.MessageData) == 0 {
		return "", "", false
	}
	var messageData map[string]interface{}
	if err := json.Unmarshal(msg.MessageData, &messageData); err != nil {
		return "", "", false
	}
	role, _ := messageData["role"].(string)
	if role == "" {
		role = msg.Role
	}
	if role != "user" && role != "assistant" {
		return "", "", false
	}
	if isTitleCompactionSummaryData(messageData) || isTitleEphemeralErrorData(messageData) || hasToolCallsInTitleData(messageData) {
		return "", "", false
	}
	content, _ := messageData["content"].(string)
	content = strings.TrimSpace(content)
	if content == "" {
		return "", "", false
	}
	return role, content, true
}

func isTitleCompactionSummaryData(data map[string]interface{}) bool {
	extra, ok := data["extra"].(map[string]interface{})
	if ok {
		ct, _ := extra["_eino_summarization_content_type"].(string)
		if ct == "summary" {
			return true
		}
	}
	meta, ok := data["metadata"].(map[string]interface{})
	if !ok {
		return false
	}
	flag, _ := meta["compaction_summary"].(bool)
	return flag
}

func isTitleEphemeralErrorData(data map[string]interface{}) bool {
	meta, ok := data["metadata"].(map[string]interface{})
	if !ok {
		return false
	}
	value, _ := meta["ephemeral_error"].(bool)
	return value
}

func hasToolCallsInTitleData(data map[string]interface{}) bool {
	toolCalls, ok := data["tool_calls"].([]interface{})
	return ok && len(toolCalls) > 0
}

func compactTitleWhitespace(content string) string {
	return strings.Join(strings.Fields(content), " ")
}

func truncateTitleRunes(content string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(content)
	if len(runes) <= max {
		return content
	}
	return string(runes[:max]) + "..."
}

func truncateTitleRunesPlain(content string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(content)
	if len(runes) <= max {
		return content
	}
	return string(runes[:max])
}

// generateTitleWithModel 使用模型生成标题
type generatedTitle struct {
	Title     string
	Profile   titleModelProfile
	StartedAt time.Time
}

const (
	titleFirstOutputTimeout = 15 * time.Second
	titleMaxOutputTokens    = 64
)

func (s *TitleService) generateTitleWithModel(ctx context.Context, sessionID, userID int64, conversationText string) (*generatedTitle, error) {
	started := time.Now()

	setupCtx, setupCancel := titleControlContext(ctx)
	profile, err := s.resolveTitleModelProfile(setupCtx)
	if err != nil {
		setupCancel()
		s.recordTitleTaskRun(ctx, sessionID, userID, titleModelProfile{}, started, repository.ModelTaskStatusFailed, err)
		return nil, err
	}

	baseModel, err := s.buildTitleChatModel(setupCtx, profile.Provider, profile.ModelID)
	setupCancel()
	if err != nil {
		err = fmt.Errorf("failed to create chat model: %w", err)
		s.recordTitleTaskRun(ctx, sessionID, userID, profile, started, repository.ModelTaskStatusFailed, err)
		return nil, err
	}
	chatModel := modelusage.WrapChatModel(baseModel, s.usage, modelusage.Meta{
		UserID:    userID,
		SessionID: sessionID,
		Kind:      modelusage.KindTitle,
		Provider:  profile.Provider,
		ModelID:   profile.ModelID,
	})
	ctx = modelusage.WithMeta(ctx, modelusage.Meta{Kind: modelusage.KindTitle, UserID: userID, SessionID: sessionID})

	// 构造消息
	messages := []*schema.Message{
		{
			Role: schema.System,
			Content: `You generate compact chat titles from a short conversation seed.

Requirements:
- Use the same language as the conversation.
- Capture the actual task or topic, not generic words like "question" or "discussion".
- 2-6 English words, or 4-12 Chinese characters when the conversation is Chinese.
- No quotes, emoji, markdown, trailing punctuation, or explanation.
- Prefer a noun phrase over a sentence.
- If the seed contains multiple topics, title the latest concrete user task.
- Examples:
  - "Go 并发优化"
  - "React Performance Tips"
  - "数据库设计建议"`,
		},
		{
			Role:    schema.User,
			Content: fmt.Sprintf("Conversation:\n\n%s\n\nGenerate a title:", conversationText),
		},
	}

	// 标题与主对话走同一个底层 Stream 契约。固定预算只等待首个有效输出；
	// 首包到达后继续收完整响应，避免慢模型在已经开始生成时被总时长截断。
	resp, err := modelstream.Collect(ctx, chatModel, messages, titleFirstOutputTimeout)
	if err != nil {
		err = fmt.Errorf("title model stream failed: %w", err)
		s.recordTitleTaskRun(ctx, sessionID, userID, profile, started, repository.ModelTaskStatusFailed, err)
		return nil, err
	}
	if resp == nil {
		err = fmt.Errorf("empty title generated")
		s.recordTitleTaskRun(ctx, sessionID, userID, profile, started, repository.ModelTaskStatusFailed, err)
		return nil, err
	}

	title := strings.TrimSpace(resp.Content)

	// 清理标题
	title = strings.Trim(title, `"'`)
	title = strings.TrimSuffix(title, ".")

	// 限制长度
	if len([]rune(title)) > 50 {
		runes := []rune(title)
		title = string(runes[:50])
	}

	if title == "" {
		err = fmt.Errorf("empty title generated")
		s.recordTitleTaskRun(ctx, sessionID, userID, profile, started, repository.ModelTaskStatusFailed, err)
		return nil, err
	}

	return &generatedTitle{Title: title, Profile: profile, StartedAt: started}, nil
}

func (s *TitleService) recordTitleTaskRun(ctx context.Context, sessionID, userID int64, profile titleModelProfile, started time.Time, status string, err error) {
	if s == nil || s.taskRuns == nil {
		return
	}
	recordCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var retryAfter *time.Time
	errorType, errorMessage := "", ""
	if err != nil {
		errorType = modelusage.ErrorType(err)
		errorMessage = err.Error()
		// User stop and service drain are lifecycle events, not evidence that
		// the configured title model is unhealthy.
		if ctx == nil || ctx.Err() == nil {
			t := time.Now().Add(30 * time.Minute)
			retryAfter = &t
		}
	}
	_, recordErr := s.taskRuns.Record(recordCtx, repository.RecordModelTaskRunInput{
		TaskKey:      repository.ModelTaskTitleGeneration,
		UserID:       userID,
		SessionID:    sessionID,
		Source:       repository.ModelTaskSourceAuto,
		Status:       status,
		Provider:     profile.Provider,
		ModelID:      profile.ModelID,
		TargetType:   "session",
		TargetID:     fmt.Sprint(sessionID),
		ErrorType:    errorType,
		ErrorMessage: errorMessage,
		RetryAfter:   retryAfter,
		StartedAt:    started,
		FinishedAt:   time.Now(),
	})
	if recordErr != nil {
		log.Printf("[model_task_runs] record title_generation failed: session=%d err=%v", sessionID, recordErr)
	}
}

type titleModelProfile struct {
	ModelID  string
	Provider string
}

func (s *TitleService) resolveTitleModelProfile(ctx context.Context) (titleModelProfile, error) {
	modelID := "gpt-4o-mini"
	if s != nil && s.configRepo != nil {
		var err error
		modelID, err = s.configRepo.GetStringContext(ctx, "title_generation_model", modelID)
		if err != nil {
			return titleModelProfile{}, err
		}
	}
	modelID = strings.TrimSpace(modelID)
	if modelID != "" {
		if info := modelbank.Get(modelID); info != nil && info.Enabled && s.titleChannelAvailable(ctx, info.Provider) {
			return titleModelProfile{ModelID: info.ID, Provider: info.Provider}, nil
		}
		if cause := context.Cause(ctx); cause != nil {
			return titleModelProfile{}, cause
		}
	}

	candidates := modelbank.List()
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Provider == candidates[j].Provider {
			return candidates[i].ID < candidates[j].ID
		}
		return candidates[i].Provider < candidates[j].Provider
	})
	for _, info := range candidates {
		if info != nil && info.Enabled && s.titleChannelAvailable(ctx, info.Provider) {
			return titleModelProfile{ModelID: info.ID, Provider: info.Provider}, nil
		}
		if cause := context.Cause(ctx); cause != nil {
			return titleModelProfile{}, cause
		}
	}
	if modelID == "" {
		return titleModelProfile{}, fmt.Errorf("no enabled title model has an available channel")
	}
	return titleModelProfile{}, fmt.Errorf("title model %q is unavailable and no fallback model has an available channel", modelID)
}

func (s *TitleService) titleChannelAvailable(ctx context.Context, channelKey string) bool {
	if s == nil || s.channels == nil || strings.TrimSpace(channelKey) == "" {
		return false
	}
	_, err := s.channels.ResolveAIChannelContext(ctx, channelKey)
	return err == nil
}

func (s *TitleService) buildTitleChatModel(ctx context.Context, channelKey, modelID string) (einoModel.ToolCallingChatModel, error) {
	if s == nil || s.channels == nil {
		return nil, fmt.Errorf("model channel configuration is not available")
	}
	channel, err := s.channels.ResolveAIChannelContext(ctx, channelKey)
	if err != nil {
		return nil, err
	}

	switch channel.Adapter {
	case AdapterOpenAICompatible:
		return openai.NewChatModel(ctx, buildTitleOpenAIConfig(channel, modelID))
	case AdapterOpenAIResponses:
		return openairesponses.NewChatModel(ctx, &openairesponses.Config{
			APIKey:    channel.APIKey,
			BaseURL:   channel.BaseURL,
			Model:     modelID,
			MaxTokens: intPtr(titleMaxOutputTokens),
		})
	case AdapterAnthropic:
		cfg := &claude.Config{
			APIKey:     channel.APIKey,
			Model:      modelID,
			MaxTokens:  titleMaxOutputTokens,
			HTTPClient: providerhttp.NewAnthropicSingleAttemptClient(nil),
		}
		if channel.BaseURL != "" {
			baseURL := channel.BaseURL
			cfg.BaseURL = &baseURL
		}
		return claude.NewChatModel(ctx, cfg)
	case AdapterGoogle:
		clientCfg := &genai.ClientConfig{APIKey: channel.APIKey}
		if channel.BaseURL != "" {
			clientCfg.HTTPOptions = genai.HTTPOptions{BaseURL: channel.BaseURL}
		}
		client, err := genai.NewClient(ctx, clientCfg)
		if err != nil {
			return nil, err
		}
		return gemini.NewChatModel(ctx, &gemini.Config{
			Client:    client,
			Model:     modelID,
			MaxTokens: intPtr(titleMaxOutputTokens),
		})
	default:
		return nil, fmt.Errorf("unsupported adapter %q for channel %q", channel.Adapter, channel.Key)
	}
}

func buildTitleOpenAIConfig(channel *model.AIChannel, modelID string) *openai.ChatModelConfig {
	cfg := &openai.ChatModelConfig{Model: modelID}
	if channel != nil {
		cfg.APIKey = channel.APIKey
		cfg.BaseURL = channel.BaseURL
	}
	provider := ""
	if channel != nil {
		provider = channel.Key
	}
	format := modelbank.ResolveThinkingFormat(provider, modelID, "auto", true)
	if format == modelbank.ThinkingFormatOpenAIReasoningEffort || format == modelbank.ThinkingFormatOpenAIGPT56 {
		cfg.MaxCompletionTokens = intPtr(titleMaxOutputTokens)
	} else {
		cfg.MaxTokens = intPtr(titleMaxOutputTokens)
	}
	if format == modelbank.ThinkingFormatOpenAIGPT56 {
		cfg.ExtraFields = map[string]any{"reasoning_effort": "none"}
	}
	return cfg
}

func intPtr(v int) *int {
	return &v
}

// useFallbackTitle 使用第一条用户消息作为标题
func (s *TitleService) useFallbackTitle(ctx context.Context, sessionID, userID int64, messages []*model.Message, expectedAnswerSelectionRevision *int64) error {
	var title string

	// 找第一条用户消息
	for _, msg := range messages {
		role, content, ok := titleSeedMessageContent(msg)
		if ok && role == "user" {
			title = truncateTitleRunesPlain(compactTitleWhitespace(content), fallbackTitleMaxRunes)
			break
		}
	}

	if title == "" {
		title = "新对话"
	}

	if expectedAnswerSelectionRevision == nil {
		if err := s.sessionRepo.UpdateAutomaticTitleContext(ctx, sessionID, userID, title, false); err != nil {
			return fmt.Errorf("failed to update title: %w", err)
		}
		return nil
	}
	updated, err := s.sessionRepo.UpdateAutomaticTitleAtAnswerRevision(ctx, sessionID, userID, title, false, *expectedAnswerSelectionRevision)
	if err != nil {
		return fmt.Errorf("failed to update title: %w", err)
	}
	if !updated {
		return repository.ErrAnswerSelectionRevisionConflict
	}
	return nil
}

func (s *TitleService) useFallbackTitleBounded(parent context.Context, sessionID, userID int64, messages []*model.Message, expectedAnswerSelectionRevision *int64) error {
	ctx, cancel := titleControlContext(parent)
	defer cancel()
	return s.useFallbackTitle(ctx, sessionID, userID, messages, expectedAnswerSelectionRevision)
}

// ShouldGenerateTitle 判断是否应该生成标题
// 规则：用户第二条真实消息后触发（不统计压缩摘要消息）
func (s *TitleService) ShouldGenerateTitle(ctx context.Context, sessionID, userID int64) (bool, error) {
	session, err := s.sessionRepo.GetByIDContext(ctx, sessionID, userID)
	if err != nil {
		return false, err
	}
	if session.Title != "新对话" && session.Title != "New Conversation" {
		return false, nil
	}
	if s.taskRuns != nil {
		cooling, err := s.taskRuns.LatestAutoCooldown(ctx, sessionID, userID, repository.ModelTaskTitleGeneration, time.Now())
		if err != nil {
			return false, err
		}
		if cooling != nil {
			return false, nil
		}
	}
	messages, err := s.messageRepo.ListBySessionContext(ctx, sessionID)
	if err != nil {
		return false, err
	}

	userMessageCount := 0
	for _, msg := range messages {
		var messageData map[string]interface{}
		if err := json.Unmarshal(msg.MessageData, &messageData); err != nil {
			continue
		}

		role, _ := messageData["role"].(string)
		if role != "user" {
			continue
		}

		// 跳过 eino 压缩摘要消息（标记在 extra 字段中）
		if extra, ok := messageData["extra"].(map[string]interface{}); ok {
			if ct, _ := extra["_eino_summarization_content_type"].(string); ct == "summary" {
				continue
			}
		}

		userMessageCount++
	}

	trigger := 2
	if s.configRepo != nil {
		trigger, err = s.configRepo.GetIntContext(ctx, "title_generation_trigger", 2)
		if err != nil {
			return false, err
		}
	}
	if trigger <= 0 {
		return false, nil
	}

	return userMessageCount >= trigger, nil
}
