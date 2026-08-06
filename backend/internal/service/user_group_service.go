package service

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
)

var ErrUserGroupInvalid = errors.New("invalid user group")

const (
	userGroupNameMaxRunes        = 50
	userGroupDescriptionMaxRunes = 200
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
	return s.CreateContext(context.Background(), req)
}

func (s *UserGroupService) CreateContext(ctx context.Context, req *CreateGroupRequest) (*model.UserGroup, error) {
	if err := validateUserGroupText(req.Name, req.Description); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUserGroupInvalid, err)
	}
	if req.Level < 0 {
		return nil, fmt.Errorf("%w: level must be >= 0", ErrUserGroupInvalid)
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
		return nil, fmt.Errorf("%w: %v", ErrUserGroupInvalid, err)
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
	if err := s.groupRepo.CreateContext(ctx, g); err != nil {
		return nil, err
	}
	return g, nil
}

func (s *UserGroupService) Update(id int64, req *UpdateGroupRequest) (*model.UserGroup, error) {
	return s.UpdateContext(context.Background(), id, req)
}

func (s *UserGroupService) UpdateContext(ctx context.Context, id int64, req *UpdateGroupRequest) (*model.UserGroup, error) {
	patch := repository.UserGroupPatch{
		Name: req.Name, Level: req.Level, Description: req.Description, IsDefault: req.IsDefault,
		DailyMessageLimit: req.DailyMessageLimit, DailyTokenLimit: req.DailyTokenLimit,
		ConcurrentRunLimit: req.ConcurrentRunLimit, DailyToolCallLimit: req.DailyToolCallLimit,
		DailyWebSearchLimit: req.DailyWebSearchLimit, DailyWebExtractLimit: req.DailyWebExtractLimit,
		DailyOCRFileLimit: req.DailyOCRFileLimit, DailyOCRPageLimit: req.DailyOCRPageLimit,
	}
	return s.groupRepo.UpdateFieldsContext(ctx, id, patch, validateUserGroup)
}

func validateUserGroup(g *model.UserGroup) error {
	if g.Level < 0 {
		return fmt.Errorf("%w: level must be >= 0", ErrUserGroupInvalid)
	}
	if err := validateUserGroupText(g.Name, g.Description); err != nil {
		return fmt.Errorf("%w: %v", ErrUserGroupInvalid, err)
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
		return fmt.Errorf("%w: %v", ErrUserGroupInvalid, err)
	}
	return nil
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

func validateUserGroupText(name, description string) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if utf8.RuneCountInString(name) > userGroupNameMaxRunes {
		return fmt.Errorf("name must be at most %d characters", userGroupNameMaxRunes)
	}
	if utf8.RuneCountInString(description) > userGroupDescriptionMaxRunes {
		return fmt.Errorf("description must be at most %d characters", userGroupDescriptionMaxRunes)
	}
	return nil
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
	return s.DeleteContext(context.Background(), id)
}

func (s *UserGroupService) DeleteContext(ctx context.Context, id int64) error {
	return s.groupRepo.DeleteContext(ctx, id)
}
