package model

import "time"

type User struct {
	ID           int64      `json:"id"`
	Username     string     `json:"username"`
	Email        *string    `json:"email,omitempty"`
	PasswordHash string     `json:"-"` // 不返回给前端
	Nickname     *string    `json:"nickname,omitempty"`
	AvatarURL    *string    `json:"avatar_url,omitempty"`
	Role         string     `json:"role"`               // admin, user
	GroupID      *int64     `json:"group_id,omitempty"` // 所属分级组，NULL=默认最低级
	Permissions  []byte     `json:"permissions,omitempty"`
	Preferences  []byte     `json:"preferences,omitempty"`
	IsActive     bool       `json:"is_active"`
	AuthVersion  int        `json:"-"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	LastLoginAt  *time.Time `json:"last_login_at,omitempty"`
}

// UserGroup 用户分级组（user_groups 表）。level 越大权限越高。
type UserGroup struct {
	ID                   int64     `json:"id"`
	Name                 string    `json:"name"`
	Level                int       `json:"level"`
	Description          string    `json:"description"`
	IsDefault            bool      `json:"is_default"`
	DailyMessageLimit    int       `json:"daily_message_limit"`
	DailyTokenLimit      int       `json:"daily_token_limit"`
	ConcurrentRunLimit   int       `json:"concurrent_run_limit"`
	DailyToolCallLimit   int       `json:"daily_tool_call_limit"`
	DailyWebSearchLimit  int       `json:"daily_web_search_limit"`
	DailyWebExtractLimit int       `json:"daily_web_extract_limit"`
	DailyOCRFileLimit    int       `json:"daily_ocr_file_limit"`
	DailyOCRPageLimit    int       `json:"daily_ocr_page_limit"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type Session struct {
	ID                      int64      `json:"id"`
	UserID                  int64      `json:"user_id"`
	FolderID                *int64     `json:"folder_id"`
	PinnedAt                *time.Time `json:"pinned_at,omitempty"`
	Title                   string     `json:"title"`
	TitleGenerated          bool       `json:"title_generated"`
	ModelID                 string     `json:"model_id"`
	Provider                string     `json:"provider"`
	SystemPrompt            *string    `json:"system_prompt,omitempty"`
	Temperature             *float64   `json:"temperature,omitempty"`
	MaxTokens               *int       `json:"max_tokens,omitempty"`
	MessageFormat           string     `json:"message_format"` // v1, v2
	SearchMode              string     `json:"search_mode"`    // off, auto, on
	MemoryEnabled           bool       `json:"memory_enabled"` // 会话记忆开关
	AnswerSelectionRevision int64      `json:"answer_selection_revision"`
	Metadata                []byte     `json:"metadata,omitempty"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`
	DeletedAt               *time.Time `json:"deleted_at,omitempty"`
}

type SessionFolder struct {
	ID        int64      `json:"id"`
	UserID    int64      `json:"user_id"`
	Name      string     `json:"name"`
	PinnedAt  *time.Time `json:"pinned_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type Message struct {
	ID                   int64      `json:"id"`
	SessionID            int64      `json:"session_id"`
	SchemaVersion        string     `json:"schema_version"` // v1, v2
	MessageData          []byte     `json:"message_data"`   // JSONB
	Role                 string     `json:"role"`           // 生成列
	HasToolCalls         bool       `json:"has_tool_calls"` // 生成列
	HasReasoning         bool       `json:"has_reasoning"`  // 生成列
	HasMultimodal        bool       `json:"has_multimodal"` // 生成列
	AnswerAttemptID      *int64     `json:"answer_attempt_id,omitempty"`
	CompressedAt         *time.Time `json:"compressed_at,omitempty"`
	CompressionSummaryID *int64     `json:"compression_summary_id,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
	DeletedAt            *time.Time `json:"deleted_at,omitempty"`
}

type ThinkingEffortOption struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"desc"`
}

// Model 模型能力记录（models 表）
// 字段与 modelbank.ModelInfo / ModelCapabilities 对齐，由 repository 层映射。
type Model struct {
	ID             string    `json:"id"`
	DisplayName    string    `json:"display_name"`
	Provider       string    `json:"provider"`
	Vision         bool      `json:"vision"`
	ToolUse        bool      `json:"tool_use"`
	Reasoning      bool      `json:"reasoning"`
	ThinkingFormat string    `json:"thinking_format"` // auto, none, deepseek_v4, openai_reasoning_effort...
	SearchImpl     string    `json:"search_impl"`     // '', internal, params, tool
	ContextWindow  int       `json:"context_window"`
	MaxOutput      int       `json:"max_output"`
	Enabled        bool      `json:"enabled"`
	MinGroupLevel  int       `json:"min_group_level"` // 最低可见组等级，0=所有人可见
	SortOrder      int       `json:"sort_order"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`

	ResolvedThinkingFormat string                 `json:"resolved_thinking_format,omitempty"`
	DefaultThinkingEffort  string                 `json:"default_thinking_effort,omitempty"`
	ThinkingEffortOptions  []ThinkingEffortOption `json:"thinking_effort_options,omitempty"`
	RuntimeProfile         *ModelRuntimeProfile   `json:"runtime_profile,omitempty"`
	ChannelDisplayName     string                 `json:"channel_display_name,omitempty"`
	ChannelAdapter         string                 `json:"channel_adapter,omitempty"`
	ChannelEnabled         bool                   `json:"channel_enabled"`
	ChannelConfigured      bool                   `json:"channel_configured"`
}

type AIChannel struct {
	ID          int64     `json:"id"`
	Key         string    `json:"key"`
	DisplayName string    `json:"display_name"`
	Adapter     string    `json:"adapter"`
	BaseURL     string    `json:"base_url"`
	APIKey      string    `json:"-"`
	APIKeySet   bool      `json:"api_key_set"`
	Enabled     bool      `json:"enabled"`
	SortOrder   int       `json:"sort_order"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type ExternalService struct {
	ID             int64     `json:"id"`
	Key            string    `json:"key"`
	DisplayName    string    `json:"display_name"`
	Kind           string    `json:"kind"`
	BaseURL        string    `json:"base_url"`
	APIKey         string    `json:"-"`
	APIKeySet      bool      `json:"api_key_set"`
	Enabled        bool      `json:"enabled"`
	SortOrder      int       `json:"sort_order"`
	MaxConcurrency int       `json:"max_concurrency"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type ToolConfig struct {
	ID             int64     `json:"id"`
	Key            string    `json:"key"`
	DisplayName    string    `json:"display_name"`
	Enabled        bool      `json:"enabled"`
	TimeoutSeconds int       `json:"timeout_seconds"`
	SortOrder      int       `json:"sort_order"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type ModelRuntimeProfile struct {
	Family                string                 `json:"family"`
	WireProtocol          string                 `json:"wire_protocol"`
	ThinkingFormat        string                 `json:"thinking_format"`
	DefaultThinkingEffort string                 `json:"default_thinking_effort,omitempty"`
	ThinkingEffortOptions []ThinkingEffortOption `json:"thinking_effort_options,omitempty"`
	SupportsVision        bool                   `json:"supports_vision"`
	SupportsTools         bool                   `json:"supports_tools"`
	SearchImpl            string                 `json:"search_impl"`
}

// Prompt 预设提示词
type Prompt struct {
	ID          int64     `json:"id"`
	UserID      int64     `json:"user_id"`
	Title       string    `json:"title"`
	Content     string    `json:"content"`
	Description *string   `json:"description,omitempty"`
	Tags        []string  `json:"tags"`
	GroupID     *int64    `json:"group_id,omitempty"`
	GroupName   string    `json:"group_name"`
	IsPublic    bool      `json:"is_public"`
	UseCount    int       `json:"use_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type PromptGroup struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Skill 文字型 Agent skill。
//
// 新版 Skills 不再把正文长期保存在 PostgreSQL，也不再自动把正文注入系统提示。
// 数据库只保存元数据和权限门槛；真正的 SKILL.md / references 文本落在
// data/storage/skills 下，并由 skill_read / skill_search 工具按需读取。
type Skill struct {
	ID              string      `json:"id"`
	Name            string      `json:"name"`
	Description     string      `json:"description"`
	Content         string      `json:"-"`
	SourceType      string      `json:"source_type"`
	SourceURL       *string     `json:"source_url,omitempty"`
	SourceRef       *string     `json:"source_ref,omitempty"`
	SourcePath      *string     `json:"source_path,omitempty"`
	Checksum        string      `json:"checksum"`
	PackageChecksum string      `json:"package_checksum"`
	EntryPath       string      `json:"entry_path"`
	MinGroupLevel   int         `json:"min_group_level"`
	Files           []SkillFile `json:"files,omitempty"`
	Enabled         bool        `json:"enabled"`
	IsBuiltin       bool        `json:"is_builtin"`
	CreatedBy       *int64      `json:"created_by,omitempty"`
	CreatedAt       time.Time   `json:"created_at"`
	UpdatedAt       time.Time   `json:"updated_at"`
	DeletedAt       *time.Time  `json:"deleted_at,omitempty"`
}

type SkillFile struct {
	ID           int64     `json:"id,omitempty"`
	SkillID      string    `json:"skill_id,omitempty"`
	RelativePath string    `json:"path"`
	StoragePath  string    `json:"-"`
	Kind         string    `json:"kind"`
	Size         int64     `json:"size"`
	Checksum     string    `json:"checksum"`
	CreatedAt    time.Time `json:"created_at,omitempty"`
}

// SkillImportRecord 记录一次 Skill 包写入动作。
//
// Skills 的正文已经从数据库迁到 data/storage/skills，单看当前 skills 行只能知道
// “现在是什么”，不知道“这次包从哪里来、管理员当时选了哪些 reference”。更新检查
// 和重复导入确认都需要这层审计信息：它不参与运行态读取，只负责解释历史和支撑后续回滚。
type SkillImportRecord struct {
	ID              int64     `json:"id"`
	SkillID         string    `json:"skill_id"`
	Action          string    `json:"action"`
	SourceType      string    `json:"source_type"`
	SourceURL       *string   `json:"source_url,omitempty"`
	SourceRef       *string   `json:"source_ref,omitempty"`
	SourcePath      string    `json:"source_path"`
	UpstreamSkillID string    `json:"upstream_skill_id"`
	UpstreamName    string    `json:"upstream_name"`
	PackageChecksum string    `json:"package_checksum"`
	SelectedFiles   []byte    `json:"-"`
	FileManifest    []byte    `json:"-"`
	ImportReport    []byte    `json:"-"`
	CreatedBy       *int64    `json:"created_by,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type FontAsset struct {
	ID          int64      `json:"id"`
	DisplayName string     `json:"display_name"`
	FamilyName  string     `json:"family_name"`
	FileName    string     `json:"file_name"`
	FilePath    string     `json:"-"`
	FileURL     string     `json:"file_url,omitempty"`
	MimeType    string     `json:"mime_type"`
	FileSize    int64      `json:"file_size"`
	Checksum    string     `json:"checksum"`
	Weight      int        `json:"weight"`
	Style       string     `json:"style"`
	Enabled     bool       `json:"enabled"`
	CreatedBy   *int64     `json:"created_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
}

// File 上传文件元数据
type File struct {
	ID                int64      `json:"id"`
	UserID            int64      `json:"user_id"`
	SessionID         *int64     `json:"session_id,omitempty"`
	FileName          string     `json:"file_name"`
	FilePath          string     `json:"-"`
	FileType          string     `json:"file_type"`
	FileSize          int64      `json:"file_size"`
	FileHash          *string    `json:"file_hash,omitempty"`
	Status            string     `json:"status"`
	ExtractedTextPath *string    `json:"-"` // 提取正文文件路径，仅供 file_read 工具按需读取
	ExtractStatus     string     `json:"extract_status"`
	ExtractError      *string    `json:"extract_error,omitempty"`
	TokenEstimate     int        `json:"token_estimate"`
	OCRProvider       *string    `json:"ocr_provider,omitempty"`
	OCRTaskID         *string    `json:"ocr_task_id,omitempty"`
	OCRPageCount      int        `json:"ocr_page_count"`
	OCRProgressPages  int        `json:"ocr_progress_pages"`
	OCRStartedAt      *time.Time `json:"ocr_started_at,omitempty"`
	OCRCompletedAt    *time.Time `json:"ocr_completed_at,omitempty"`
	OCRErrorType      *string    `json:"ocr_error_type,omitempty"`
	OCRSourcePath     *string    `json:"-"`
	OCRLeaseUntil     *time.Time `json:"-"`
	OCRAttempts       int        `json:"-"`
	OCRNextRetryAt    *time.Time `json:"-"`
	CreatedAt         time.Time  `json:"created_at"`
	DeletedAt         *time.Time `json:"deleted_at,omitempty"`
}
