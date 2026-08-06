package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/repository"
)

var (
	ErrModelInvalid  = errors.New("invalid model configuration")
	ErrModelNotFound = errors.New("model not found")
	ErrModelExists   = errors.New("model already exists")
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
	ID                   string                     `json:"id" binding:"required"`
	DisplayName          string                     `json:"display_name" binding:"required"`
	Provider             string                     `json:"provider" binding:"required"`
	Vision               bool                       `json:"vision"`
	ToolUse              bool                       `json:"tool_use"`
	Reasoning            bool                       `json:"reasoning"`
	ThinkingFormat       string                     `json:"thinking_format"`
	SearchImpl           string                     `json:"search_impl"`
	ContextWindow        int                        `json:"context_window"`
	MaxOutput            int                        `json:"max_output"`
	Enabled              *bool                      `json:"enabled"` // 指针：缺省时默认 true
	MinGroupLevel        int                        `json:"min_group_level"`
	SortOrder            int                        `json:"sort_order"`
	CatalogSource        string                     `json:"catalog_source"`
	CatalogCheckedAt     *time.Time                 `json:"catalog_checked_at"`
	LifecycleStatus      string                     `json:"lifecycle_status"`
	TemperaturePolicy    string                     `json:"temperature_policy"`
	TemperatureValue     *float64                   `json:"temperature_value"`
	OpenAIRequestProfile model.OpenAIRequestProfile `json:"openai_request_profile"`
}

// UpdateModelRequest 更新模型请求（指针字段，仅更新提供的项）
type UpdateModelRequest struct {
	DisplayName          *string                     `json:"display_name"`
	Provider             *string                     `json:"provider"`
	Vision               *bool                       `json:"vision"`
	ToolUse              *bool                       `json:"tool_use"`
	Reasoning            *bool                       `json:"reasoning"`
	ThinkingFormat       *string                     `json:"thinking_format"`
	SearchImpl           *string                     `json:"search_impl"`
	ContextWindow        *int                        `json:"context_window"`
	MaxOutput            *int                        `json:"max_output"`
	Enabled              *bool                       `json:"enabled"`
	MinGroupLevel        *int                        `json:"min_group_level"`
	SortOrder            *int                        `json:"sort_order"`
	CatalogSource        *string                     `json:"catalog_source"`
	CatalogCheckedAt     *time.Time                  `json:"catalog_checked_at"`
	LifecycleStatus      *string                     `json:"lifecycle_status"`
	TemperaturePolicy    *string                     `json:"temperature_policy"`
	TemperatureValue     *float64                    `json:"temperature_value"`
	OpenAIRequestProfile *model.OpenAIRequestProfile `json:"openai_request_profile"`
}

// validModelFields 模型字段校验值
var validSearchImpls = map[string]bool{
	"": true, "internal": true, "params": true, "tool": true,
}

// validateModelInput 校验模型字段（纯函数，不依赖 DB）
func validateModelInput(m *model.Model) error {
	if m.ID == "" {
		return fmt.Errorf("%w: id is required", ErrModelInvalid)
	}
	if m.DisplayName == "" {
		return fmt.Errorf("%w: display_name is required", ErrModelInvalid)
	}
	if m.Provider == "" {
		return fmt.Errorf("%w: provider is required", ErrModelInvalid)
	}
	if !validSearchImpls[m.SearchImpl] {
		return fmt.Errorf("%w: search_impl must be one of '', internal, params, tool", ErrModelInvalid)
	}
	if !modelbank.IsValidThinkingFormat(m.ThinkingFormat) {
		return fmt.Errorf("%w: invalid thinking_format", ErrModelInvalid)
	}
	if err := model.ValidateTemperatureProfile(m.TemperaturePolicy, m.TemperatureValue); err != nil {
		return fmt.Errorf("%w: %v", ErrModelInvalid, err)
	}
	if err := model.ValidateOpenAIRequestProfile(m.OpenAIRequestProfile); err != nil {
		return fmt.Errorf("%w: %v", ErrModelInvalid, err)
	}
	if m.ContextWindow < 0 {
		return fmt.Errorf("%w: context_window must be >= 0", ErrModelInvalid)
	}
	if m.MaxOutput < 0 {
		return fmt.Errorf("%w: max_output must be >= 0", ErrModelInvalid)
	}
	if m.MinGroupLevel < 0 {
		return fmt.Errorf("%w: min_group_level must be >= 0", ErrModelInvalid)
	}
	if !model.IsValidCatalogSource(m.CatalogSource) {
		return fmt.Errorf("%w: invalid catalog_source", ErrModelInvalid)
	}
	if !model.IsValidModelLifecycleStatus(m.LifecycleStatus) {
		return fmt.Errorf("%w: invalid lifecycle_status", ErrModelInvalid)
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
		return nil, ErrModelNotFound
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
		return nil, ErrModelExists
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	m := &model.Model{
		ID:                   req.ID,
		DisplayName:          req.DisplayName,
		Provider:             req.Provider,
		Vision:               req.Vision,
		ToolUse:              req.ToolUse,
		Reasoning:            req.Reasoning,
		ThinkingFormat:       modelbank.NormalizeThinkingFormat(req.ThinkingFormat),
		SearchImpl:           req.SearchImpl,
		ContextWindow:        req.ContextWindow,
		MaxOutput:            req.MaxOutput,
		Enabled:              enabled,
		MinGroupLevel:        req.MinGroupLevel,
		SortOrder:            req.SortOrder,
		CatalogSource:        model.NormalizeCatalogSource(req.CatalogSource),
		CatalogCheckedAt:     req.CatalogCheckedAt,
		LifecycleStatus:      model.NormalizeModelLifecycleStatus(req.LifecycleStatus),
		TemperaturePolicy:    model.NormalizeTemperaturePolicy(req.TemperaturePolicy),
		TemperatureValue:     req.TemperatureValue,
		OpenAIRequestProfile: model.CloneOpenAIRequestProfile(req.OpenAIRequestProfile),
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
func (s *ModelService) Update(ctx context.Context, id string, req *UpdateModelRequest) (*model.Model, error) {
	patch := modelPatchFromRequest(req)
	updated, err := s.modelRepo.UpdateFields(ctx, id, patch, validateModelInput)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, ErrModelNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := s.reloadRegistry(); err != nil {
		return nil, err
	}
	return s.applyThinkingRuntimeMetadata(updated), nil
}

func modelPatchFromRequest(req *UpdateModelRequest) repository.ModelPatch {
	return repository.ModelPatch{
		DisplayName: req.DisplayName, Provider: req.Provider, Vision: req.Vision,
		ToolUse: req.ToolUse, Reasoning: req.Reasoning, ThinkingFormat: req.ThinkingFormat,
		SearchImpl: req.SearchImpl, ContextWindow: req.ContextWindow, MaxOutput: req.MaxOutput,
		Enabled: req.Enabled, MinGroupLevel: req.MinGroupLevel, SortOrder: req.SortOrder,
		CatalogSource: req.CatalogSource, CatalogCheckedAt: req.CatalogCheckedAt,
		LifecycleStatus: req.LifecycleStatus, TemperaturePolicy: req.TemperaturePolicy,
		TemperatureValue: req.TemperatureValue, OpenAIRequestProfile: req.OpenAIRequestProfile,
	}
}

// Delete transitions a model to disabled so existing sessions fail with a clear,
// recoverable model state instead of losing their configured model reference.
func (s *ModelService) Delete(ctx context.Context, id string) error {
	disabled := false
	_, err := s.modelRepo.UpdateFields(ctx, id, repository.ModelPatch{Enabled: &disabled}, nil)
	if errors.Is(err, repository.ErrNotFound) {
		return ErrModelNotFound
	}
	if err != nil {
		return err
	}
	return s.reloadRegistry()
}

func (s *ModelService) ValidateDefaultModel(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		models, err := s.modelRepo.List(true)
		if err != nil {
			return fmt.Errorf("list runnable public models: %w", err)
		}
		for _, candidate := range models {
			if candidate == nil || candidate.MinGroupLevel > 0 || s.channelService == nil {
				continue
			}
			if _, err := s.channelService.ResolveAIChannel(candidate.Provider); err == nil {
				return fmt.Errorf("%w: default_model_id is required while a runnable public model exists", ErrModelInvalid)
			} else if !errors.Is(err, ErrChannelInvalid) && !errors.Is(err, ErrChannelNotFound) && !errors.Is(err, ErrChannelUnavailable) {
				return fmt.Errorf("validate public model channel: %w", err)
			}
		}
		return nil
	}
	m, err := s.modelRepo.Get(id)
	if err != nil {
		return fmt.Errorf("load default model: %w", err)
	}
	if m == nil {
		return fmt.Errorf("%w: default_model_id does not exist", ErrModelInvalid)
	}
	if !m.Enabled || m.MinGroupLevel > 0 {
		return fmt.Errorf("%w: default_model_id must be an enabled public model", ErrModelInvalid)
	}
	if s.channelService == nil {
		return fmt.Errorf("%w: default model channel is unavailable", ErrModelInvalid)
	}
	if _, err := s.channelService.ResolveAIChannel(m.Provider); err != nil {
		if errors.Is(err, ErrChannelInvalid) || errors.Is(err, ErrChannelNotFound) || errors.Is(err, ErrChannelUnavailable) {
			return fmt.Errorf("%w: default model channel is unavailable", ErrModelInvalid)
		}
		return fmt.Errorf("validate default model channel: %w", err)
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
