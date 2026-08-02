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

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/service"
)

const acceptedRuntimeSnapshotVersion = 4

type AcceptedRuntimeSnapshot struct {
	Version                int                              `json:"version"`
	CapturedAt             time.Time                        `json:"captured_at"`
	ModelID                string                           `json:"model_id"`
	Provider               string                           `json:"provider"`
	ChannelKey             string                           `json:"channel_key"`
	ContextWindow          int                              `json:"context_window"`
	ModelMaxOutput         int                              `json:"model_max_output"`
	RequestedMaxOutput     int                              `json:"requested_max_output,omitempty"`
	ThinkingFormat         string                           `json:"thinking_format"`
	ThinkingEffort         string                           `json:"thinking_effort,omitempty"`
	ModelChecksum          string                           `json:"model_checksum"`
	ChannelChecksum        string                           `json:"channel_checksum"`
	RequestChecksum        string                           `json:"request_checksum"`
	PromptChecksum         string                           `json:"prompt_checksum"`
	ConfigChecksum         string                           `json:"config_checksum"`
	ExtractSummaryChecksum string                           `json:"extract_summary_checksum"`
	MemoryChecksum         string                           `json:"memory_checksum"`
	Skills                 []RuntimeDependencyRef           `json:"skills,omitempty"`
	SkillsChecksum         string                           `json:"skills_checksum"`
	EnabledTools           []string                         `json:"enabled_tools,omitempty"`
	ToolConfigChecksum     string                           `json:"tool_config_checksum"`
	ToolConfigState        service.RuntimeConfigState       `json:"tool_config_state"`
	SearchProviders        []string                         `json:"search_providers,omitempty"`
	ExtractProviders       []string                         `json:"extract_providers,omitempty"`
	SearchConfigChecksum   string                           `json:"search_config_checksum"`
	SearchConfigState      service.SearchRuntimeConfigState `json:"search_config_state"`
	ExtractSummaryState    service.RuntimeConfigState       `json:"extract_summary_state"`
	MemoryState            service.RuntimeConfigState       `json:"memory_state"`
	Checksum               string                           `json:"checksum"`
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
	// Keep the backwards-compatible configurable default out of the checksum
	// JSON so pre-047 accepted runs are not invalidated by a no-op schema field.
	// Omit/fixed policies remain checksum-owned runtime dependencies.
	TemperaturePolicy string   `json:"TemperaturePolicy,omitempty"`
	TemperatureValue  *float64 `json:"TemperatureValue,omitempty"`
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
	ExtractSummaryState         service.RuntimeConfigState
	MemoryMaxChars              int
}

type runtimeExtractSummaryMaterial struct {
	Enabled           bool
	ConfiguredModelID string
	State             service.RuntimeConfigState
	Available         bool
	Model             runtimeModelMaterial
	Channel           runtimeChannelMaterial
}

func runtimeModelInfoMaterial(info *modelbank.ModelInfo) runtimeModelMaterial {
	if info == nil {
		return runtimeModelMaterial{}
	}
	policy := model.NormalizeTemperaturePolicy(info.TemperaturePolicy)
	materialPolicy := policy
	if policy == model.TemperaturePolicyConfigurable {
		materialPolicy = ""
	}
	return runtimeModelMaterial{
		ID:                info.ID,
		Provider:          info.Provider,
		Vision:            info.Capabilities.Vision,
		ToolUse:           info.Capabilities.ToolUse,
		Reasoning:         info.Capabilities.Reasoning,
		Thinking:          info.ThinkingFormat,
		SearchImpl:        info.Capabilities.SearchImpl,
		ContextWindow:     info.Capabilities.ContextWindow,
		MaxOutput:         info.Capabilities.MaxOutput,
		TemperaturePolicy: materialPolicy,
		TemperatureValue:  cloneFloat64Value(info.TemperatureValue),
	}
}

func cloneFloat64Value(value *float64) *float64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func runtimeAIChannelMaterial(channel *model.AIChannel) runtimeChannelMaterial {
	if channel == nil {
		return runtimeChannelMaterial{}
	}
	return runtimeChannelMaterial{
		Key:     channel.Key,
		Adapter: channel.Adapter,
		BaseURL: channel.BaseURL,
		APIKey:  channel.APIKey,
		Enabled: channel.Enabled,
	}
}

// resolveAcceptedExtractSummaryRuntime resolves the exact utility dependency
// selected at admission. Non-context availability failures preserve the main
// run and become a fixed per-run refinement downgrade; cancellation and setup
// deadlines still abort admission so they cannot be mistaken for availability.
func (a *EinoAgent) resolveAcceptedExtractSummaryRuntime(
	ctx context.Context,
	config runtimeConfigMaterial,
) (*modelbank.ModelInfo, *model.AIChannel, runtimeExtractSummaryMaterial, error) {
	material := runtimeExtractSummaryMaterial{
		Enabled:           config.ExtractSummaryEnabled,
		ConfiguredModelID: strings.TrimSpace(config.ExtractSummaryModel),
		State:             config.ExtractSummaryState,
	}
	if !material.Enabled {
		return nil, nil, material, nil
	}

	info, channel, err := a.resolveUtilityModelInfo(ctx, material.ConfiguredModelID)
	if err != nil {
		if ctxErr := runtimeContextError(ctx, err); ctxErr != nil {
			return nil, nil, runtimeExtractSummaryMaterial{}, ctxErr
		}
		return nil, nil, material, nil
	}
	if info == nil || channel == nil {
		return nil, nil, material, nil
	}

	infoCopy := *info
	channelCopy := *channel
	material.Available = true
	material.Model = runtimeModelInfoMaterial(&infoCopy)
	material.Channel = runtimeAIChannelMaterial(&channelCopy)
	return &infoCopy, &channelCopy, material, nil
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
	currentReq.RuntimeExtractSummaryEnabled = false
	currentReq.RuntimeExtractSummaryModel = ""
	currentReq.RuntimeExtractSummaryState = service.RuntimeConfigState{}
	currentReq.RuntimeExtractSummaryModelInfo = nil
	currentReq.RuntimeExtractSummaryChannel = nil
	current, err := a.captureAcceptedRuntimeSnapshot(ctx, &currentReq)
	if err != nil {
		if ctxErr := runtimeContextError(ctx, err); ctxErr != nil {
			return ctxErr
		}
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
	req.RuntimeExtractSummaryEnabled = currentReq.RuntimeExtractSummaryEnabled
	req.RuntimeExtractSummaryModel = currentReq.RuntimeExtractSummaryModel
	req.RuntimeExtractSummaryState = currentReq.RuntimeExtractSummaryState
	req.RuntimeExtractSummaryModelInfo = currentReq.RuntimeExtractSummaryModelInfo
	req.RuntimeExtractSummaryChannel = currentReq.RuntimeExtractSummaryChannel
	req.ContextWindow = expected.ContextWindow
	req.ModelMaxOutput = expected.ModelMaxOutput
	req.Vision = currentReq.Vision
	req.ToolUse = currentReq.ToolUse
	req.Reasoning = currentReq.Reasoning
	req.ThinkingFormat = expected.ThinkingFormat
	req.ThinkingEffort = expected.ThinkingEffort
	req.SearchImpl = currentReq.SearchImpl
	req.TemperaturePolicy = currentReq.TemperaturePolicy
	req.TemperatureValue = cloneFloat64Value(currentReq.TemperatureValue)
	req.Temperature = cloneFloat64Value(currentReq.Temperature)
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
	if cause := context.Cause(ctx); cause != nil {
		return nil, cause
	}
	if a == nil || req == nil {
		return nil, fmt.Errorf("runtime snapshot is unavailable")
	}
	modelInfo := modelbank.Get(req.ModelID)
	if modelInfo == nil || !modelInfo.Enabled || strings.TrimSpace(modelInfo.Provider) != strings.TrimSpace(req.Provider) {
		return nil, fmt.Errorf("accepted model is no longer available")
	}
	channel, err := a.channelService.ResolveAIChannelContext(ctx, req.Provider)
	if err != nil {
		if ctxErr := runtimeContextError(ctx, err); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("accepted channel is no longer available: %w", err)
	}
	templateText, err := loadPromptTemplateContext(ctx, a.configRepo)
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
	modelMaterial := runtimeModelInfoMaterial(modelInfo)
	acceptedTemperaturePolicy := model.NormalizeTemperaturePolicy(modelInfo.TemperaturePolicy)
	effectiveTemperature, err := model.ResolveTemperatureForRequest(acceptedTemperaturePolicy, modelInfo.TemperatureValue, req.Temperature)
	if err != nil {
		return nil, fmt.Errorf("invalid accepted model temperature profile: %w", err)
	}
	req.TemperaturePolicy = acceptedTemperaturePolicy
	req.TemperatureValue = cloneFloat64Value(modelInfo.TemperatureValue)
	req.Temperature = effectiveTemperature
	channelMaterial := runtimeAIChannelMaterial(channel)
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

	memory, memoryState, err := a.readRuntimeMemoryContext(ctx, req.SessionID, req.MemoryEnabled)
	if err != nil {
		return nil, err
	}
	toolRuntime, toolRuntimeState, err := a.resolveToolRuntimeConfigWithStateContext(ctx)
	if err != nil {
		return nil, err
	}
	searchRuntime, searchRuntimeState, err := a.resolveSearchRuntimeConfigWithStateContext(ctx)
	if err != nil {
		return nil, err
	}
	configMaterial, err := a.runtimeConfigMaterialContext(ctx)
	if err != nil {
		return nil, err
	}
	extractSummaryInfo, extractSummaryChannel, extractSummaryMaterial, err := a.resolveAcceptedExtractSummaryRuntime(ctx, configMaterial)
	if err != nil {
		return nil, err
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
	req.RuntimeExtractSummaryEnabled = configMaterial.ExtractSummaryEnabled
	req.RuntimeExtractSummaryModel = configMaterial.ExtractSummaryModel
	req.RuntimeExtractSummaryState = configMaterial.ExtractSummaryState
	req.RuntimeExtractSummaryModelInfo = extractSummaryInfo
	req.RuntimeExtractSummaryChannel = extractSummaryChannel
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
		Version:                acceptedRuntimeSnapshotVersion,
		CapturedAt:             capturedAt,
		ModelID:                req.ModelID,
		Provider:               req.Provider,
		ChannelKey:             channel.Key,
		ContextWindow:          modelMaterial.ContextWindow,
		ModelMaxOutput:         modelMaterial.MaxOutput,
		RequestedMaxOutput:     req.MaxTokens,
		ThinkingFormat:         modelMaterial.Thinking,
		ThinkingEffort:         thinkingEffort,
		ModelChecksum:          checksumValue("model", modelMaterial),
		ChannelChecksum:        checksumValue("channel", channelMaterial),
		RequestChecksum:        checksumValue("request", requestMaterial),
		PromptChecksum:         checksumValue("prompt", templateText),
		ConfigChecksum:         checksumValue("config", configMaterial),
		ExtractSummaryChecksum: checksumValue("extract-summary", extractSummaryMaterial),
		MemoryChecksum:         checksumValue("memory", memory),
		Skills:                 skillRefs,
		SkillsChecksum:         skillChecksum,
		EnabledTools:           enabledTools,
		ToolConfigChecksum:     checksumValue("tools", toolRuntime),
		ToolConfigState:        toolRuntimeState,
		SearchProviders:        append([]string(nil), searchRuntime.SearchProviders...),
		ExtractProviders:       append([]string(nil), searchRuntime.CrawlerProviders...),
		SearchConfigChecksum:   checksumValue("search", searchRuntime),
		SearchConfigState:      searchRuntimeState,
		ExtractSummaryState:    configMaterial.ExtractSummaryState,
		MemoryState:            memoryState,
	}
	snapshot.Checksum = acceptedRuntimeChecksum(snapshot)
	return snapshot, nil
}

func (a *EinoAgent) readRuntimeMemory(ctx context.Context, sessionID int64, enabled bool) (string, service.RuntimeConfigState) {
	value, state, _ := a.readRuntimeMemoryContext(ctx, sessionID, enabled)
	return value, state
}

func (a *EinoAgent) readRuntimeMemoryContext(ctx context.Context, sessionID int64, enabled bool) (string, service.RuntimeConfigState, error) {
	if cause := context.Cause(ctx); cause != nil {
		return "", service.RuntimeConfigState{}, cause
	}
	if !enabled {
		return "", service.RuntimeConfigState{State: service.RuntimeStateDisabled, Cause: "session_disabled", Version: "disabled"}, nil
	}
	if a == nil || a.memoryRepo == nil || sessionID <= 0 {
		return "", service.RuntimeConfigState{State: service.RuntimeStateUnavailable, Cause: "repository_unavailable", Version: "unavailable"}, nil
	}
	value, updatedAt, err := a.memoryRepo.GetWithUpdatedAt(ctx, sessionID)
	if err != nil {
		if ctxErr := runtimeContextError(ctx, err); ctxErr != nil {
			return "", service.RuntimeConfigState{}, ctxErr
		}
		return "", service.RuntimeConfigState{State: service.RuntimeStateUnavailable, Cause: "repository_unavailable", Version: "unavailable"}, nil
	}
	if updatedAt.IsZero() {
		return "", service.RuntimeConfigState{State: service.RuntimeStateUnconfigured, Cause: "empty_memory", Version: "empty"}, nil
	}
	return value, service.RuntimeConfigState{State: service.RuntimeStateReady, Version: updatedAt.UTC().Format(time.RFC3339Nano)}, nil
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
