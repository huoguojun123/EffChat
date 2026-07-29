package service

import (
	"fmt"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
)

type UserGroupService struct {
	groupRepo *repository.UserGroupRepository
}

func NewUserGroupService(groupRepo *repository.UserGroupRepository) *UserGroupService {
	return &UserGroupService{groupRepo: groupRepo}
}

// CreateGroupRequest 新建分级组请求
type CreateGroupRequest struct {
	Name                 string `json:"name" binding:"required,max=50"`
	Level                int    `json:"level"`
	Description          string `json:"description"`
	IsDefault            bool   `json:"is_default"`
	DailyMessageLimit    int    `json:"daily_message_limit"`
	DailyTokenLimit      int    `json:"daily_token_limit"`
	ConcurrentRunLimit   int    `json:"concurrent_run_limit"`
	DailyToolCallLimit   int    `json:"daily_tool_call_limit"`
	DailyWebSearchLimit  int    `json:"daily_web_search_limit"`
	DailyWebExtractLimit int    `json:"daily_web_extract_limit"`
	DailyOCRFileLimit    int    `json:"daily_ocr_file_limit"`
	DailyOCRPageLimit    int    `json:"daily_ocr_page_limit"`
}

// UpdateGroupRequest 更新分级组请求（指针字段，仅更新提供的项）
type UpdateGroupRequest struct {
	Name                 *string `json:"name"`
	Level                *int    `json:"level"`
	Description          *string `json:"description"`
	IsDefault            *bool   `json:"is_default"`
	DailyMessageLimit    *int    `json:"daily_message_limit"`
	DailyTokenLimit      *int    `json:"daily_token_limit"`
	ConcurrentRunLimit   *int    `json:"concurrent_run_limit"`
	DailyToolCallLimit   *int    `json:"daily_tool_call_limit"`
	DailyWebSearchLimit  *int    `json:"daily_web_search_limit"`
	DailyWebExtractLimit *int    `json:"daily_web_extract_limit"`
	DailyOCRFileLimit    *int    `json:"daily_ocr_file_limit"`
	DailyOCRPageLimit    *int    `json:"daily_ocr_page_limit"`
}

func (s *UserGroupService) List() ([]*model.UserGroup, error) {
	return s.groupRepo.List()
}

func (s *UserGroupService) Create(req *CreateGroupRequest) (*model.UserGroup, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if req.Level < 0 {
		return nil, fmt.Errorf("level must be >= 0")
	}
	if err := validateGroupLimits(groupLimitValues{
		DailyMessages:    req.DailyMessageLimit,
		DailyTokens:      req.DailyTokenLimit,
		ConcurrentRuns:   req.ConcurrentRunLimit,
		DailyToolCalls:   req.DailyToolCallLimit,
		DailyWebSearches: req.DailyWebSearchLimit,
		DailyWebExtracts: req.DailyWebExtractLimit,
		DailyOCRFiles:    req.DailyOCRFileLimit,
		DailyOCRPages:    req.DailyOCRPageLimit,
	}); err != nil {
		return nil, err
	}
	g := &model.UserGroup{
		Name:                 req.Name,
		Level:                req.Level,
		Description:          req.Description,
		IsDefault:            req.IsDefault,
		DailyMessageLimit:    req.DailyMessageLimit,
		DailyTokenLimit:      req.DailyTokenLimit,
		ConcurrentRunLimit:   req.ConcurrentRunLimit,
		DailyToolCallLimit:   req.DailyToolCallLimit,
		DailyWebSearchLimit:  req.DailyWebSearchLimit,
		DailyWebExtractLimit: req.DailyWebExtractLimit,
		DailyOCRFileLimit:    req.DailyOCRFileLimit,
		DailyOCRPageLimit:    req.DailyOCRPageLimit,
	}
	if err := s.groupRepo.Create(g); err != nil {
		return nil, err
	}
	return g, nil
}

func (s *UserGroupService) Update(id int64, req *UpdateGroupRequest) (*model.UserGroup, error) {
	g, err := s.groupRepo.Get(id)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, fmt.Errorf("user group not found: %d", id)
	}
	if req.Name != nil {
		if *req.Name == "" {
			return nil, fmt.Errorf("name cannot be empty")
		}
		g.Name = *req.Name
	}
	if req.Level != nil {
		if *req.Level < 0 {
			return nil, fmt.Errorf("level must be >= 0")
		}
		g.Level = *req.Level
	}
	if req.Description != nil {
		g.Description = *req.Description
	}
	if req.IsDefault != nil {
		g.IsDefault = *req.IsDefault
	}
	if req.DailyMessageLimit != nil {
		g.DailyMessageLimit = *req.DailyMessageLimit
	}
	if req.DailyTokenLimit != nil {
		g.DailyTokenLimit = *req.DailyTokenLimit
	}
	if req.ConcurrentRunLimit != nil {
		g.ConcurrentRunLimit = *req.ConcurrentRunLimit
	}
	if req.DailyToolCallLimit != nil {
		g.DailyToolCallLimit = *req.DailyToolCallLimit
	}
	if req.DailyWebSearchLimit != nil {
		g.DailyWebSearchLimit = *req.DailyWebSearchLimit
	}
	if req.DailyWebExtractLimit != nil {
		g.DailyWebExtractLimit = *req.DailyWebExtractLimit
	}
	if req.DailyOCRFileLimit != nil {
		g.DailyOCRFileLimit = *req.DailyOCRFileLimit
	}
	if req.DailyOCRPageLimit != nil {
		g.DailyOCRPageLimit = *req.DailyOCRPageLimit
	}
	if err := validateGroupLimits(groupLimitValues{
		DailyMessages:    g.DailyMessageLimit,
		DailyTokens:      g.DailyTokenLimit,
		ConcurrentRuns:   g.ConcurrentRunLimit,
		DailyToolCalls:   g.DailyToolCallLimit,
		DailyWebSearches: g.DailyWebSearchLimit,
		DailyWebExtracts: g.DailyWebExtractLimit,
		DailyOCRFiles:    g.DailyOCRFileLimit,
		DailyOCRPages:    g.DailyOCRPageLimit,
	}); err != nil {
		return nil, err
	}
	if err := s.groupRepo.Update(g); err != nil {
		return nil, err
	}
	return g, nil
}

type groupLimitValues struct {
	DailyMessages    int
	DailyTokens      int
	ConcurrentRuns   int
	DailyToolCalls   int
	DailyWebSearches int
	DailyWebExtracts int
	DailyOCRFiles    int
	DailyOCRPages    int
}

func validateGroupLimits(limits groupLimitValues) error {
	if limits.DailyMessages < 0 {
		return fmt.Errorf("daily_message_limit must be >= 0")
	}
	if limits.DailyTokens < 0 {
		return fmt.Errorf("daily_token_limit must be >= 0")
	}
	if limits.ConcurrentRuns < 0 {
		return fmt.Errorf("concurrent_run_limit must be >= 0")
	}
	if limits.DailyToolCalls < 0 {
		return fmt.Errorf("daily_tool_call_limit must be >= 0")
	}
	if limits.DailyWebSearches < 0 {
		return fmt.Errorf("daily_web_search_limit must be >= 0")
	}
	if limits.DailyWebExtracts < 0 {
		return fmt.Errorf("daily_web_extract_limit must be >= 0")
	}
	if limits.DailyOCRFiles < 0 {
		return fmt.Errorf("daily_ocr_file_limit must be >= 0")
	}
	if limits.DailyOCRPages < 0 {
		return fmt.Errorf("daily_ocr_page_limit must be >= 0")
	}
	return nil
}

func (s *UserGroupService) Delete(id int64) error {
	return s.groupRepo.Delete(id)
}
