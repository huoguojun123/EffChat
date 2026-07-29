package modelbank

import (
	"sync"

	"github.com/huoguojun123/EffChat/internal/model"
)

// registry 内置模型能力表
// 数据来源参考 LobeHub model-bank，按 provider 维护
//
// SearchImpl 分类说明：
//   - internal: Perplexity sonar 系列，搜索对调用方透明
//   - params:   Gemini（grounding）、Qwen 等，传参数启用
//   - tool:     依赖模型 function calling 自主调用搜索
//   - "":       不支持原生搜索，由应用挂载 SearXNG 工具兜底
//
// builtinModels 返回内置默认模型表的一份新副本（每次调用重新构造，
// 便于 LoadModels 覆盖后仍可还原）。
func builtinModels() map[string]*ModelInfo {
	return map[string]*ModelInfo{
		// ============ OpenAI ============
		"gpt-5.6": {
			ID: "gpt-5.6", DisplayName: "GPT-5.6", Provider: "openai", Enabled: true,
			Capabilities: ModelCapabilities{
				Vision: true, ToolUse: true, Reasoning: true, SearchImpl: SearchImplNone,
				ContextWindow: 1050000, MaxOutput: 128000,
			},
		},
		"gpt-5.6-sol": {
			ID: "gpt-5.6-sol", DisplayName: "GPT-5.6 Sol", Provider: "openai", Enabled: true,
			Capabilities: ModelCapabilities{
				Vision: true, ToolUse: true, Reasoning: true, SearchImpl: SearchImplNone,
				ContextWindow: 1050000, MaxOutput: 128000,
			},
		},
		"gpt-5.6-terra": {
			ID: "gpt-5.6-terra", DisplayName: "GPT-5.6 Terra", Provider: "openai", Enabled: true,
			Capabilities: ModelCapabilities{
				Vision: true, ToolUse: true, Reasoning: true, SearchImpl: SearchImplNone,
				ContextWindow: 1050000, MaxOutput: 128000,
			},
		},
		"gpt-5.6-luna": {
			ID: "gpt-5.6-luna", DisplayName: "GPT-5.6 Luna", Provider: "openai", Enabled: true,
			Capabilities: ModelCapabilities{
				Vision: true, ToolUse: true, Reasoning: true, SearchImpl: SearchImplNone,
				ContextWindow: 1050000, MaxOutput: 128000,
			},
		},
		"gpt-5.5": {
			ID: "gpt-5.5", DisplayName: "GPT-5.5", Provider: "openai", Enabled: true,
			Capabilities: ModelCapabilities{
				Vision: true, ToolUse: true, Reasoning: true, SearchImpl: SearchImplNone,
			},
		},
		"gpt-5.4": {
			ID: "gpt-5.4", DisplayName: "GPT-5.4", Provider: "openai", Enabled: true,
			Capabilities: ModelCapabilities{
				Vision: true, ToolUse: true, Reasoning: true, SearchImpl: SearchImplNone,
			},
		},
		"gpt-5.4-mini": {
			ID: "gpt-5.4-mini", DisplayName: "GPT-5.4 mini", Provider: "openai", Enabled: true,
			Capabilities: ModelCapabilities{
				Vision: true, ToolUse: true, Reasoning: true, SearchImpl: SearchImplNone,
			},
		},
		"gpt-5.4-nano": {
			ID: "gpt-5.4-nano", DisplayName: "GPT-5.4 nano", Provider: "openai", Enabled: true,
			Capabilities: ModelCapabilities{
				Vision: true, ToolUse: true, Reasoning: true, SearchImpl: SearchImplNone,
			},
		},
		"gpt-4o": {
			ID: "gpt-4o", DisplayName: "GPT-4o", Provider: "openai", Enabled: true,
			Capabilities: ModelCapabilities{
				Vision: true, ToolUse: true, SearchImpl: SearchImplNone,
				ContextWindow: 128000, MaxOutput: 16384,
			},
		},
		"gpt-4o-mini": {
			ID: "gpt-4o-mini", DisplayName: "GPT-4o mini", Provider: "openai", Enabled: true,
			Capabilities: ModelCapabilities{
				Vision: true, ToolUse: true, SearchImpl: SearchImplNone,
				ContextWindow: 128000, MaxOutput: 16384,
			},
		},

		// ============ Anthropic ============
		"claude-fable-5": {
			ID: "claude-fable-5", DisplayName: "Claude Fable 5", Provider: "anthropic", Enabled: true,
			Capabilities: ModelCapabilities{
				Vision: true, ToolUse: true, Reasoning: true, SearchImpl: SearchImplTool,
				ContextWindow: 1000000, MaxOutput: 128000,
			},
		},
		"claude-opus-4-8": {
			ID: "claude-opus-4-8", DisplayName: "Claude Opus 4.8", Provider: "anthropic", Enabled: true,
			Capabilities: ModelCapabilities{
				Vision: true, ToolUse: true, Reasoning: true, SearchImpl: SearchImplTool,
				ContextWindow: 1000000, MaxOutput: 128000,
			},
		},
		"claude-sonnet-4-6": {
			ID: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6", Provider: "anthropic", Enabled: true,
			Capabilities: ModelCapabilities{
				Vision: true, ToolUse: true, Reasoning: true, SearchImpl: SearchImplTool,
				ContextWindow: 200000, MaxOutput: 64000,
			},
		},
		"claude-haiku-4-5": {
			ID: "claude-haiku-4-5", DisplayName: "Claude Haiku 4.5", Provider: "anthropic", Enabled: true,
			Capabilities: ModelCapabilities{
				Vision: true, ToolUse: true, Reasoning: true, SearchImpl: SearchImplTool,
				ContextWindow: 200000, MaxOutput: 64000,
			},
		},
		"claude-haiku-4-5-20251001": {
			ID: "claude-haiku-4-5-20251001", DisplayName: "Claude Haiku 4.5", Provider: "anthropic", Enabled: true,
			Capabilities: ModelCapabilities{
				Vision: true, ToolUse: true, Reasoning: true, SearchImpl: SearchImplTool,
				ContextWindow: 200000, MaxOutput: 64000,
			},
		},

		// ============ Google Gemini ============
		"gemini-3.5-flash": {
			ID: "gemini-3.5-flash", DisplayName: "Gemini 3.5 Flash", Provider: "google", Enabled: true,
			Capabilities: ModelCapabilities{
				Vision: true, ToolUse: true, Reasoning: true, SearchImpl: SearchImplParams,
				ContextWindow: 1048576, MaxOutput: 65536,
			},
		},
		"gemini-3.1-pro-preview": {
			ID: "gemini-3.1-pro-preview", DisplayName: "Gemini 3.1 Pro Preview", Provider: "google", Enabled: true,
			Capabilities: ModelCapabilities{
				Vision: true, ToolUse: true, Reasoning: true, SearchImpl: SearchImplParams,
				ContextWindow: 1048576, MaxOutput: 65536,
			},
		},
		"gemini-3-flash-preview": {
			ID: "gemini-3-flash-preview", DisplayName: "Gemini 3 Flash Preview", Provider: "google", Enabled: true,
			Capabilities: ModelCapabilities{
				Vision: true, ToolUse: true, Reasoning: true, SearchImpl: SearchImplParams,
				ContextWindow: 1048576, MaxOutput: 65536,
			},
		},
		"gemini-3.1-flash-lite": {
			ID: "gemini-3.1-flash-lite", DisplayName: "Gemini 3.1 Flash-Lite", Provider: "google", Enabled: true,
			Capabilities: ModelCapabilities{
				Vision: true, ToolUse: true, Reasoning: true, SearchImpl: SearchImplParams,
				ContextWindow: 1048576, MaxOutput: 65536,
			},
		},
		"gemini-2.5-pro": {
			ID: "gemini-2.5-pro", DisplayName: "Gemini 2.5 Pro", Provider: "google", Enabled: true,
			Capabilities: ModelCapabilities{
				Vision: true, ToolUse: true, Reasoning: true, SearchImpl: SearchImplParams,
				ContextWindow: 1048576, MaxOutput: 65536,
			},
		},
		"gemini-2.5-flash": {
			ID: "gemini-2.5-flash", DisplayName: "Gemini 2.5 Flash", Provider: "google", Enabled: true,
			Capabilities: ModelCapabilities{
				Vision: true, ToolUse: true, Reasoning: true, SearchImpl: SearchImplParams,
				ContextWindow: 1048576, MaxOutput: 65536,
			},
		},

		// ============ DeepSeek ============
		"deepseek-v4-flash": {
			ID: "deepseek-v4-flash", DisplayName: "DeepSeek V4 Flash", Provider: "deepseek", Enabled: true,
			Capabilities: ModelCapabilities{
				ToolUse: true, Reasoning: true, SearchImpl: SearchImplNone,
				ContextWindow: 1000000, MaxOutput: 384000,
			},
		},
		"deepseek-v4-pro": {
			ID: "deepseek-v4-pro", DisplayName: "DeepSeek V4 Pro", Provider: "deepseek", Enabled: true,
			Capabilities: ModelCapabilities{
				ToolUse: true, Reasoning: true, SearchImpl: SearchImplNone,
				ContextWindow: 1000000, MaxOutput: 384000,
			},
		},
		"deepseek-chat": {
			ID: "deepseek-chat", DisplayName: "DeepSeek Chat", Provider: "deepseek", Enabled: true,
			Capabilities: ModelCapabilities{
				ToolUse: true, SearchImpl: SearchImplNone,
				ContextWindow: 1000000, MaxOutput: 384000,
			},
		},
		"deepseek-reasoner": {
			ID: "deepseek-reasoner", DisplayName: "DeepSeek Reasoner", Provider: "deepseek", Enabled: true,
			Capabilities: ModelCapabilities{
				ToolUse: true, Reasoning: true, SearchImpl: SearchImplNone,
				ContextWindow: 1000000, MaxOutput: 384000,
			},
		},

		// ============ Perplexity ============
		"sonar": {
			ID: "sonar", DisplayName: "Sonar", Provider: "perplexity", Enabled: true,
			Capabilities: ModelCapabilities{
				SearchImpl: SearchImplInternal,
			},
		},
		"sonar-pro": {
			ID: "sonar-pro", DisplayName: "Sonar Pro", Provider: "perplexity", Enabled: true,
			Capabilities: ModelCapabilities{
				SearchImpl: SearchImplInternal,
			},
		},
		"sonar-reasoning-pro": {
			ID: "sonar-reasoning-pro", DisplayName: "Sonar Reasoning Pro", Provider: "perplexity", Enabled: true,
			Capabilities: ModelCapabilities{
				Reasoning: true, SearchImpl: SearchImplInternal,
			},
		},
		"sonar-deep-research": {
			ID: "sonar-deep-research", DisplayName: "Sonar Deep Research", Provider: "perplexity", Enabled: true,
			Capabilities: ModelCapabilities{
				Reasoning: true, SearchImpl: SearchImplInternal,
			},
		},
	}
}

// registry 当前生效的内存模型表，默认用内置数据初始化，
// 应用启动后由 LoadModels 用数据库记录整体替换。
var registry = builtinModels()

var mu sync.RWMutex

// Get 根据模型 ID 获取模型信息，找不到返回 nil
func Get(modelID string) *ModelInfo {
	mu.RLock()
	defer mu.RUnlock()
	return registry[modelID]
}

// GetOrDefault 获取模型信息，找不到时返回一个保守默认（不支持原生搜索）
func GetOrDefault(modelID, provider string) *ModelInfo {
	if m := Get(modelID); m != nil {
		return m
	}
	// 未知模型：保守假设支持工具调用、不支持原生搜索
	return &ModelInfo{
		ID:          modelID,
		DisplayName: modelID,
		Provider:    provider,
		Enabled:     true,
		Capabilities: ModelCapabilities{
			ToolUse:    true,
			SearchImpl: SearchImplNone,
		},
	}
}

// List 返回所有启用的模型
func List() []*ModelInfo {
	mu.RLock()
	defer mu.RUnlock()
	result := make([]*ModelInfo, 0, len(registry))
	for _, m := range registry {
		if m.Enabled {
			result = append(result, m)
		}
	}
	return result
}

// Register 注册/覆盖模型（供 Admin 热更新能力标签使用）
func Register(m *ModelInfo) {
	mu.Lock()
	defer mu.Unlock()
	registry[m.ID] = m
}

// LoadModels 用数据库中的模型记录覆盖内存注册表。
// 传入空切片时也会清空注册表，保证生产运行完全以 models 表为事实来源。
func LoadModels(models []*model.Model) {
	mu.Lock()
	defer mu.Unlock()
	registry = make(map[string]*ModelInfo, len(models))
	for _, m := range models {
		registry[m.ID] = &ModelInfo{
			ID:             m.ID,
			DisplayName:    m.DisplayName,
			Provider:       m.Provider,
			Enabled:        m.Enabled,
			ThinkingFormat: m.ThinkingFormat,
			Capabilities: ModelCapabilities{
				Vision:        m.Vision,
				ToolUse:       m.ToolUse,
				Reasoning:     m.Reasoning,
				SearchImpl:    SearchImpl(m.SearchImpl),
				ContextWindow: m.ContextWindow,
				MaxOutput:     m.MaxOutput,
			},
		}
	}
}

// resetRegistryToBuiltins 还原内存表为内置默认（测试用，避免 LoadModels 污染同包其它测试）。
func resetRegistryToBuiltins() {
	mu.Lock()
	defer mu.Unlock()
	registry = builtinModels()
}
