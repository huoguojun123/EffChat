package usage

import "time"

const (
	KindChat              = "chat"
	KindRetry             = "retry"
	KindTitle             = "title"
	KindCompression       = "compression"
	KindToolChain         = "tool_chain"
	KindMemoryMaintenance = "memory_maintenance"
)

// Meta 描述一次模型调用所属的业务上下文。
//
// 它不是给模型看的提示词，只是统计标签。放进 context 后，最底层的 ChatModel wrapper
// 在真正调用上游 API 前后读取这些标签并落库。这样统计逻辑贴在模型 API 边界，而不是
// 分散到聊天、标题、压缩、工具链每条业务路径里。
type Meta struct {
	UserID    int64
	SessionID int64
	MessageID int64
	RunID     string
	Kind      string
	Provider  string
	ModelID   string
}

type Event struct {
	ID               int64      `json:"id"`
	UserID           int64      `json:"user_id,omitempty"`
	SessionID        int64      `json:"session_id,omitempty"`
	MessageID        int64      `json:"message_id,omitempty"`
	RunID            string     `json:"run_id,omitempty"`
	Kind             string     `json:"kind"`
	Provider         string     `json:"provider"`
	ModelID          string     `json:"model_id"`
	Success          bool       `json:"success"`
	PromptTokens     int        `json:"prompt_tokens"`
	CompletionTokens int        `json:"completion_tokens"`
	TotalTokens      int        `json:"total_tokens"`
	CachedTokens     int        `json:"cached_tokens"`
	ReasoningTokens  int        `json:"reasoning_tokens"`
	DurationMs       int64      `json:"duration_ms"`
	ErrorType        string     `json:"error_type,omitempty"`
	ErrorMessage     string     `json:"error_message,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	LastCalledAt     *time.Time `json:"last_called_at,omitempty"`
}

type ToolEvent struct {
	ID            int64     `json:"id"`
	UserID        int64     `json:"user_id,omitempty"`
	SessionID     int64     `json:"session_id,omitempty"`
	RunID         string    `json:"run_id,omitempty"`
	CallID        string    `json:"call_id,omitempty"`
	ToolKey       string    `json:"tool_key"`
	Success       bool      `json:"success"`
	ContextTokens int       `json:"context_tokens"`
	Truncated     bool      `json:"truncated"`
	DurationMs    int64     `json:"duration_ms"`
	ErrorType     string    `json:"error_type,omitempty"`
	ErrorMessage  string    `json:"error_message,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type Totals struct {
	Requests          int64      `json:"requests"`
	Successes         int64      `json:"successes"`
	Failures          int64      `json:"failures"`
	Canceled          int64      `json:"canceled"`
	PromptTokens      int64      `json:"prompt_tokens"`
	CompletionTokens  int64      `json:"completion_tokens"`
	TotalTokens       int64      `json:"total_tokens"`
	CachedTokens      int64      `json:"cached_tokens"`
	ReasoningTokens   int64      `json:"reasoning_tokens"`
	ToolCalls         int64      `json:"tool_calls"`
	WebSearchCalls    int64      `json:"web_search_calls"`
	WebExtractCalls   int64      `json:"web_extract_calls"`
	OCRFiles          int64      `json:"ocr_files"`
	OCRPages          int64      `json:"ocr_pages"`
	OCRFailures       int64      `json:"ocr_failures"`
	ToolContextTokens int64      `json:"tool_context_tokens"`
	AvgDurationMs     int64      `json:"avg_duration_ms"`
	LastCalledAt      *time.Time `json:"last_called_at,omitempty"`
}

type RunTotals struct {
	Runs           int64      `json:"runs"`
	Running        int64      `json:"running"`
	Completed      int64      `json:"completed"`
	Failed         int64      `json:"failed"`
	Canceled       int64      `json:"canceled"`
	UserStopped    int64      `json:"user_stopped"`
	SystemCanceled int64      `json:"system_canceled"`
	AvgDurationMs  int64      `json:"avg_duration_ms"`
	LastAcceptedAt *time.Time `json:"last_accepted_at,omitempty"`
}

type ByUser struct {
	UserID   int64  `json:"user_id,omitempty"`
	Username string `json:"username"`
	Totals
}

type ByModel struct {
	Provider string `json:"provider"`
	ModelID  string `json:"model_id"`
	Totals
}

type ByKind struct {
	Kind string `json:"kind"`
	Totals
}

type ToolTotals struct {
	Calls           int64      `json:"calls"`
	Successes       int64      `json:"successes"`
	Failures        int64      `json:"failures"`
	Degraded        int64      `json:"degraded"`
	WebSearchCalls  int64      `json:"web_search_calls"`
	WebExtractCalls int64      `json:"web_extract_calls"`
	ContextTokens   int64      `json:"context_tokens"`
	Truncated       int64      `json:"truncated"`
	AvgDurationMs   int64      `json:"avg_duration_ms"`
	LastCalledAt    *time.Time `json:"last_called_at,omitempty"`
}

type ByTool struct {
	ToolKey string `json:"tool_key"`
	ToolTotals
}

type QuotaUserUsage struct {
	UserID               int64     `json:"user_id"`
	Username             string    `json:"username"`
	GroupID              *int64    `json:"group_id,omitempty"`
	GroupName            string    `json:"group_name"`
	DailyMessageLimit    int       `json:"daily_message_limit"`
	DailyTokenLimit      int       `json:"daily_token_limit"`
	ConcurrentRunLimit   int       `json:"concurrent_run_limit"`
	DailyToolCallLimit   int       `json:"daily_tool_call_limit"`
	DailyWebSearchLimit  int       `json:"daily_web_search_limit"`
	DailyWebExtractLimit int       `json:"daily_web_extract_limit"`
	DailyOCRFileLimit    int       `json:"daily_ocr_file_limit"`
	DailyOCRPageLimit    int       `json:"daily_ocr_page_limit"`
	DailyMessages        int64     `json:"daily_messages"`
	DailyModelTokens     int64     `json:"daily_model_tokens"`
	DailyToolCalls       int64     `json:"daily_tool_calls"`
	DailyWebSearches     int64     `json:"daily_web_searches"`
	DailyWebExtracts     int64     `json:"daily_web_extracts"`
	DailyOCRFiles        int64     `json:"daily_ocr_files"`
	DailyOCRPages        int64     `json:"daily_ocr_pages"`
	ResetAt              time.Time `json:"reset_at"`
}

type Summary struct {
	Totals     Totals           `json:"totals"`
	RunTotals  RunTotals        `json:"run_totals"`
	ByUser     []ByUser         `json:"by_user"`
	ByModel    []ByModel        `json:"by_model"`
	ByKind     []ByKind         `json:"by_kind"`
	ToolTotals ToolTotals       `json:"tool_totals"`
	ByTool     []ByTool         `json:"by_tool"`
	QuotaUsers []QuotaUserUsage `json:"quota_users"`
}
