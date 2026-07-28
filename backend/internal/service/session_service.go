package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/huoguojun123/effchat/internal/model"
	"github.com/huoguojun123/effchat/internal/modelbank"
	"github.com/huoguojun123/effchat/internal/repository"
)

type SessionService struct {
	sessionRepo *repository.SessionRepository
	messageRepo *repository.MessageRepository
	configRepo  *repository.ConfigRepository
	folderRepo  *repository.SessionFolderRepository
	modelRepo   *repository.ModelRepository
	channels    *ChannelService
	userRepo    *repository.UserRepository
	runHub      *RunHub
}

func NewSessionService(sessionRepo *repository.SessionRepository, messageRepo *repository.MessageRepository, configRepo *repository.ConfigRepository, folderRepo ...*repository.SessionFolderRepository) *SessionService {
	var folders *repository.SessionFolderRepository
	if len(folderRepo) > 0 {
		folders = folderRepo[0]
	}
	return &SessionService{
		sessionRepo: sessionRepo,
		messageRepo: messageRepo,
		configRepo:  configRepo,
		folderRepo:  folders,
	}
}

func (s *SessionService) SetRuntimeModelDependencies(modelRepo *repository.ModelRepository, channels *ChannelService, userRepos ...*repository.UserRepository) {
	s.modelRepo = modelRepo
	s.channels = channels
	if len(userRepos) > 0 {
		s.userRepo = userRepos[0]
	}
}

func (s *SessionService) SetRunHub(runHub *RunHub) {
	s.runHub = runHub
}

type RuntimeModelError struct {
	Code     string `json:"code"`
	Message  string `json:"error"`
	Provider string `json:"provider,omitempty"`
	ModelID  string `json:"model_id,omitempty"`
}

func (e *RuntimeModelError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

type NullableInt64 struct {
	Set   bool
	Valid bool
	Value int64
}

func (n *NullableInt64) UnmarshalJSON(data []byte) error {
	n.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		n.Valid = false
		n.Value = 0
		return nil
	}
	var value int64
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	n.Valid = true
	n.Value = value
	return nil
}

type CreateSessionRequest struct {
	Title         string   `json:"title"`
	ModelID       string   `json:"model_id"`
	Provider      string   `json:"provider"`
	FolderID      *int64   `json:"folder_id"`
	SystemPrompt  *string  `json:"system_prompt"`
	Temperature   *float64 `json:"temperature"`
	MaxTokens     *int     `json:"max_tokens"`
	MessageFormat string   `json:"message_format"` // v1 or v2
	SearchMode    string   `json:"search_mode"`    // off, auto, on
	MemoryEnabled *bool    `json:"memory_enabled"`
}

type UpdateSessionRequest struct {
	ModelID       *string       `json:"model_id"`
	Provider      *string       `json:"provider"`
	Title         *string       `json:"title"`
	FolderID      NullableInt64 `json:"folder_id"`
	SystemPrompt  *string       `json:"system_prompt"`
	Temperature   *float64      `json:"temperature"`
	MaxTokens     *int          `json:"max_tokens"`
	SearchMode    *string       `json:"search_mode"`
	MemoryEnabled *bool         `json:"memory_enabled"`
	Pinned        *bool         `json:"pinned"`
}

// normalizeSearchMode 校验会话搜索模式，非法或空值回退到 auto（自适应）。
func normalizeSearchMode(mode string) string {
	switch modelbank.SearchMode(strings.TrimSpace(mode)) {
	case modelbank.SearchModeOff:
		return string(modelbank.SearchModeOff)
	case modelbank.SearchModeOn:
		return string(modelbank.SearchModeOn)
	default:
		return string(modelbank.SearchModeAuto)
	}
}

type SessionWithStats struct {
	*model.Session
	MessageCount int `json:"message_count"`
}

type SessionListFilter struct {
	FolderID *int64
	Unfiled  bool
}

type SessionListResult struct {
	Sessions   []*SessionWithStats `json:"sessions"`
	HasMore    bool                `json:"has_more"`
	NextOffset int                 `json:"next_offset"`
}

const (
	maxSessionSystemPromptBytes = 16 << 10
	maxSessionTemperature       = 2.0
)

func validateSessionSystemPrompt(prompt *string) error {
	if prompt != nil && len(*prompt) > maxSessionSystemPromptBytes {
		return fmt.Errorf("system_prompt must not exceed %d bytes", maxSessionSystemPromptBytes)
	}
	return nil
}

// Create 创建会话
func (s *SessionService) Create(userID int64, req *CreateSessionRequest) (*model.Session, error) {
	if err := validateSessionSystemPrompt(req.SystemPrompt); err != nil {
		return nil, err
	}
	// 默认标题
	title := req.Title
	if title == "" {
		title = "新对话"
	}

	// 默认消息格式
	messageFormat := req.MessageFormat
	if messageFormat == "" {
		messageFormat = "v1"
	}

	// 验证消息格式
	if messageFormat != "v1" && messageFormat != "v2" {
		return nil, fmt.Errorf("invalid message_format: must be v1 or v2")
	}

	modelID := strings.TrimSpace(req.ModelID)
	if modelID == "" {
		modelID = s.defaultModelID()
	}
	if modelID == "" {
		return nil, fmt.Errorf("model_id is required")
	}

	provider := req.Provider
	if provider == "" {
		provider = modelbank.GetOrDefault(modelID, "").Provider
	}
	if provider == "" {
		return nil, fmt.Errorf("provider is required")
	}
	if err := s.ValidateModelForUser(userID, modelID, provider); err != nil {
		return nil, err
	}
	m, err := s.runnableModel(&model.Session{ModelID: modelID, Provider: provider})
	if err != nil {
		return nil, err
	}
	if err := validateSessionGenerationParameters(m, req.Temperature, req.MaxTokens); err != nil {
		return nil, err
	}

	folderID, err := s.validateFolderID(userID, req.FolderID)
	if err != nil {
		return nil, err
	}

	// 默认元数据
	metadata := map[string]interface{}{
		"skills_enabled": []string{},
		"file_count":     0,
		"compressed":     false,
	}
	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal session metadata: %w", err)
	}

	memoryEnabled := true
	if req.MemoryEnabled != nil {
		memoryEnabled = *req.MemoryEnabled
	}

	session := &model.Session{
		UserID:         userID,
		Title:          title,
		TitleGenerated: false,
		ModelID:        modelID,
		Provider:       provider,
		FolderID:       folderID,
		SystemPrompt:   req.SystemPrompt,
		Temperature:    req.Temperature,
		MaxTokens:      req.MaxTokens,
		MessageFormat:  messageFormat,
		SearchMode:     normalizeSearchMode(req.SearchMode),
		MemoryEnabled:  memoryEnabled,
		Metadata:       metadataBytes,
	}

	if err := s.sessionRepo.Create(session); err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	return session, nil
}

func (s *SessionService) defaultModelID() string {
	if s.configRepo != nil {
		modelID := strings.TrimSpace(s.configRepo.GetString("default_model_id", ""))
		if modelID != "" {
			if info := modelbank.Get(modelID); info != nil {
				return modelID
			}
		}
	}
	return ""
}

// GetByID 获取会话（含权限检查）
func (s *SessionService) GetByID(sessionID, userID int64) (*model.Session, error) {
	return s.GetByIDContext(context.Background(), sessionID, userID)
}

func (s *SessionService) GetByIDContext(ctx context.Context, sessionID, userID int64) (*model.Session, error) {
	session, err := s.sessionRepo.GetByIDContext(ctx, sessionID, userID)
	if err != nil {
		return nil, fmt.Errorf("session not found or access denied")
	}
	return session, nil
}

func (s *SessionService) ValidateRunnableModel(session *model.Session) error {
	m, err := s.runnableModel(session)
	if err != nil {
		return err
	}
	return validateSessionGenerationParameters(m, session.Temperature, session.MaxTokens)
}

// ValidateRunnableModelForUser verifies that the session model is still usable
// and visible to the current account before a run can reserve quota or write messages.
func (s *SessionService) ValidateRunnableModelForUser(session *model.Session, userID int64) error {
	m, err := s.runnableModel(session)
	if err != nil {
		return err
	}
	if err := s.validateModelAccess(userID, m, session.Provider); err != nil {
		return err
	}
	return validateSessionGenerationParameters(m, session.Temperature, session.MaxTokens)
}

// ValidateModelForUser validates a candidate model before it is persisted to a session.
func (s *SessionService) ValidateModelForUser(userID int64, modelID, provider string) error {
	return s.ValidateRunnableModelForUser(&model.Session{ModelID: modelID, Provider: provider}, userID)
}

func validateSessionGenerationParameters(m *model.Model, temperature *float64, maxTokens *int) error {
	if temperature != nil && (math.IsNaN(*temperature) || math.IsInf(*temperature, 0) || *temperature < 0 || *temperature > maxSessionTemperature) {
		return fmt.Errorf("temperature must be between 0 and %.1f", maxSessionTemperature)
	}
	if maxTokens == nil {
		return nil
	}
	if *maxTokens <= 0 {
		return fmt.Errorf("max_tokens must be positive")
	}
	if m != nil && m.MaxOutput > 0 && *maxTokens > m.MaxOutput {
		return fmt.Errorf("max_tokens must not exceed this model's max_output (%d)", m.MaxOutput)
	}
	return nil
}

func (s *SessionService) runnableModel(session *model.Session) (*model.Model, error) {
	if session == nil {
		return nil, &RuntimeModelError{Code: "session_model_missing", Message: "当前会话模型不可用，请切换模型"}
	}
	provider := strings.TrimSpace(session.Provider)
	modelID := strings.TrimSpace(session.ModelID)
	if provider == "" || modelID == "" {
		return nil, &RuntimeModelError{Code: "session_model_missing", Message: "当前会话没有可用模型，请切换模型", Provider: provider, ModelID: modelID}
	}
	if s.modelRepo == nil {
		return nil, &RuntimeModelError{Code: "model_runtime_unavailable", Message: "模型配置暂不可用，请稍后重试", Provider: provider, ModelID: modelID}
	}
	m, err := s.modelRepo.Get(modelID)
	if err != nil || m == nil {
		return nil, &RuntimeModelError{Code: "session_model_missing", Message: fmt.Sprintf("模型 %q 不存在，请切换模型", modelID), Provider: provider, ModelID: modelID}
	}
	if !m.Enabled {
		return nil, &RuntimeModelError{Code: "session_model_disabled", Message: fmt.Sprintf("模型 %q 已停用，请切换模型", modelID), Provider: provider, ModelID: modelID}
	}
	if strings.TrimSpace(m.Provider) != provider {
		return nil, &RuntimeModelError{
			Code:     "session_model_channel_mismatch",
			Message:  fmt.Sprintf("模型 %q 属于渠道 %q，当前会话仍绑定渠道 %q，请切换模型", modelID, m.Provider, provider),
			Provider: provider,
			ModelID:  modelID,
		}
	}
	if s.channels != nil {
		channel, err := s.channels.GetAIChannel(provider)
		if err != nil || channel == nil {
			return nil, &RuntimeModelError{Code: "channel_not_configured", Message: fmt.Sprintf("渠道 %q 未配置，请切换模型", provider), Provider: provider, ModelID: modelID}
		}
		if !channel.Enabled {
			return nil, &RuntimeModelError{Code: "channel_disabled", Message: fmt.Sprintf("渠道 %q 已停用，请切换模型", provider), Provider: provider, ModelID: modelID}
		}
		if strings.TrimSpace(channel.APIKey) == "" {
			return nil, &RuntimeModelError{Code: "channel_api_key_missing", Message: fmt.Sprintf("渠道 %q 没有配置 API key，请切换模型或联系管理员", provider), Provider: provider, ModelID: modelID}
		}
	}
	return m, nil
}

func (s *SessionService) validateModelAccess(userID int64, m *model.Model, provider string) error {
	if s.userRepo == nil {
		return &RuntimeModelError{Code: "model_runtime_unavailable", Message: "模型权限配置暂不可用，请稍后重试", Provider: provider, ModelID: m.ID}
	}
	user, err := s.userRepo.GetByID(userID)
	if err != nil {
		return &RuntimeModelError{Code: "model_access_denied", Message: "当前账号无权使用该模型", Provider: provider, ModelID: m.ID}
	}
	if user.Role == "admin" {
		return nil
	}
	level, err := s.userRepo.GetGroupLevel(userID)
	if err != nil || level < m.MinGroupLevel {
		return &RuntimeModelError{Code: "model_access_denied", Message: "当前账号无权使用该模型", Provider: provider, ModelID: m.ID}
	}
	return nil
}

// List 获取用户的会话列表
func (s *SessionService) List(userID int64, limit, offset int, filter SessionListFilter) (*SessionListResult, error) {
	if limit <= 0 {
		limit = 100
	}
	sessions, err := s.sessionRepo.ListByUser(userID, limit+1, offset, filter.FolderID, filter.Unfiled)
	if err != nil {
		return nil, fmt.Errorf("failed to list sessions: %w", err)
	}

	hasMore := len(sessions) > limit
	if hasMore {
		sessions = sessions[:limit]
	}

	sessionIDs := make([]int64, 0, len(sessions))
	for _, session := range sessions {
		sessionIDs = append(sessionIDs, session.ID)
	}
	counts, err := s.messageRepo.CountBySessions(sessionIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to count session messages: %w", err)
	}

	// 添加消息统计。CountBySessions 缺失的会话表示没有消息，按 0 处理。
	result := make([]*SessionWithStats, len(sessions))
	for i, session := range sessions {
		result[i] = &SessionWithStats{
			Session:      session,
			MessageCount: counts[session.ID],
		}
	}

	nextOffset := offset
	if hasMore {
		nextOffset = offset + len(result)
	}

	return &SessionListResult{
		Sessions:   result,
		HasMore:    hasMore,
		NextOffset: nextOffset,
	}, nil
}

// Update 更新会话
func (s *SessionService) Update(sessionID, userID int64, req *UpdateSessionRequest) error {
	if err := validateSessionSystemPrompt(req.SystemPrompt); err != nil {
		return err
	}
	// 先检查权限
	session, err := s.sessionRepo.GetByID(sessionID, userID)
	if err != nil {
		return fmt.Errorf("session not found or access denied")
	}

	if req.ModelID != nil || req.Provider != nil {
		nextModelID := session.ModelID
		nextProvider := session.Provider
		if req.ModelID != nil {
			nextModelID = *req.ModelID
			if req.Provider == nil {
				nextProvider = modelbank.GetOrDefault(*req.ModelID, session.Provider).Provider
			}
		}
		if req.Provider != nil {
			nextProvider = *req.Provider
		}
		if err := s.ValidateModelForUser(userID, nextModelID, nextProvider); err != nil {
			return err
		}
		session.ModelID = nextModelID
		session.Provider = nextProvider
	}
	if req.Title != nil {
		session.Title = *req.Title
	}
	if req.FolderID.Set {
		if req.FolderID.Valid {
			folderID, err := s.validateFolderID(userID, &req.FolderID.Value)
			if err != nil {
				return err
			}
			session.FolderID = folderID
		} else {
			session.FolderID = nil
		}
	}
	if req.SystemPrompt != nil {
		session.SystemPrompt = req.SystemPrompt
	}
	if req.Temperature != nil {
		session.Temperature = req.Temperature
	}
	if req.MaxTokens != nil {
		session.MaxTokens = req.MaxTokens
	}
	if req.SearchMode != nil {
		session.SearchMode = normalizeSearchMode(*req.SearchMode)
	}
	if req.MemoryEnabled != nil {
		session.MemoryEnabled = *req.MemoryEnabled
	}
	if req.ModelID != nil || req.Provider != nil || req.Temperature != nil || req.MaxTokens != nil {
		m, err := s.runnableModel(session)
		if err != nil {
			return err
		}
		if err := validateSessionGenerationParameters(m, session.Temperature, session.MaxTokens); err != nil {
			return err
		}
	}

	patch := repository.SessionPatch{
		ModelID:       req.ModelID,
		Provider:      req.Provider,
		Title:         req.Title,
		FolderIDSet:   req.FolderID.Set,
		FolderID:      session.FolderID,
		SystemPrompt:  req.SystemPrompt,
		Temperature:   req.Temperature,
		MaxTokens:     req.MaxTokens,
		SearchMode:    req.SearchMode,
		MemoryEnabled: req.MemoryEnabled,
		Pinned:        req.Pinned,
	}
	if req.ModelID != nil && req.Provider == nil {
		patch.Provider = &session.Provider
	}
	if req.SearchMode != nil {
		patch.SearchMode = &session.SearchMode
	}
	if err := s.sessionRepo.UpdateFields(sessionID, userID, patch); err != nil {
		return fmt.Errorf("failed to update session: %w", err)
	}

	return nil
}

// Delete 删除会话
func (s *SessionService) Delete(sessionID, userID int64) error {
	if err := s.sessionRepo.Delete(sessionID, userID); err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}
	if s.runHub != nil {
		s.runHub.CancelSession(sessionID, userID)
	}
	return nil
}

// GetSessionCount 获取用户会话数量
func (s *SessionService) GetSessionCount(userID int64) (int, error) {
	return s.sessionRepo.CountByUser(userID)
}

func (s *SessionService) validateFolderID(userID int64, folderID *int64) (*int64, error) {
	if folderID == nil {
		return nil, nil
	}
	if *folderID <= 0 {
		return nil, fmt.Errorf("invalid folder_id")
	}
	if s.folderRepo == nil {
		return nil, fmt.Errorf("session folders are not configured")
	}
	if _, err := s.folderRepo.GetByID(*folderID, userID); err != nil {
		return nil, fmt.Errorf("session folder not found or access denied")
	}
	return folderID, nil
}
