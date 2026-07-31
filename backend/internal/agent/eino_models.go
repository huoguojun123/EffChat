package agent

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/cloudwego/eino-ext/components/model/claude"
	"github.com/cloudwego/eino-ext/components/model/gemini"
	"github.com/cloudwego/eino-ext/components/model/openai"
	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/modelstream"
	"github.com/huoguojun123/EffChat/internal/service"
	"github.com/huoguojun123/EffChat/internal/tool"
	modelusage "github.com/huoguojun123/EffChat/internal/usage"
	"google.golang.org/genai"
)

// buildChatModel 根据管理员后台配置的渠道构造对应的 ChatModel。
// searchDecision 用于 gemini 等支持原生搜索的 provider 在构造时挂载搜索能力。
func (a *EinoAgent) buildChatModel(ctx context.Context, req *ChatRequest, searchDecision modelbank.SearchDecision) (einoModel.ToolCallingChatModel, error) {
	if a == nil || a.channelService == nil {
		return nil, fmt.Errorf("model channel configuration is not available")
	}

	channel := req.RuntimeChannel
	if channel == nil {
		var err error
		channel, err = a.channelService.ResolveAIChannel(req.Provider)
		if err != nil {
			return nil, err
		}
	}

	switch channel.Adapter {
	case service.AdapterOpenAICompatible:
		cfg := &openai.ChatModelConfig{
			Model:       req.ModelID,
			APIKey:      channel.APIKey,
			Temperature: ptrFloat32(req.Temperature),
		}
		applyOpenAITokenLimit(req, cfg)
		if channel.BaseURL != "" {
			cfg.BaseURL = channel.BaseURL
		}
		applyOpenAICompatibleThinking(runtimeReqForAdapter(req, "openai"), cfg)
		cm, err := openai.NewChatModel(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to create channel %q chat model: %w", channel.Key, err)
		}
		return a.wrapUsageModel(cm, req), nil

	case service.AdapterAnthropic:
		maxOutput := req.ModelMaxOutput
		if !req.RuntimeResolved {
			maxOutput = modelbank.GetOrDefault(req.ModelID, req.Provider).Capabilities.MaxOutput
		}
		cfg := &claude.Config{
			APIKey:    channel.APIKey,
			Model:     req.ModelID,
			MaxTokens: resolveClaudeMaxTokens(req.MaxTokens, maxOutput),
		}
		if channel.BaseURL != "" {
			baseURL := channel.BaseURL
			cfg.BaseURL = &baseURL
		}
		applyClaudeThinking(runtimeReqForAdapter(req, "anthropic"), cfg)
		cm, err := claude.NewChatModel(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to create channel %q chat model: %w", channel.Key, err)
		}
		return a.wrapUsageModel(cm, req), nil

	case service.AdapterGoogle:
		clientCfg := &genai.ClientConfig{APIKey: channel.APIKey}
		if channel.BaseURL != "" {
			clientCfg.HTTPOptions = genai.HTTPOptions{BaseURL: channel.BaseURL}
		}
		client, err := genai.NewClient(ctx, clientCfg)
		if err != nil {
			return nil, fmt.Errorf("failed to create genai client: %w", err)
		}
		cfg := &gemini.Config{
			Client:      client,
			Model:       req.ModelID,
			MaxTokens:   ptrIntPositive(req.MaxTokens),
			Temperature: ptrFloat32(req.Temperature),
		}
		// params 型原生搜索：按统一决策挂载 google_search（grounding）。
		if searchDecision.UseModelNativeSearch && searchDecision.SearchImpl == modelbank.SearchImplParams {
			cfg.EnableGoogleSearch = &genai.GoogleSearch{}
		}
		applyGeminiThinking(runtimeReqForAdapter(req, "google"), cfg)
		cm, err := gemini.NewChatModel(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to create channel %q chat model: %w", channel.Key, err)
		}
		return a.wrapUsageModel(cm, req), nil

	default:
		return nil, fmt.Errorf("unsupported adapter %q for channel %q", channel.Adapter, channel.Key)
	}
}

func applyOpenAITokenLimit(req *ChatRequest, cfg *openai.ChatModelConfig) {
	if req == nil || cfg == nil {
		return
	}
	limit := ptrIntPositive(req.MaxTokens)
	switch modelbank.ResolveThinkingFormat(req.Provider, req.ModelID, req.ThinkingFormat, req.Reasoning) {
	case modelbank.ThinkingFormatOpenAIReasoningEffort, modelbank.ThinkingFormatOpenAIGPT56:
		cfg.MaxCompletionTokens = limit
		return
	}
	cfg.MaxTokens = limit
}

func resolveClaudeMaxTokens(requested, modelMaxOutput int) int {
	if requested > 0 {
		return requested
	}
	const fallback = 8192
	if modelMaxOutput > 0 && modelMaxOutput < fallback {
		return modelMaxOutput
	}
	return fallback
}

func runtimeReqForAdapter(req *ChatRequest, protocolProvider string) *ChatRequest {
	if req == nil {
		return nil
	}
	clone := *req
	clone.Provider = protocolProvider
	return &clone
}

func (a *EinoAgent) wrapUsageModel(cm einoModel.ToolCallingChatModel, req *ChatRequest) einoModel.ToolCallingChatModel {
	cm = modelstream.ObserveChatModel(cm)
	if a != nil && req != nil && !req.SkipUsage && a.usageService != nil {
		cm = modelusage.WrapChatModel(cm, a.usageService, modelusage.Meta{
			UserID:    req.UserID,
			SessionID: req.SessionID,
			Provider:  req.Provider,
			ModelID:   req.ModelID,
		})
	}
	return cm
}

func (a *EinoAgent) buildUtilityModelWithInfo(ctx context.Context, modelID string) (einoModel.ToolCallingChatModel, *modelbank.ModelInfo, error) {
	info, err := a.resolveUtilityModelInfo(modelID)
	if err != nil {
		return nil, nil, err
	}
	cm, err := a.buildChatModel(ctx, &ChatRequest{
		ModelID:  info.ID,
		Provider: info.Provider,
	}, modelbank.SearchDecision{})
	if err != nil {
		return nil, info, err
	}
	return cm, info, nil
}

func (a *EinoAgent) resolveUtilityModelInfo(preferredModelID string) (*modelbank.ModelInfo, error) {
	channelAvailability := make(map[string]bool)
	isChannelAvailable := func(channelKey string) bool {
		channelKey = strings.TrimSpace(channelKey)
		if channelKey == "" {
			return false
		}
		available, ok := channelAvailability[channelKey]
		if ok {
			return available
		}
		available = a.utilityChannelAvailable(channelKey)
		channelAvailability[channelKey] = available
		return available
	}

	preferredModelID = strings.TrimSpace(preferredModelID)
	if preferredModelID != "" {
		if info := modelbank.Get(preferredModelID); info != nil && info.Enabled && isChannelAvailable(info.Provider) {
			return info, nil
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
		if info != nil && info.Enabled && isChannelAvailable(info.Provider) {
			return info, nil
		}
	}
	if preferredModelID == "" {
		return nil, fmt.Errorf("no enabled utility model has an available channel")
	}
	return nil, fmt.Errorf("utility model %q is unavailable and no fallback model has an available channel", preferredModelID)
}

func (a *EinoAgent) utilityChannelAvailable(channelKey string) bool {
	if a == nil || a.channelService == nil || strings.TrimSpace(channelKey) == "" {
		return false
	}
	_, err := a.channelService.ResolveAIChannel(channelKey)
	return err == nil
}

// buildExtractSummarizer 按配置构造网页提炼器：
// 读 extract_summary_enabled（默认 true）/ extract_summary_model（默认 claude-haiku-4-5）。
// 关闭或小模型构造失败时返回 (nil, false)，工具据此降级到截断，不阻断主流程。
func (a *EinoAgent) buildExtractSummarizer(ctx context.Context) (tool.Summarizer, bool) {
	enabled := true
	modelID := "claude-haiku-4-5"
	if a.configRepo != nil {
		enabled = a.configRepo.GetBool("extract_summary_enabled", true)
		modelID = a.configRepo.GetString("extract_summary_model", modelID)
	}
	if !enabled {
		return nil, false
	}
	cm, info, err := a.buildUtilityModelWithInfo(ctx, modelID)
	if err != nil {
		log.Printf("[web_extract] 提炼小模型构造失败，降级到截断: model=%s err=%v", modelID, err)
		return nil, false
	}
	return &extractSummarizer{
		chatModel: cm, taskRuns: a.taskRunRepo, provider: info.Provider, modelID: info.ID,
		runtimeVersion: a.extractSummarizerRuntimeVersion(modelID, info),
	}, true
}

func (a *EinoAgent) extractSummarizerRuntimeVersion(configuredModelID string, info *modelbank.ModelInfo) string {
	parts := []string{strings.TrimSpace(configuredModelID)}
	if info != nil {
		parts = append(parts, info.Provider, info.ID)
		if a != nil && a.channelService != nil {
			if channel, err := a.channelService.ResolveAIChannel(info.Provider); err == nil && channel != nil {
				parts = append(parts, channel.UpdatedAt.UTC().Format(time.RFC3339Nano))
			}
		}
	}
	if a != nil && a.configRepo != nil {
		for _, key := range []string{"extract_summary_enabled", "extract_summary_model"} {
			item, err := a.configRepo.Get(key)
			if err != nil || item == nil {
				parts = append(parts, key, "unavailable")
				continue
			}
			parts = append(parts, key, item.UpdatedAt.UTC().Format(time.RFC3339Nano))
		}
	}
	return checksumValue("extract-refinement-runtime", parts)
}

func ptrFloat32(f *float64) *float32 {
	if f == nil {
		return nil
	}
	v := float32(*f)
	return &v
}

// ptrIntPositive 把 max_tokens 转为 *int；<=0 视为"未设置"返回 nil，交给模型默认。
func ptrIntPositive(n int) *int {
	if n <= 0 {
		return nil
	}
	return &n
}
