package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/service"
)

const acceptedRuntimeSnapshotVersion = 2

type AcceptedRuntimeSnapshot struct {
	Version              int                              `json:"version"`
	CapturedAt           time.Time                        `json:"captured_at"`
	ModelID              string                           `json:"model_id"`
	Provider             string                           `json:"provider"`
	ChannelKey           string                           `json:"channel_key"`
	ContextWindow        int                              `json:"context_window"`
	ModelMaxOutput       int                              `json:"model_max_output"`
	RequestedMaxOutput   int                              `json:"requested_max_output,omitempty"`
	ThinkingFormat       string                           `json:"thinking_format"`
	ThinkingEffort       string                           `json:"thinking_effort,omitempty"`
	ModelChecksum        string                           `json:"model_checksum"`
	ChannelChecksum      string                           `json:"channel_checksum"`
	RequestChecksum      string                           `json:"request_checksum"`
	PromptChecksum       string                           `json:"prompt_checksum"`
	ConfigChecksum       string                           `json:"config_checksum"`
	MemoryChecksum       string                           `json:"memory_checksum"`
	Skills               []RuntimeDependencyRef           `json:"skills,omitempty"`
	SkillsChecksum       string                           `json:"skills_checksum"`
	EnabledTools         []string                         `json:"enabled_tools,omitempty"`
	ToolConfigChecksum   string                           `json:"tool_config_checksum"`
	ToolConfigState      service.RuntimeConfigState       `json:"tool_config_state"`
	SearchProviders      []string                         `json:"search_providers,omitempty"`
	ExtractProviders     []string                         `json:"extract_providers,omitempty"`
	SearchConfigChecksum string                           `json:"search_config_checksum"`
	SearchConfigState    service.SearchRuntimeConfigState `json:"search_config_state"`
	MemoryState          service.RuntimeConfigState       `json:"memory_state"`
	Checksum             string                           `json:"checksum"`
}

type RuntimeDependencyRef struct {
	ID       string `json:"id"`
	Checksum string `json:"checksum"`
}

type runtimeModelMaterial struct {
	ID            string
	Provider      string
	Vision        bool
	ToolUse       bool
	Reasoning     bool
	Thinking      string
	SearchImpl    modelbank.SearchImpl
	ContextWindow int
	MaxOutput     int
}

type runtimeChannelMaterial struct {
	Key     string
	Adapter string
	BaseURL string
	APIKey  string
	Enabled bool
}

type runtimeRequestMaterial struct {
	SystemName              string
	SystemPrompt            string
	Temperature             *float64
	MaxTokens               int
	SchemaVersion           string
	MessageFormat           string
	SessionTitle            string
	SessionMetadata         []byte
	UserName                string
	UserNickname            string
	UserDisplayName         string
	UserRole                string
	UserPreferences         []byte
	Reasoning               bool
	ThinkingFormat          string
	ThinkingEffort          string
	SearchMode              modelbank.SearchMode
	PreferModelNativeSearch bool
	MemoryEnabled           bool
}

type runtimeSkillMaterial struct {
	ID          string
	Name        string
	Description string
	Files       []runtimeSkillFileMaterial
}

type runtimeSkillFileMaterial struct {
	Path     string
	Kind     string
	Size     int64
	Checksum string
}

type runtimeConfigMaterial struct {
	CompressionContextThreshold int
	ExtractSummaryEnabled       bool
	ExtractSummaryModel         string
	MemoryMaxChars              int
}

func (a *EinoAgent) CaptureAcceptedRuntimeSnapshot(ctx context.Context, req *ChatRequest) (json.RawMessage, error) {
	snapshot, err := a.captureAcceptedRuntimeSnapshot(ctx, req)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("marshal accepted runtime snapshot: %w", err)
	}
	if len(raw) > 16384 {
		return nil, fmt.Errorf("accepted runtime snapshot exceeds size limit")
	}
	return raw, nil
}

func (a *EinoAgent) ValidateAcceptedRuntimeSnapshot(ctx context.Context, req *ChatRequest, raw json.RawMessage) error {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "{}" {
		return nil
	}
	var expected AcceptedRuntimeSnapshot
	if err := json.Unmarshal(raw, &expected); err != nil || expected.Version != acceptedRuntimeSnapshotVersion || expected.Checksum == "" {
		return runtimeDependencyChangedError()
	}
	if req == nil || req.ModelID != expected.ModelID || req.Provider != expected.Provider {
		return runtimeDependencyChangedError()
	}

	currentReq := *req
	currentReq.PromptTime = expected.CapturedAt
	currentReq.RuntimeResolved = false
	currentReq.RuntimeChannel = nil
	currentReq.RuntimePromptTemplate = ""
	currentReq.RuntimeMemory = nil
	currentReq.RuntimeMemoryState = service.RuntimeConfigState{}
	currentReq.RuntimeToolConfig = nil
	currentReq.RuntimeToolConfigState = service.RuntimeConfigState{}
	currentReq.RuntimeSearchConfig = service.SearchRuntimeConfig{}
	currentReq.RuntimeSearchConfigState = service.SearchRuntimeConfigState{}
	current, err := a.captureAcceptedRuntimeSnapshot(ctx, &currentReq)
	if err != nil {
		return runtimeDependencyChangedError()
	}
	if current.Checksum != expected.Checksum {
		return runtimeDependencyChangedError()
	}

	req.PromptTime = expected.CapturedAt
	req.RuntimeResolved = true
	req.RuntimeChannel = currentReq.RuntimeChannel
	req.RuntimePromptTemplate = currentReq.RuntimePromptTemplate
	req.RuntimeMemory = currentReq.RuntimeMemory
	req.RuntimeMemoryState = currentReq.RuntimeMemoryState
	req.RuntimeToolConfig = currentReq.RuntimeToolConfig
	req.RuntimeToolConfigState = currentReq.RuntimeToolConfigState
	req.RuntimeSearchConfig = currentReq.RuntimeSearchConfig
	req.RuntimeSearchConfigState = currentReq.RuntimeSearchConfigState
	req.ContextWindow = expected.ContextWindow
	req.ModelMaxOutput = expected.ModelMaxOutput
	req.Vision = currentReq.Vision
	req.ToolUse = currentReq.ToolUse
	req.Reasoning = currentReq.Reasoning
	req.ThinkingFormat = expected.ThinkingFormat
	req.ThinkingEffort = expected.ThinkingEffort
	req.SearchImpl = currentReq.SearchImpl
	return nil
}

func ParseAcceptedRuntimeSnapshot(raw json.RawMessage) (*AcceptedRuntimeSnapshot, error) {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "{}" {
		return nil, nil
	}
	var snapshot AcceptedRuntimeSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return nil, err
	}
	if snapshot.Version != acceptedRuntimeSnapshotVersion || snapshot.Checksum == "" {
		return nil, fmt.Errorf("invalid accepted runtime snapshot")
	}
	return &snapshot, nil
}

func (a *EinoAgent) captureAcceptedRuntimeSnapshot(ctx context.Context, req *ChatRequest) (*AcceptedRuntimeSnapshot, error) {
	if a == nil || req == nil {
		return nil, fmt.Errorf("runtime snapshot is unavailable")
	}
	modelInfo := modelbank.Get(req.ModelID)
	if modelInfo == nil || !modelInfo.Enabled || strings.TrimSpace(modelInfo.Provider) != strings.TrimSpace(req.Provider) {
		return nil, fmt.Errorf("accepted model is no longer available")
	}
	channel, err := a.channelService.ResolveAIChannel(req.Provider)
	if err != nil {
		return nil, fmt.Errorf("accepted channel is no longer available: %w", err)
	}
	templateText, err := loadPromptTemplate(a.configRepo)
	if err != nil {
		return nil, err
	}

	capturedAt := req.PromptTime
	if capturedAt.IsZero() {
		capturedAt = time.Now().UTC()
	}
	thinkingEffort := modelbank.NormalizeThinkingEffort(req.ThinkingEffort)
	if thinkingEffort == string(modelbank.ThinkingEffortAuto) || !modelbank.IsValidThinkingEffort(thinkingEffort) {
		thinkingEffort = ""
	}
	req.ThinkingEffort = thinkingEffort
	modelMaterial := runtimeModelMaterial{
		ID:            modelInfo.ID,
		Provider:      modelInfo.Provider,
		Vision:        modelInfo.Capabilities.Vision,
		ToolUse:       modelInfo.Capabilities.ToolUse,
		Reasoning:     modelInfo.Capabilities.Reasoning,
		Thinking:      modelInfo.ThinkingFormat,
		SearchImpl:    modelInfo.Capabilities.SearchImpl,
		ContextWindow: modelInfo.Capabilities.ContextWindow,
		MaxOutput:     modelInfo.Capabilities.MaxOutput,
	}
	channelMaterial := runtimeChannelMaterial{
		Key: channel.Key, Adapter: channel.Adapter, BaseURL: channel.BaseURL,
		APIKey: channel.APIKey, Enabled: channel.Enabled,
	}
	requestMaterial := runtimeRequestMaterial{
		SystemName: req.SystemName, SystemPrompt: req.SystemPrompt, Temperature: req.Temperature,
		MaxTokens: req.MaxTokens, SchemaVersion: req.SchemaVersion, MessageFormat: req.MessageFormat,
		SessionTitle: req.SessionTitle, SessionMetadata: req.SessionMetadata,
		UserName: req.UserName, UserNickname: req.UserNickname, UserDisplayName: req.UserDisplayName,
		UserRole: req.UserRole, UserPreferences: req.UserPreferences,
		Reasoning: modelInfo.Capabilities.Reasoning, ThinkingFormat: modelInfo.ThinkingFormat,
		ThinkingEffort: thinkingEffort, SearchMode: req.SearchMode,
		PreferModelNativeSearch: req.PreferModelNativeSearch, MemoryEnabled: req.MemoryEnabled,
	}

	memory, memoryState := a.readRuntimeMemory(ctx, req.SessionID, req.MemoryEnabled)
	toolRuntime, toolRuntimeState := a.resolveToolRuntimeConfigWithState()
	searchRuntime, searchRuntimeState := a.resolveSearchRuntimeConfigWithState()
	configMaterial := runtimeConfigMaterial{
		CompressionContextThreshold: a.compressionContextThreshold(),
		ExtractSummaryEnabled:       true,
		ExtractSummaryModel:         "claude-haiku-4-5",
		MemoryMaxChars:              a.memoryLimits().MaxChars,
	}
	if a.configRepo != nil {
		configMaterial.ExtractSummaryEnabled = a.configRepo.GetBool("extract_summary_enabled", true)
		configMaterial.ExtractSummaryModel = a.configRepo.GetString("extract_summary_model", configMaterial.ExtractSummaryModel)
	}

	req.PromptTime = capturedAt
	req.RuntimeResolved = true
	channelCopy := *channel
	req.RuntimeChannel = &channelCopy
	req.RuntimePromptTemplate = templateText
	if memoryState.State == service.RuntimeStateUnavailable {
		req.RuntimeMemory = nil
	} else {
		req.RuntimeMemory = stringPointer(memory)
	}
	req.RuntimeMemoryState = memoryState
	req.RuntimeToolConfig = cloneToolRuntimeConfig(toolRuntime)
	req.RuntimeToolConfigState = toolRuntimeState
	req.RuntimeSearchConfig = searchRuntime
	req.RuntimeSearchConfigState = searchRuntimeState
	req.ContextWindow = modelMaterial.ContextWindow
	req.ModelMaxOutput = modelMaterial.MaxOutput
	req.Vision = modelMaterial.Vision
	req.ToolUse = modelMaterial.ToolUse
	req.Reasoning = modelMaterial.Reasoning
	req.ThinkingFormat = modelMaterial.Thinking
	req.SearchImpl = modelMaterial.SearchImpl

	plan := a.buildRuntimeContextPlan(req)
	enabledTools := enabledToolNames(plan.mountedTools)
	skillRefs, skillChecksum := runtimeSkillRefs(req.EnabledSkills)
	snapshot := &AcceptedRuntimeSnapshot{
		Version:              acceptedRuntimeSnapshotVersion,
		CapturedAt:           capturedAt,
		ModelID:              req.ModelID,
		Provider:             req.Provider,
		ChannelKey:           channel.Key,
		ContextWindow:        modelMaterial.ContextWindow,
		ModelMaxOutput:       modelMaterial.MaxOutput,
		RequestedMaxOutput:   req.MaxTokens,
		ThinkingFormat:       modelMaterial.Thinking,
		ThinkingEffort:       thinkingEffort,
		ModelChecksum:        checksumValue("model", modelMaterial),
		ChannelChecksum:      checksumValue("channel", channelMaterial),
		RequestChecksum:      checksumValue("request", requestMaterial),
		PromptChecksum:       checksumValue("prompt", templateText),
		ConfigChecksum:       checksumValue("config", configMaterial),
		MemoryChecksum:       checksumValue("memory", memory),
		Skills:               skillRefs,
		SkillsChecksum:       skillChecksum,
		EnabledTools:         enabledTools,
		ToolConfigChecksum:   checksumValue("tools", toolRuntime),
		ToolConfigState:      toolRuntimeState,
		SearchProviders:      append([]string(nil), searchRuntime.SearchProviders...),
		ExtractProviders:     append([]string(nil), searchRuntime.CrawlerProviders...),
		SearchConfigChecksum: checksumValue("search", searchRuntime),
		SearchConfigState:    searchRuntimeState,
		MemoryState:          memoryState,
	}
	snapshot.Checksum = acceptedRuntimeChecksum(snapshot)
	return snapshot, nil
}

func (a *EinoAgent) readRuntimeMemory(ctx context.Context, sessionID int64, enabled bool) (string, service.RuntimeConfigState) {
	if !enabled {
		return "", service.RuntimeConfigState{State: service.RuntimeStateDisabled, Cause: "session_disabled", Version: "disabled"}
	}
	if a == nil || a.memoryRepo == nil || sessionID <= 0 {
		return "", service.RuntimeConfigState{State: service.RuntimeStateUnavailable, Cause: "repository_unavailable", Version: "unavailable"}
	}
	value, updatedAt, err := a.memoryRepo.GetWithUpdatedAt(ctx, sessionID)
	if err != nil {
		return "", service.RuntimeConfigState{State: service.RuntimeStateUnavailable, Cause: "repository_unavailable", Version: "unavailable"}
	}
	if updatedAt.IsZero() {
		return "", service.RuntimeConfigState{State: service.RuntimeStateUnconfigured, Cause: "empty_memory", Version: "empty"}
	}
	return value, service.RuntimeConfigState{State: service.RuntimeStateReady, Version: updatedAt.UTC().Format(time.RFC3339Nano)}
}

func runtimeSkillRefs(skills []SkillInstruction) ([]RuntimeDependencyRef, string) {
	materials := make([]runtimeSkillMaterial, 0, len(skills))
	for _, skill := range skills {
		files := make([]runtimeSkillFileMaterial, 0, len(skill.Files))
		for _, file := range skill.Files {
			files = append(files, runtimeSkillFileMaterial{
				Path: file.RelativePath, Kind: file.Kind, Size: file.Size, Checksum: file.Checksum,
			})
		}
		sort.Slice(files, func(i, j int) bool {
			if files[i].Path == files[j].Path {
				return files[i].Kind < files[j].Kind
			}
			return files[i].Path < files[j].Path
		})
		materials = append(materials, runtimeSkillMaterial{
			ID: skill.ID, Name: skill.Name, Description: skill.Description, Files: files,
		})
	}
	sort.Slice(materials, func(i, j int) bool { return materials[i].ID < materials[j].ID })
	refs := make([]RuntimeDependencyRef, 0, len(materials))
	for _, material := range materials {
		refs = append(refs, RuntimeDependencyRef{
			ID: material.ID, Checksum: checksumValue("skill:"+material.ID, material),
		})
	}
	return refs, checksumValue("skills", materials)
}

func acceptedRuntimeChecksum(snapshot *AcceptedRuntimeSnapshot) string {
	copy := *snapshot
	copy.Checksum = ""
	return checksumValue("accepted-runtime", copy)
}

func checksumValue(domain string, value interface{}) string {
	raw, _ := json.Marshal(struct {
		Domain string      `json:"domain"`
		Value  interface{} `json:"value"`
	}{Domain: domain, Value: value})
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func cloneToolRuntimeConfig(input service.ToolRuntimeConfigSet) service.ToolRuntimeConfigSet {
	if input == nil {
		return nil
	}
	out := make(service.ToolRuntimeConfigSet, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func stringPointer(value string) *string {
	copy := value
	return &copy
}

func runtimeDependencyChangedError() error {
	return &RuntimeError{
		Code:      "runtime_dependency_changed",
		Message:   "本轮运行配置在开始前已变化，请重新发送",
		Category:  RuntimeErrorServerUpdate,
		Retryable: true,
	}
}
