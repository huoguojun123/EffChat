package modelbank

// SearchImpl 模型搜索实现类型（对齐 LobeHub ModelSearchImplement）
type SearchImpl string

const (
	// SearchImplNone 模型不支持原生搜索，需挂载应用内置工具（SearXNG）
	SearchImplNone SearchImpl = ""
	// SearchImplInternal 模型内部搜索，对调用方透明（如 Perplexity、Jina）
	// 无需任何处理，直接调用即可
	SearchImplInternal SearchImpl = "internal"
	// SearchImplParams 通过参数开关启用搜索（如 Gemini、Qwen）
	// 传 enable_search / web_search_options 等参数
	SearchImplParams SearchImpl = "params"
	// SearchImplTool 通过 tool calling 实现搜索
	SearchImplTool SearchImpl = "tool"
)

// ModelCapabilities 模型能力声明
type ModelCapabilities struct {
	// Vision 支持图片输入
	Vision bool `json:"vision"`
	// ToolUse 支持工具调用（function calling）
	ToolUse bool `json:"tool_use"`
	// Reasoning 支持思维链/推理输出（thinking）
	Reasoning bool `json:"reasoning"`
	// SearchImpl 原生搜索实现类型，为空表示不支持
	SearchImpl SearchImpl `json:"search_impl"`
	// ContextWindow 上下文窗口大小（tokens）
	ContextWindow int `json:"context_window"`
	// MaxOutput 最大输出 tokens
	MaxOutput int `json:"max_output"`
}

// ModelInfo 模型信息
type ModelInfo struct {
	ID           string            `json:"id"`           // 模型 ID（如 gpt-4o-mini）
	DisplayName  string            `json:"display_name"` // 展示名称
	Provider     string            `json:"provider"`     // 提供商
	Capabilities ModelCapabilities `json:"capabilities"` // 能力
	Enabled      bool              `json:"enabled"`      // 是否启用
	// ThinkingFormat 是管理员配置的思考参数格式覆盖项。auto 表示按 model_id 推断。
	ThinkingFormat string `json:"thinking_format"`
}

// HasBuiltinSearch 模型是否有原生搜索能力
func (m *ModelInfo) HasBuiltinSearch() bool {
	return m.Capabilities.SearchImpl != SearchImplNone
}

// IsSearchInternal 模型搜索是否为透明内部实现
func (m *ModelInfo) IsSearchInternal() bool {
	return m.Capabilities.SearchImpl == SearchImplInternal
}

// IsSearchParams 模型搜索是否为参数开关型
func (m *ModelInfo) IsSearchParams() bool {
	return m.Capabilities.SearchImpl == SearchImplParams
}

// IsSearchTool 模型搜索是否为工具调用型
func (m *ModelInfo) IsSearchTool() bool {
	return m.Capabilities.SearchImpl == SearchImplTool
}
