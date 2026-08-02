package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/service"
)

type modelListConfig struct {
	channelKey string
	adapter    string
	baseURL    string
	apiKey     string
}

func channelModelListConfig(channelService *service.ChannelService, requestedChannel string) (modelListConfig, bool, error) {
	if channelService == nil {
		return modelListConfig{}, false, nil
	}
	if requestedChannel != "" {
		channel, err := channelService.ResolveAIChannel(requestedChannel)
		if err != nil {
			return modelListConfig{}, false, err
		}
		cfg, err := modelListConfigForChannel(channel)
		return cfg, err == nil, err
	}

	channels, err := channelService.ListAIChannels(false)
	if err != nil {
		return modelListConfig{}, false, err
	}
	for _, channel := range channels {
		if channel != nil && channel.Key == "openai" {
			if cfg, err := modelListConfigForChannel(channel); err == nil {
				return cfg, true, nil
			}
		}
	}
	for _, channel := range channels {
		if cfg, err := modelListConfigForChannel(channel); err == nil {
			return cfg, true, nil
		}
	}
	return modelListConfig{}, false, nil
}

func modelListConfigForChannel(channel *model.AIChannel) (modelListConfig, error) {
	if channel == nil {
		return modelListConfig{}, service.ErrChannelNotFound
	}
	if strings.TrimSpace(channel.APIKey) == "" {
		return modelListConfig{}, service.ErrChannelUnavailable
	}
	if !supportedModelListAdapter(channel.Adapter) {
		return modelListConfig{}, service.ErrChannelUnavailable
	}
	baseURL := strings.TrimSpace(channel.BaseURL)
	if baseURL == "" {
		baseURL = defaultModelListBaseURL(channel.Adapter)
	}
	return modelListConfig{
		channelKey: channel.Key,
		adapter:    channel.Adapter,
		baseURL:    baseURL,
		apiKey:     channel.APIKey,
	}, nil
}

func supportedModelListAdapter(adapter string) bool {
	switch adapter {
	case service.AdapterOpenAICompatible, service.AdapterOpenAIResponses, service.AdapterAnthropic, service.AdapterGoogle:
		return true
	default:
		return false
	}
}

func defaultModelListBaseURL(adapter string) string {
	switch adapter {
	case service.AdapterAnthropic:
		return "https://api.anthropic.com"
	case service.AdapterGoogle:
		return "https://generativelanguage.googleapis.com"
	default:
		return ""
	}
}

func enabledModelProviders(channelService *service.ChannelService) map[string]bool {
	if channelService == nil {
		return nil
	}
	channels, err := channelService.ListAIChannels(false)
	if err != nil {
		return nil
	}
	enabled := make(map[string]bool)
	for _, channel := range channels {
		if channel == nil || strings.TrimSpace(channel.APIKey) == "" {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(channel.Key))
		if key != "" {
			enabled[key] = true
		}
	}
	return enabled
}

type upstreamModelMeta struct {
	ID                     string
	Name                   string
	OwnedBy                string
	ContextWindow          int
	MaxOutput              int
	Vision                 bool
	ToolUse                bool
	Reasoning              bool
	SearchImpl             string
	SupportedEndpointTypes []string
	MatchFields            []string
}

func fetchChannelModels(ctx context.Context, cfg modelListConfig) ([]upstreamModelMeta, error) {
	switch cfg.adapter {
	case service.AdapterOpenAICompatible, service.AdapterOpenAIResponses:
		return fetchGatewayModels(ctx, cfg.baseURL, cfg.apiKey)
	case service.AdapterAnthropic:
		return fetchAnthropicModels(ctx, cfg.baseURL, cfg.apiKey)
	case service.AdapterGoogle:
		return fetchGoogleModels(ctx, cfg.baseURL, cfg.apiKey)
	default:
		return nil, fmt.Errorf("unsupported adapter %q", cfg.adapter)
	}
}

func fetchGatewayModels(ctx context.Context, baseURL, apiKey string) ([]upstreamModelMeta, error) {
	endpoint := modelListEndpoint(baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Accept", "application/json")

	body, err := doModelListRequest(req)
	if err != nil {
		return nil, err
	}

	var raw interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		if looksLikeHTML(body) {
			return nil, fmt.Errorf("model list endpoint returned HTML instead of JSON; check the channel Base URL and use the API root, for example https://api.openai.com/v1")
		}
		return nil, fmt.Errorf("failed to decode model list: %w", err)
	}

	models := extractGatewayModels(raw)
	if len(models) == 0 {
		return nil, fmt.Errorf("model list is empty")
	}
	sort.Slice(models, func(i, j int) bool {
		return models[i].ID < models[j].ID
	})
	return models, nil
}

func fetchAnthropicModels(ctx context.Context, baseURL, apiKey string) ([]upstreamModelMeta, error) {
	endpoint := anthropicModelListEndpoint(baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Accept", "application/json")

	body, err := doModelListRequest(req)
	if err != nil {
		return nil, err
	}
	var raw interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		if looksLikeHTML(body) {
			return nil, fmt.Errorf("anthropic model list endpoint returned HTML instead of JSON; check the channel Base URL and use the API root, for example https://api.anthropic.com")
		}
		return nil, fmt.Errorf("failed to decode Anthropic model list: %w", err)
	}
	models := extractGatewayModels(raw)
	if len(models) == 0 {
		return nil, fmt.Errorf("anthropic model list is empty")
	}
	for i := range models {
		models[i].OwnedBy = "anthropic"
		models[i].ToolUse = true
		models[i].Vision = true
		models[i].Reasoning = true
		models[i].SearchImpl = string(modelbank.SearchImplTool)
	}
	sort.Slice(models, func(i, j int) bool {
		return models[i].ID < models[j].ID
	})
	return models, nil
}

func fetchGoogleModels(ctx context.Context, baseURL, apiKey string) ([]upstreamModelMeta, error) {
	endpoint := googleModelListEndpoint(baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-goog-api-key", apiKey)
	req.Header.Set("Accept", "application/json")

	body, err := doModelListRequest(req)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Models []struct {
			Name              string   `json:"name"`
			DisplayName       string   `json:"displayName"`
			InputTokenLimit   int      `json:"inputTokenLimit"`
			OutputTokenLimit  int      `json:"outputTokenLimit"`
			SupportedActions  []string `json:"supportedActions"`
			Description       string   `json:"description"`
			Version           string   `json:"version"`
			Thinking          bool     `json:"thinking"`
			MaxTemperature    float64  `json:"maxTemperature"`
			DefaultCheckpoint string   `json:"defaultCheckpointId"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		if looksLikeHTML(body) {
			return nil, fmt.Errorf("google model list endpoint returned HTML instead of JSON; check the channel Base URL and use the API root, for example https://generativelanguage.googleapis.com")
		}
		return nil, fmt.Errorf("failed to decode Google model list: %w", err)
	}
	models := make([]upstreamModelMeta, 0, len(payload.Models))
	for _, item := range payload.Models {
		id := strings.TrimSpace(item.Name)
		id = strings.TrimPrefix(id, "models/")
		if id == "" {
			continue
		}
		meta := upstreamModelMeta{
			ID:                     id,
			Name:                   item.DisplayName,
			OwnedBy:                "google",
			ContextWindow:          item.InputTokenLimit,
			MaxOutput:              item.OutputTokenLimit,
			ToolUse:                containsAny(item.SupportedActions, "generateContent"),
			Reasoning:              item.Thinking || strings.Contains(strings.ToLower(item.Description), "thinking") || strings.Contains(strings.ToLower(id), "gemini"),
			SearchImpl:             string(modelbank.SearchImplParams),
			SupportedEndpointTypes: item.SupportedActions,
			MatchFields:            compactStringFields([]string{item.Name, item.DisplayName, item.Version, item.DefaultCheckpoint}),
		}
		models = append(models, meta)
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("google model list is empty")
	}
	sort.Slice(models, func(i, j int) bool {
		return models[i].ID < models[j].ID
	})
	return models, nil
}

func doModelListRequest(req *http.Request) ([]byte, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch model list: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("failed to read model list response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("failed to fetch model list: status %d: %s", resp.StatusCode, responseExcerpt(body))
	}
	return body, nil
}

func modelListEndpoint(baseURL string) string {
	base := strings.TrimRight(service.NormalizeOpenAICompatibleBaseURL(service.AdapterOpenAICompatible, baseURL), "/")
	if strings.HasSuffix(base, "/v1") {
		return base + "/models"
	}
	return base + "/v1/models"
}

func anthropicModelListEndpoint(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	base = strings.TrimSuffix(base, "/v1/models")
	base = strings.TrimSuffix(base, "/models")
	base = strings.TrimSuffix(base, "/v1")
	if base == "" {
		base = defaultModelListBaseURL(service.AdapterAnthropic)
	}
	return strings.TrimRight(base, "/") + "/v1/models"
}

func googleModelListEndpoint(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	base = strings.TrimSuffix(base, "/v1beta/models")
	base = strings.TrimSuffix(base, "/v1/models")
	base = strings.TrimSuffix(base, "/models")
	base = strings.TrimSuffix(base, "/v1beta")
	base = strings.TrimSuffix(base, "/v1")
	if base == "" {
		base = defaultModelListBaseURL(service.AdapterGoogle)
	}
	return strings.TrimRight(base, "/") + "/v1beta/models"
}

func looksLikeHTML(body []byte) bool {
	trimmed := strings.TrimSpace(string(body))
	return strings.HasPrefix(strings.ToLower(trimmed), "<!doctype html") || strings.HasPrefix(strings.ToLower(trimmed), "<html") || strings.HasPrefix(trimmed, "<")
}

func responseExcerpt(body []byte) string {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return "empty response"
	}
	const max = 240
	if len(text) > max {
		return text[:max] + "..."
	}
	return text
}

func extractGatewayModels(raw interface{}) []upstreamModelMeta {
	seen := map[string]bool{}
	models := make([]upstreamModelMeta, 0)
	var walk func(interface{})
	walk = func(value interface{}) {
		switch typed := value.(type) {
		case []interface{}:
			for _, item := range typed {
				walk(item)
			}
		case map[string]interface{}:
			if id, ok := typed["id"].(string); ok && id != "" && !seen[id] {
				seen[id] = true
				models = append(models, parseUpstreamModelMap(id, typed))
			}
			if data, ok := typed["data"]; ok {
				walk(data)
			}
			if nested, ok := typed["models"]; ok {
				walk(nested)
			}
		case string:
			if typed != "" && !seen[typed] {
				seen[typed] = true
				models = append(models, upstreamModelMeta{ID: typed})
			}
		}
	}
	walk(raw)
	return models
}

func parseUpstreamModelMap(id string, payload map[string]interface{}) upstreamModelMeta {
	architecture := nestedMapField(payload, "architecture")
	topProvider := nestedMapField(payload, "top_provider", "topProvider")
	metadata := nestedMapField(payload, "metadata")

	meta := upstreamModelMeta{
		ID:         id,
		Name:       stringField(payload, "name", "display_name", "displayName"),
		OwnedBy:    stringField(payload, "owned_by", "provider", "vendor", "organization"),
		SearchImpl: stringField(payload, "search_impl", "search_mode"),
	}
	meta.MatchFields = collectModelIdentityFields(payload, architecture, topProvider, metadata)

	meta.ContextWindow = intField(payload,
		"context_window",
		"context_length",
		"max_context_tokens",
		"input_token_limit",
		"inputTokenLimit",
	)
	if meta.ContextWindow == 0 && topProvider != nil {
		meta.ContextWindow = intField(topProvider,
			"context_window",
			"context_length",
			"contextLength",
			"max_context_tokens",
		)
	}
	meta.MaxOutput = intField(payload,
		"max_output",
		"max_output_tokens",
		"output_token_limit",
		"outputTokenLimit",
		"max_completion_tokens",
	)
	if meta.MaxOutput == 0 && topProvider != nil {
		meta.MaxOutput = intField(topProvider,
			"max_output",
			"max_output_tokens",
			"output_token_limit",
			"outputTokenLimit",
			"max_completion_tokens",
		)
	}

	inputModalities := stringSliceField(payload, "input_modalities", "inputModalities", "modalities")
	if architecture != nil {
		inputModalities = append(inputModalities, stringSliceField(architecture, "input_modalities", "inputModalities", "modalities")...)
		if modality := strings.ToLower(stringField(architecture, "modality")); modality != "" {
			inputModalities = append(inputModalities, modality)
		}
	}
	supportedParams := stringSliceField(payload, "supported_parameters", "supportedParameters", "capabilities")
	endpoints := stringSliceField(payload, "supported_endpoint_types", "supportedEndpointTypes")
	if metadata != nil {
		supportedParams = append(supportedParams, stringSliceField(metadata, "supported_parameters", "supportedParameters", "capabilities")...)
	}

	meta.SupportedEndpointTypes = endpoints
	meta.Vision = containsAny(inputModalities, "image", "vision", "multimodal") || boolField(payload, "vision")
	meta.ToolUse = containsAny(supportedParams, "tools", "tool_calls", "function_calling", "function-calling") ||
		containsAny(endpoints, "responses", "chat/completions", "assistants") ||
		boolField(payload, "tool_use", "function_calling")
	meta.Reasoning = boolField(payload, "reasoning", "thinking") ||
		containsAny(supportedParams, "reasoning", "thinking", "reasoning_effort", "include_reasoning")

	if meta.ContextWindow == 0 && metadata != nil {
		meta.ContextWindow = intField(metadata,
			"context_window",
			"context_length",
			"max_context_tokens",
			"input_token_limit",
			"inputTokenLimit",
		)
	}
	if meta.MaxOutput == 0 && metadata != nil {
		meta.MaxOutput = intField(metadata,
			"max_output",
			"max_output_tokens",
			"output_token_limit",
			"outputTokenLimit",
		)
	}
	if !meta.Vision && metadata != nil {
		meta.Vision = boolField(metadata, "vision") || containsAny(stringSliceField(metadata, "input_modalities", "modalities"), "image", "vision", "multimodal")
	}
	if !meta.ToolUse && metadata != nil {
		meta.ToolUse = boolField(metadata, "tool_use", "function_calling") || containsAny(stringSliceField(metadata, "supported_parameters", "capabilities"), "tools", "tool_calls", "function_calling")
	}
	if !meta.Reasoning && metadata != nil {
		meta.Reasoning = boolField(metadata, "reasoning", "thinking") || containsAny(stringSliceField(metadata, "supported_parameters", "capabilities"), "reasoning", "thinking", "reasoning_effort", "include_reasoning")
	}

	return meta
}

func inferModel(meta upstreamModelMeta, index int) *model.Model {
	return inferModelForProvider(meta, index, "", nil)
}

func inferModelForChannel(meta upstreamModelMeta, index int, cfg modelListConfig, enabledProviders map[string]bool) *model.Model {
	return modelbank.ApplyThinkingRuntimeMetadataWithAdapter(
		inferModelForProvider(meta, index, cfg.channelKey, enabledProviders),
		cfg.adapter,
	)
}

func inferModelForProvider(meta upstreamModelMeta, index int, fallbackProvider string, enabledProviders map[string]bool) *model.Model {
	provider := inferProviderForMeta(meta, fallbackProvider, enabledProviders)
	displaySource := meta.ID
	if meta.Name != "" {
		displaySource = meta.Name
	}
	m := &model.Model{
		ID:             meta.ID,
		DisplayName:    inferDisplayName(displaySource),
		Provider:       provider,
		Enabled:        false,
		SortOrder:      1000 + index,
		ContextWindow:  meta.ContextWindow,
		MaxOutput:      meta.MaxOutput,
		Vision:         meta.Vision,
		ToolUse:        meta.ToolUse,
		Reasoning:      meta.Reasoning,
		ThinkingFormat: modelbank.NormalizeThinkingFormat(""),
		SearchImpl:     meta.SearchImpl,
	}

	switch provider {
	case "anthropic":
		if m.SearchImpl == "" {
			m.SearchImpl = "tool"
		}
		applyIfZero(&m.ContextWindow, 1000000)
		applyIfZero(&m.MaxOutput, 64000)
		applyIfFalse(&m.Vision, true)
		applyIfFalse(&m.ToolUse, true)
		applyIfFalse(&m.Reasoning, true)
	case "google":
		if m.SearchImpl == "" {
			m.SearchImpl = "params"
		}
		applyIfZero(&m.ContextWindow, 1048576)
		applyIfZero(&m.MaxOutput, 65536)
		applyIfFalse(&m.Vision, true)
		applyIfFalse(&m.ToolUse, true)
		applyIfFalse(&m.Reasoning, true)
	case "deepseek":
		applyIfZero(&m.ContextWindow, 1000000)
		applyIfZero(&m.MaxOutput, 384000)
		applyIfFalse(&m.ToolUse, true)
		if !m.Reasoning {
			m.Reasoning = strings.Contains(strings.ToLower(meta.ID), "reasoner") || strings.Contains(strings.ToLower(meta.ID), "r1") || strings.Contains(strings.ToLower(meta.ID), "v4")
		}
	case "perplexity":
		if m.SearchImpl == "" {
			m.SearchImpl = "internal"
		}
		if strings.Contains(strings.ToLower(meta.ID), "reasoning") || strings.Contains(strings.ToLower(meta.ID), "research") {
			m.Reasoning = true
		}
	default:
		applyIfFalse(&m.Vision, true)
		applyIfFalse(&m.ToolUse, true)
		if !m.Reasoning {
			lower := strings.ToLower(meta.ID)
			m.Reasoning = strings.HasPrefix(lower, "o3") || strings.HasPrefix(lower, "o4") || strings.HasPrefix(lower, "gpt-5")
		}
	}
	if !m.Reasoning && modelbank.IsKnownThinkingModel(provider, meta.ID, meta.Name) {
		m.Reasoning = true
	}
	return modelbank.ApplyThinkingRuntimeMetadata(m)
}

func matchExistingGatewayModel(meta upstreamModelMeta, existingModels []*model.Model, fallbackProvider string, enabledProviders map[string]bool) *model.Model {
	if len(existingModels) == 0 {
		return nil
	}
	targetProvider := inferProviderForMeta(meta, fallbackProvider, enabledProviders)
	for _, m := range existingModels {
		if m != nil && m.ID == meta.ID && modelProviderMatches(m.Provider, targetProvider) {
			return m
		}
	}

	upstreamFields := gatewayModelMatchFields(meta)
	for _, m := range existingModels {
		if m == nil || !modelProviderMatches(m.Provider, targetProvider) {
			continue
		}
		if modelIdentityFieldsMatch(upstreamFields, []string{m.ID, m.DisplayName}) {
			return m
		}
	}
	return nil
}

func modelProviderMatches(existingProvider, targetProvider string) bool {
	existing := strings.ToLower(strings.TrimSpace(existingProvider))
	target := strings.ToLower(strings.TrimSpace(targetProvider))
	return target == "" || existing == target
}

func gatewayModelMatchFields(meta upstreamModelMeta) []string {
	fields := []string{meta.ID, meta.Name}
	fields = append(fields, meta.MatchFields...)
	return compactStringFields(fields)
}

func modelIdentityFieldsMatch(left, right []string) bool {
	for _, a := range left {
		na := normalizeModelIdentity(a)
		if na == "" {
			continue
		}
		for _, b := range right {
			nb := normalizeModelIdentity(b)
			if nb == "" {
				continue
			}
			if na == nb {
				return true
			}
			if min(len(na), len(nb)) >= 5 && (strings.Contains(na, nb) || strings.Contains(nb, na)) {
				return true
			}
		}
	}
	return false
}

func inferProviderForMeta(meta upstreamModelMeta, fallbackProvider string, enabledProviders map[string]bool) string {
	fields := []string{meta.ID, meta.Name, meta.OwnedBy}
	fields = append(fields, meta.MatchFields...)
	return inferProviderFromFieldsWithEnabled(fields, fallbackProvider, enabledProviders)
}

func inferProviderFromFields(fields []string, fallbackProvider string) string {
	return inferProviderFromFieldsWithEnabled(fields, fallbackProvider, nil)
}

func inferProviderFromFieldsWithEnabled(fields []string, fallbackProvider string, enabledProviders map[string]bool) string {
	// NewAPI / OpenAI 兼容网关经常把不同厂商模型混在一个 /models 响应里，
	// 并且模型 ID 未必是官方 ID。provider 归类因此不能只看当前点的是哪个渠道，
	// 而要扫描上游对象里的 id/name/provider/canonical_slug/alias 等身份字段。
	//
	// 但 openai 在本项目里不仅代表 OpenAI 官方，也代表最常见的 OpenAI-compatible
	// 自部署网关。管理员从 openai 渠道拉取时，所有上游模型都保留在 openai 渠道；
	// 如果管理员确实配置并使用 deepseek/qwen/google 等独立渠道，再按字段归类到
	// 对应渠道。这里的 provider 字段在当前版本仍兼容为 channel_key。
	fallback := strings.ToLower(strings.TrimSpace(fallbackProvider))
	if len(enabledProviders) > 0 && fallback == "openai" && providerEnabledForImport("openai", enabledProviders) {
		return "openai"
	}

	candidates := []struct {
		provider string
		needles  []string
	}{
		{provider: "anthropic", needles: []string{"claude", "anthropic"}},
		{provider: "google", needles: []string{"gemini", "google"}},
		{provider: "deepseek", needles: []string{"deepseek"}},
		{provider: "qwen", needles: []string{"qwen", "qwq", "tongyi"}},
		{provider: "zhipu", needles: []string{"glm", "zhipu", "bigmodel"}},
		{provider: "moonshot", needles: []string{"kimi", "moonshot"}},
		{provider: "volcengine", needles: []string{"doubao", "volcengine", "bytedance"}},
		{provider: "minimax", needles: []string{"minimax"}},
		{provider: "xai", needles: []string{"grok", "xai"}},
		{provider: "mistral", needles: []string{"mistral", "mixtral", "codestral"}},
		{provider: "perplexity", needles: []string{"sonar", "perplexity"}},
		{provider: "openai", needles: []string{"gpt", "openai"}},
	}
	for _, candidate := range candidates {
		if modelFieldContainsAny(fields, candidate.needles...) && providerEnabledForImport(candidate.provider, enabledProviders) {
			return candidate.provider
		}
	}

	if fallback != "" && providerEnabledForImport(fallback, enabledProviders) {
		return fallback
	}
	if provider := firstEnabledProvider(enabledProviders); provider != "" {
		return provider
	}
	return "openai"
}

func providerEnabledForImport(provider string, enabledProviders map[string]bool) bool {
	if len(enabledProviders) == 0 {
		return true
	}
	return enabledProviders[strings.ToLower(strings.TrimSpace(provider))]
}

func firstEnabledProvider(enabledProviders map[string]bool) string {
	if len(enabledProviders) == 0 {
		return ""
	}
	if enabledProviders["openai"] {
		return "openai"
	}
	providers := make([]string, 0, len(enabledProviders))
	for provider := range enabledProviders {
		providers = append(providers, provider)
	}
	sort.Strings(providers)
	return providers[0]
}

func modelFieldContainsAny(fields []string, needles ...string) bool {
	for _, field := range fields {
		normalized := normalizeModelIdentity(field)
		if normalized == "" {
			continue
		}
		for _, needle := range needles {
			if strings.Contains(normalized, normalizeModelIdentity(needle)) {
				return true
			}
		}
	}
	return false
}

func collectModelIdentityFields(payload map[string]interface{}, nestedMaps ...map[string]interface{}) []string {
	fields := identityFieldsFromMap(payload)
	for _, nested := range nestedMaps {
		fields = append(fields, identityFieldsFromMap(nested)...)
	}
	return compactStringFields(fields)
}

func identityFieldsFromMap(values map[string]interface{}) []string {
	if values == nil {
		return nil
	}
	// 上游网关返回的模型对象没有统一标准：有些把官方名放在 id，有些放在
	// canonical_slug / model_id / aliases 里。这里必须收集全部候选字段，而不是
	// 像普通取值一样只取第一个非空字段，否则“内部字段能对上”的模型会被误判为未导入。
	fields := stringFieldsFromKeys(values,
		"id", "name", "display_name", "displayName", "slug", "canonical_slug", "canonicalSlug",
		"model", "model_id", "modelId", "root", "parent", "family", "base_model", "baseModel",
	)
	fields = append(fields, stringSliceField(values, "aliases", "alias", "slugs", "models")...)
	return compactStringFields(fields)
}

func stringFieldsFromKeys(payload map[string]interface{}, keys ...string) []string {
	fields := make([]string, 0, len(keys))
	for _, key := range keys {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			fields = append(fields, value)
		}
	}
	return fields
}

func compactStringFields(fields []string) []string {
	seen := make(map[string]bool, len(fields))
	result := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" || seen[field] {
			continue
		}
		seen[field] = true
		result = append(result, field)
	}
	return result
}

func normalizeModelIdentity(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func inferDisplayName(id string) string {
	replacer := strings.NewReplacer("-", " ", "_", " ")
	words := strings.Fields(replacer.Replace(id))
	for i, word := range words {
		if len(word) == 0 {
			continue
		}
		if strings.EqualFold(word, "gpt") || strings.EqualFold(word, "api") {
			words[i] = strings.ToUpper(word)
			continue
		}
		words[i] = strings.ToUpper(word[:1]) + word[1:]
	}
	return strings.Join(words, " ")
}

func applyIfZero(target *int, fallback int) {
	if *target == 0 {
		*target = fallback
	}
}

func applyIfFalse(target *bool, fallback bool) {
	if !*target {
		*target = fallback
	}
}

func stringField(payload map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

func intField(payload map[string]interface{}, keys ...string) int {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case float64:
			return int(typed)
		case float32:
			return int(typed)
		case int:
			return typed
		case int64:
			return int(typed)
		case json.Number:
			if n, err := typed.Int64(); err == nil {
				return int(n)
			}
		case string:
			typed = strings.TrimSpace(typed)
			if typed == "" {
				continue
			}
			if n, err := strconv.Atoi(typed); err == nil {
				return n
			}
		}
	}
	return 0
}

func boolField(payload map[string]interface{}, keys ...string) bool {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case bool:
			return typed
		case string:
			typed = strings.TrimSpace(strings.ToLower(typed))
			if typed == "true" || typed == "1" || typed == "yes" || typed == "supported" {
				return true
			}
		}
	}
	return false
}

func stringSliceField(payload map[string]interface{}, keys ...string) []string {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case []interface{}:
			items := make([]string, 0, len(typed))
			for _, item := range typed {
				switch v := item.(type) {
				case string:
					if v != "" {
						items = append(items, strings.ToLower(v))
					}
				case map[string]interface{}:
					if name := stringField(v, "name", "id", "type"); name != "" {
						items = append(items, strings.ToLower(name))
					}
				}
			}
			if len(items) > 0 {
				return items
			}
		case []string:
			items := make([]string, 0, len(typed))
			for _, item := range typed {
				if item != "" {
					items = append(items, strings.ToLower(item))
				}
			}
			if len(items) > 0 {
				return items
			}
		case string:
			parts := strings.Split(typed, ",")
			items := make([]string, 0, len(parts))
			for _, part := range parts {
				part = strings.TrimSpace(strings.ToLower(part))
				if part != "" {
					items = append(items, part)
				}
			}
			if len(items) > 0 {
				return items
			}
		}
	}
	return nil
}

func nestedMapField(payload map[string]interface{}, keys ...string) map[string]interface{} {
	for _, key := range keys {
		if value, ok := payload[key].(map[string]interface{}); ok {
			return value
		}
	}
	return nil
}

func containsAny(items []string, targets ...string) bool {
	if len(items) == 0 {
		return false
	}
	for _, item := range items {
		for _, target := range targets {
			if item == target || strings.Contains(item, target) {
				return true
			}
		}
	}
	return false
}
