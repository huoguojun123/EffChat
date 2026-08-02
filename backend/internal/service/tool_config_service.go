package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
)

const defaultToolTimeoutSeconds = 20

var ErrToolConfigInvalid = errors.New("invalid tool configuration")

type ToolConfigService struct {
	repo *repository.ToolConfigRepository
}

func NewToolConfigService(repo *repository.ToolConfigRepository) *ToolConfigService {
	return &ToolConfigService{repo: repo}
}

type ToolConfigInput struct {
	Key            string `json:"key" binding:"required"`
	DisplayName    string `json:"display_name" binding:"required"`
	Enabled        *bool  `json:"enabled"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	SortOrder      int    `json:"sort_order"`
}

type ToolRuntimeConfig struct {
	Enabled        bool
	TimeoutSeconds int
}

type ToolRuntimeConfigSet map[string]ToolRuntimeConfig

func DefaultToolConfigs() []*model.ToolConfig {
	return []*model.ToolConfig{
		{Key: "memory", DisplayName: "Session memory", Enabled: true, TimeoutSeconds: defaultToolTimeoutSeconds, SortOrder: 10},
		{Key: "file_list", DisplayName: "File list", Enabled: true, TimeoutSeconds: defaultToolTimeoutSeconds, SortOrder: 20},
		{Key: "file_search", DisplayName: "File search", Enabled: true, TimeoutSeconds: defaultToolTimeoutSeconds, SortOrder: 30},
		{Key: "file_read", DisplayName: "File read", Enabled: true, TimeoutSeconds: defaultToolTimeoutSeconds, SortOrder: 40},
		{Key: "skill_list", DisplayName: "Skill list", Enabled: true, TimeoutSeconds: defaultToolTimeoutSeconds, SortOrder: 50},
		{Key: "skill_search", DisplayName: "Skill search", Enabled: true, TimeoutSeconds: defaultToolTimeoutSeconds, SortOrder: 60},
		{Key: "skill_read", DisplayName: "Skill read", Enabled: true, TimeoutSeconds: defaultToolTimeoutSeconds, SortOrder: 70},
		{Key: "web_search", DisplayName: "Web search", Enabled: true, TimeoutSeconds: defaultToolTimeoutSeconds, SortOrder: 80},
		{Key: "web_extract", DisplayName: "Web extract", Enabled: true, TimeoutSeconds: 30, SortOrder: 90},
	}
}

func (s *ToolConfigService) List() ([]*model.ToolConfig, error) {
	return s.ListContext(context.Background())
}

func (s *ToolConfigService) ListContext(ctx context.Context) ([]*model.ToolConfig, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil || s.repo == nil {
		return DefaultToolConfigs(), nil
	}
	rows, err := s.repo.ListContext(ctx)
	if err != nil {
		return nil, err
	}
	return mergeToolConfigDefaults(rows), nil
}

func (s *ToolConfigService) Save(input *ToolConfigInput) (*model.ToolConfig, error) {
	item, err := toolConfigFromInput(input)
	if err != nil {
		return nil, err
	}
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("tool config repository is not available")
	}
	return s.repo.Upsert(item)
}

func (s *ToolConfigService) RuntimeConfigSet() ToolRuntimeConfigSet {
	runtime, _ := s.ResolveRuntimeConfig()
	return runtime
}

func (s *ToolConfigService) ResolveRuntimeConfig() (ToolRuntimeConfigSet, RuntimeConfigState) {
	runtime, state, _ := s.ResolveRuntimeConfigContext(context.Background())
	return runtime, state
}

func (s *ToolConfigService) ResolveRuntimeConfigContext(ctx context.Context) (ToolRuntimeConfigSet, RuntimeConfigState, error) {
	if err := ctx.Err(); err != nil {
		return nil, RuntimeConfigState{}, err
	}
	if s == nil || s.repo == nil {
		items := DefaultToolConfigs()
		return toolRuntimeConfigSet(items), runtimeConfigState(RuntimeStateDefault, "builtin_default", toolConfigVersion(items)), nil
	}
	rows, err := s.repo.ListContext(ctx)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, RuntimeConfigState{}, ctxErr
		}
		runtime := disabledToolRuntimeConfigSet()
		state := runtimeConfigState(RuntimeStateUnavailable, "repository_unavailable", runtimeConfigVersion("tools:unavailable", nil))
		return runtime, state, nil
	}
	items := mergeToolConfigDefaults(rows)
	state := RuntimeStateReady
	cause := ""
	if len(rows) == 0 {
		state = RuntimeStateDefault
		cause = "builtin_default"
	}
	return toolRuntimeConfigSet(items), runtimeConfigState(state, cause, toolConfigVersion(items)), nil
}

func toolRuntimeConfigSet(items []*model.ToolConfig) ToolRuntimeConfigSet {
	out := make(ToolRuntimeConfigSet, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		out[normalizeToolKey(item.Key)] = ToolRuntimeConfig{
			Enabled:        item.Enabled,
			TimeoutSeconds: normalizeToolTimeout(item.TimeoutSeconds),
		}
	}
	return out
}

func disabledToolRuntimeConfigSet() ToolRuntimeConfigSet {
	out := make(ToolRuntimeConfigSet, len(DefaultToolConfigs()))
	for _, item := range DefaultToolConfigs() {
		out[item.Key] = ToolRuntimeConfig{Enabled: false, TimeoutSeconds: normalizeToolTimeout(item.TimeoutSeconds)}
	}
	return out
}

func (set ToolRuntimeConfigSet) IsEnabled(key string) bool {
	cfg, ok := set[normalizeToolKey(key)]
	if !ok {
		return true
	}
	return cfg.Enabled
}

func (set ToolRuntimeConfigSet) Timeout(key string) time.Duration {
	cfg, ok := set[normalizeToolKey(key)]
	if !ok {
		return defaultToolTimeoutSeconds * time.Second
	}
	return time.Duration(normalizeToolTimeout(cfg.TimeoutSeconds)) * time.Second
}

func toolConfigFromInput(input *ToolConfigInput) (*model.ToolConfig, error) {
	if input == nil {
		return nil, fmt.Errorf("%w: tool config input is required", ErrToolConfigInvalid)
	}
	key := normalizeToolKey(input.Key)
	if key == "" {
		return nil, fmt.Errorf("%w: key is required", ErrToolConfigInvalid)
	}
	if !knownToolKey(key) {
		return nil, fmt.Errorf("%w: unknown tool key: %s", ErrToolConfigInvalid, key)
	}
	displayName := strings.TrimSpace(input.DisplayName)
	if displayName == "" {
		return nil, fmt.Errorf("%w: display_name is required", ErrToolConfigInvalid)
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	return &model.ToolConfig{
		Key:            key,
		DisplayName:    displayName,
		Enabled:        enabled,
		TimeoutSeconds: normalizeToolTimeout(input.TimeoutSeconds),
		SortOrder:      input.SortOrder,
	}, nil
}

func mergeToolConfigDefaults(rows []*model.ToolConfig) []*model.ToolConfig {
	byKey := make(map[string]*model.ToolConfig)
	for _, item := range DefaultToolConfigs() {
		copy := *item
		byKey[item.Key] = &copy
	}
	for _, item := range rows {
		if item == nil {
			continue
		}
		key := normalizeToolKey(item.Key)
		if !knownToolKey(key) {
			continue
		}
		byKey[key] = item
	}
	out := DefaultToolConfigs()
	for i, item := range out {
		out[i] = byKey[item.Key]
	}
	return out
}

func knownToolKey(key string) bool {
	for _, item := range DefaultToolConfigs() {
		if item.Key == key {
			return true
		}
	}
	return false
}

func normalizeToolKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizeToolTimeout(value int) int {
	if value <= 0 {
		return defaultToolTimeoutSeconds
	}
	if value > 120 {
		return 120
	}
	return value
}
