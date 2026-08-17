package agent

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino-ext/components/model/claude"
	"github.com/cloudwego/eino-ext/components/model/gemini"
	"github.com/cloudwego/eino-ext/components/model/openai"
	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/modelstream"
	"github.com/huoguojun123/EffChat/internal/openairesponses"
	"github.com/huoguojun123/EffChat/internal/providerhttp"
	"github.com/huoguojun123/EffChat/internal/service"
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
		channel, err = a.channelService.ResolveAIChannelContext(ctx, req.Provider)
		if err != nil {
			return nil, err
		}
	}
	temperature, err := resolveRequestTemperature(req)
	if err != nil {
		return nil, fmt.Errorf("invalid model temperature profile: %w", err)
	}

	switch channel.Adapter {
	case service.AdapterOpenAICompatible:
		openAIProfile := model.CloneOpenAIRequestProfile(req.OpenAIRequestProfile)
		if err := model.ValidateOpenAIRequestProfile(openAIProfile); err != nil {
			return nil, fmt.Errorf("invalid OpenAI-compatible request profile: %w", err)
		}
		if modelbank.ResolveThinkingFormat(req.Provider, req.ModelID, req.ThinkingFormat, req.Reasoning) == modelbank.ThinkingFormatXAIGrok {
			// xAI rejects both penalty fields on reasoning models. Preserve the
			// administrator's stored profile, but omit incompatible wire fields for
			// this request instead of turning a reusable gateway profile into a 400.
			openAIProfile.PresencePenalty = nil
			openAIProfile.FrequencyPenalty = nil
		}
		cfg := &openai.ChatModelConfig{
			Model:            req.ModelID,
			APIKey:           channel.APIKey,
			Temperature:      ptrFloat32(temperature),
			TopP:             ptrFloat32(openAIProfile.TopP),
			PresencePenalty:  ptrFloat32(openAIProfile.PresencePenalty),
			FrequencyPenalty: ptrFloat32(openAIProfile.FrequencyPenalty),
		}
		// Eino exposes top_p and both penalties as typed config fields, but its
		// current component config does not expose n and the OpenAI SDK omits
		// numeric zero penalties after dereferencing them. Bridge only those
		// already-validated typed values through ExtraFields at this adapter
		// boundary; administrators still cannot provide arbitrary JSON.
		if openAIProfile.N != nil {
			setOpenAIExtraField(cfg, "n", *openAIProfile.N)
		}
		if openAIProfile.PresencePenalty != nil && *openAIProfile.PresencePenalty == 0 {
			setOpenAIExtraField(cfg, "presence_penalty", 0)
		}
		if openAIProfile.FrequencyPenalty != nil && *openAIProfile.FrequencyPenalty == 0 {
			setOpenAIExtraField(cfg, "frequency_penalty", 0)
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

	case service.AdapterOpenAIResponses:
		cfg := &openairesponses.Config{
			Model:       req.ModelID,
			APIKey:      channel.APIKey,
			MaxTokens:   ptrIntPositive(req.MaxTokens),
			Temperature: ptrFloat32(temperature),
			Reasoning:   openAIResponsesReasoning(runtimeReqForAdapter(req, "openai")),
		}
		if channel.BaseURL != "" {
			cfg.BaseURL = channel.BaseURL
		}
		cm, err := openairesponses.NewChatModel(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to create channel %q responses model: %w", channel.Key, err)
		}
		return a.wrapUsageModel(cm, req), nil

	case service.AdapterAnthropic:
		maxOutput := req.ModelMaxOutput
		if !req.RuntimeResolved {
			maxOutput = modelbank.GetOrDefault(req.ModelID, req.Provider).Capabilities.MaxOutput
		}
		cfg := &claude.Config{
			APIKey:     channel.APIKey,
			Model:      req.ModelID,
			MaxTokens:  resolveClaudeMaxTokens(req.MaxTokens, maxOutput),
			HTTPClient: providerhttp.NewAnthropicSingleAttemptClient(nil),
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
			Client:    client,
			Model:     req.ModelID,
			MaxTokens: ptrIntPositive(req.MaxTokens),
		}
		if !modelbank.GeminiOmitsSamplingParameters(req.ModelID) {
			cfg.Temperature = ptrFloat32(temperature)
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

// buildResponsesAgenticModel constructs the native typed model used only by
// the main Responses conversation. Utility calls can keep the classic adapter
// because they do not run local Tools; the user-facing Agent path must not
// convert function calls through ToolCallingChatModel before Eino sees them.
func (a *EinoAgent) buildResponsesAgenticModel(ctx context.Context, req *ChatRequest) (einoModel.AgenticModel, error) {
	if a == nil || a.channelService == nil {
		return nil, fmt.Errorf("model channel configuration is not available")
	}
	channel := req.RuntimeChannel
	if channel == nil {
		var err error
		channel, err = a.channelService.ResolveAIChannelContext(ctx, req.Provider)
		if err != nil {
			return nil, err
		}
	}
	if channel.Adapter != service.AdapterOpenAIResponses {
		return nil, fmt.Errorf("channel %q is not an OpenAI Responses channel", channel.Key)
	}
	temperature, err := resolveRequestTemperature(req)
	if err != nil {
		return nil, fmt.Errorf("invalid model temperature profile: %w", err)
	}
	cfg := &openairesponses.Config{
		Model:       req.ModelID,
		APIKey:      channel.APIKey,
		MaxTokens:   ptrIntPositive(req.MaxTokens),
		Temperature: ptrFloat32(temperature),
		Reasoning:   openAIResponsesReasoning(runtimeReqForAdapter(req, "openai")),
	}
	if channel.BaseURL != "" {
		cfg.BaseURL = channel.BaseURL
	}
	am, err := openairesponses.NewAgenticModel(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create channel %q responses model: %w", channel.Key, err)
	}
	return a.wrapUsageAgenticModel(am, req), nil
}

func (a *EinoAgent) usesResponsesAdapter(ctx context.Context, req *ChatRequest) (bool, error) {
	if req == nil {
		return false, fmt.Errorf("chat request is required")
	}
	channel := req.RuntimeChannel
	if channel == nil {
		if a == nil || a.channelService == nil {
			return false, fmt.Errorf("model channel configuration is not available")
		}
		var err error
		channel, err = a.channelService.ResolveAIChannelContext(ctx, req.Provider)
		if err != nil {
			return false, err
		}
	}
	return channel.Adapter == service.AdapterOpenAIResponses, nil
}

func resolveRequestTemperature(req *ChatRequest) (*float64, error) {
	if req == nil {
		return nil, nil
	}
	policy := req.TemperaturePolicy
	fixed := req.TemperatureValue
	if policy == "" {
		if info := modelbank.Get(req.ModelID); info != nil {
			policy = info.TemperaturePolicy
			fixed = info.TemperatureValue
		}
	}
	return model.ResolveTemperatureForRequest(policy, fixed, req.Temperature)
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

// taskModelRequest keeps provider, channel, runtime, and thinking ownership
// from the active chat while giving a background task its own output-cost
// boundary. The clone prevents title/compaction/memory/probe preparation from
// mutating the request later used by the main conversation.
func taskModelRequest(req *ChatRequest, maxTokens int) *ChatRequest {
	if req == nil {
		req = &ChatRequest{}
	}
	clone := *req
	if clone.ModelMaxOutput > 0 && (maxTokens <= 0 || clone.ModelMaxOutput < maxTokens) {
		maxTokens = clone.ModelMaxOutput
	}
	clone.MaxTokens = maxTokens
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

func (a *EinoAgent) wrapUsageAgenticModel(am einoModel.AgenticModel, req *ChatRequest) einoModel.AgenticModel {
	am = modelstream.ObserveAgenticModel(am)
	if a != nil && req != nil && !req.SkipUsage && a.usageService != nil {
		am = modelusage.WrapAgenticModel(am, a.usageService, modelusage.Meta{
			UserID: req.UserID, SessionID: req.SessionID, Provider: req.Provider, ModelID: req.ModelID,
		})
	}
	return am
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
