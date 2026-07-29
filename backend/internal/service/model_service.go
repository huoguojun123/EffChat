package service

import (
	"fmt"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/repository"
)

type ModelService struct {
	modelRepo      *repository.ModelRepository
	channelService *ChannelService
}

func NewModelService(modelRepo *repository.ModelRepository, channelServices ...*ChannelService) *ModelService {
	var channelService *ChannelService
	if len(channelServices) > 0 {
		channelService = channelServices[0]
	}
	return &ModelService{modelRepo: modelRepo, channelService: channelService}
}

// CreateModelRequest 新建模型请求
type CreateModelRequest struct {
	ID             string `json:"id" binding:"required"`
	DisplayName    string `json:"display_name" binding:"required"`
	Provider       string `json:"provider" binding:"required"`
	Vision         bool   `json:"vision"`
	ToolUse        bool   `json:"tool_use"`
	Reasoning      bool   `json:"reasoning"`
	ThinkingFormat string `json:"thinking_format"`
	SearchImpl     string `json:"search_impl"`
	ContextWindow  int    `json:"context_window"`
	MaxOutput      int    `json:"max_output"`
	Enabled        *bool  `json:"enabled"` // 指针：缺省时默认 true
	MinGroupLevel  int    `json:"min_group_level"`
	SortOrder      int    `json:"sort_order"`
}

// UpdateModelRequest 更新模型请求（指针字段，仅更新提供的项）
type UpdateModelRequest struct {
	DisplayName    *string `json:"display_name"`
	Provider       *string `json:"provider"`
	Vision         *bool   `json:"vision"`
	ToolUse        *bool   `json:"tool_use"`
	Reasoning      *bool   `json:"reasoning"`
	ThinkingFormat *string `json:"thinking_format"`
	SearchImpl     *string `json:"search_impl"`
	ContextWindow  *int    `json:"context_window"`
	MaxOutput      *int    `json:"max_output"`
	Enabled        *bool   `json:"enabled"`
	MinGroupLevel  *int    `json:"min_group_level"`
	SortOrder      *int    `json:"sort_order"`
}

// validModelFields 模型字段校验值
var validSearchImpls = map[string]bool{
	"": true, "internal": true, "params": true, "tool": true,
}

// validateModelInput 校验模型字段（纯函数，不依赖 DB）
func validateModelInput(m *model.Model) error {
	if m.ID == "" {
		return fmt.Errorf("id is required")
	}
	if m.DisplayName == "" {
		return fmt.Errorf("display_name is required")
	}
	if m.Provider == "" {
		return fmt.Errorf("provider is required")
	}
	if !validSearchImpls[m.SearchImpl] {
		return fmt.Errorf("invalid search_impl: must be one of '', internal, params, tool")
	}
	if !modelbank.IsValidThinkingFormat(m.ThinkingFormat) {
		return fmt.Errorf("invalid thinking_format")
	}
	if m.ContextWindow < 0 {
		return fmt.Errorf("context_window must be >= 0")
	}
	if m.MaxOutput < 0 {
		return fmt.Errorf("max_output must be >= 0")
	}
	if m.MinGroupLevel < 0 {
		return fmt.Errorf("min_group_level must be >= 0")
	}
	return nil
}

// reloadRegistry 重新加载内存模型注册表，使写操作即时生效（无需重启）。
// reload 失败不回滚 DB（DB 已是事实来源），仅返回错误供调用方记录。
func (s *ModelService) reloadRegistry() error {
	models, err := s.modelRepo.List(false)
	if err != nil {
		return fmt.Errorf("failed to reload model registry: %w", err)
	}
	modelbank.LoadModels(models)
	return nil
}

// List 返回模型列表
func (s *ModelService) List(onlyEnabled bool) ([]*model.Model, error) {
	models, err := s.modelRepo.List(onlyEnabled)
	if err != nil {
		return nil, err
	}
	return s.attachThinkingRuntimeMetadata(models), nil
}

// ListVisible 返回对指定组等级可见的启用模型（普通用户用）。
func (s *ModelService) ListVisible(maxLevel int) ([]*model.Model, error) {
	models, err := s.modelRepo.ListVisible(maxLevel)
	if err != nil {
		return nil, err
	}
	return s.attachThinkingRuntimeMetadata(models), nil
}

// Get 获取单个模型，不存在返回错误
func (s *ModelService) Get(id string) (*model.Model, error) {
	m, err := s.modelRepo.Get(id)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, fmt.Errorf("model not found: %s", id)
	}
	return s.applyThinkingRuntimeMetadata(m), nil
}

// Create 新建模型（id 已存在则拒绝）
func (s *ModelService) Create(req *CreateModelRequest) (*model.Model, error) {
	existing, err := s.modelRepo.Get(req.ID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, fmt.Errorf("model already exists: %s", req.ID)
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	m := &model.Model{
		ID:             req.ID,
		DisplayName:    req.DisplayName,
		Provider:       req.Provider,
		Vision:         req.Vision,
		ToolUse:        req.ToolUse,
		Reasoning:      req.Reasoning,
		ThinkingFormat: modelbank.NormalizeThinkingFormat(req.ThinkingFormat),
		SearchImpl:     req.SearchImpl,
		ContextWindow:  req.ContextWindow,
		MaxOutput:      req.MaxOutput,
		Enabled:        enabled,
		MinGroupLevel:  req.MinGroupLevel,
		SortOrder:      req.SortOrder,
	}

	if err := validateModelInput(m); err != nil {
		return nil, err
	}
	if err := s.modelRepo.Upsert(m); err != nil {
		return nil, err
	}
	if err := s.reloadRegistry(); err != nil {
		return nil, err
	}
	return s.applyThinkingRuntimeMetadata(m), nil
}

// Update 部分更新模型（模型须已存在）
func (s *ModelService) Update(id string, req *UpdateModelRequest) (*model.Model, error) {
	m, err := s.modelRepo.Get(id)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, fmt.Errorf("model not found: %s", id)
	}

	if req.DisplayName != nil {
		m.DisplayName = *req.DisplayName
	}
	if req.Provider != nil {
		m.Provider = *req.Provider
	}
	if req.Vision != nil {
		m.Vision = *req.Vision
	}
	if req.ToolUse != nil {
		m.ToolUse = *req.ToolUse
	}
	if req.Reasoning != nil {
		m.Reasoning = *req.Reasoning
	}
	if req.ThinkingFormat != nil {
		m.ThinkingFormat = modelbank.NormalizeThinkingFormat(*req.ThinkingFormat)
	}
	if req.SearchImpl != nil {
		m.SearchImpl = *req.SearchImpl
	}
	if req.ContextWindow != nil {
		m.ContextWindow = *req.ContextWindow
	}
	if req.MaxOutput != nil {
		m.MaxOutput = *req.MaxOutput
	}
	if req.Enabled != nil {
		m.Enabled = *req.Enabled
	}
	if req.MinGroupLevel != nil {
		m.MinGroupLevel = *req.MinGroupLevel
	}
	if req.SortOrder != nil {
		m.SortOrder = *req.SortOrder
	}

	if err := validateModelInput(m); err != nil {
		return nil, err
	}
	if err := s.modelRepo.Upsert(m); err != nil {
		return nil, err
	}
	if err := s.reloadRegistry(); err != nil {
		return nil, err
	}
	return s.applyThinkingRuntimeMetadata(m), nil
}

// Delete transitions a model to disabled so existing sessions fail with a clear,
// recoverable model state instead of losing their configured model reference.
func (s *ModelService) Delete(id string) error {
	m, err := s.modelRepo.Get(id)
	if err != nil {
		return err
	}
	if m == nil {
		return fmt.Errorf("model not found: %s", id)
	}
	m.Enabled = false
	if err := s.modelRepo.Upsert(m); err != nil {
		return err
	}
	return s.reloadRegistry()
}

func (s *ModelService) ValidateDefaultModel(id string) error {
	m, err := s.modelRepo.Get(id)
	if err != nil || m == nil {
		return fmt.Errorf("default_model_id does not exist")
	}
	if !m.Enabled || m.MinGroupLevel > 0 {
		return fmt.Errorf("default_model_id must be an enabled public model")
	}
	if s.channelService == nil {
		return fmt.Errorf("default model channel is unavailable")
	}
	if _, err := s.channelService.ResolveAIChannel(m.Provider); err != nil {
		return fmt.Errorf("default model channel is unavailable")
	}
	return nil
}

func (s *ModelService) attachThinkingRuntimeMetadata(models []*model.Model) []*model.Model {
	channels := s.channelMetadata()
	for _, m := range models {
		s.applyChannelMetadata(m, channels[m.Provider])
	}
	return models
}

func (s *ModelService) applyThinkingRuntimeMetadata(m *model.Model) *model.Model {
	if m == nil {
		return nil
	}
	s.applyChannelMetadata(m, s.channelMetadata()[m.Provider])
	return m
}

type modelChannelMetadata struct {
	DisplayName string
	Adapter     string
	Enabled     bool
	Configured  bool
}

func (s *ModelService) applyChannelMetadata(m *model.Model, meta modelChannelMetadata) {
	if m == nil {
		return
	}
	m.ChannelDisplayName = meta.DisplayName
	m.ChannelAdapter = meta.Adapter
	m.ChannelEnabled = meta.Enabled
	m.ChannelConfigured = meta.Configured
	modelbank.ApplyThinkingRuntimeMetadataWithAdapter(m, meta.Adapter)
}

func (s *ModelService) channelMetadata() map[string]modelChannelMetadata {
	metadata := map[string]modelChannelMetadata{}
	if s == nil || s.channelService == nil {
		return metadata
	}
	channels, err := s.channelService.ListAIChannels(true)
	if err != nil {
		return metadata
	}
	for _, channel := range channels {
		if channel != nil {
			metadata[channel.Key] = modelChannelMetadata{
				DisplayName: channel.DisplayName,
				Adapter:     channel.Adapter,
				Enabled:     channel.Enabled,
				Configured:  true,
			}
		}
	}
	return metadata
}
