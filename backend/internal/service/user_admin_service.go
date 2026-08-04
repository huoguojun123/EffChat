package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
	"golang.org/x/crypto/bcrypt"
)

var ErrUserAdminInvalid = errors.New("invalid administrator user request")

const (
	adminUsernameMinRunes = 3
	adminUsernameMaxRunes = 50
	userNicknameMaxRunes  = 100
	userEmailMaxRunes     = 255
	userPasswordMinBytes  = 6
	userPasswordMaxBytes  = 72
)

type UserAdminService struct {
	userRepo *repository.UserRepository
	runHub   *RunHub
}

func NewUserAdminService(userRepo *repository.UserRepository) *UserAdminService {
	return &UserAdminService{userRepo: userRepo}
}

func (s *UserAdminService) SetRunHub(runHub *RunHub) {
	s.runHub = runHub
}

type UserResponse struct {
	ID             int64                      `json:"id"`
	Username       string                     `json:"username"`
	Email          *string                    `json:"email,omitempty"`
	Nickname       *string                    `json:"nickname,omitempty"`
	Role           string                     `json:"role"`
	GroupID        *int64                     `json:"group_id"`
	EffectiveGroup EffectiveUserGroupResponse `json:"effective_group"`
	Permissions    json.RawMessage            `json:"permissions,omitempty"`
	IsActive       bool                       `json:"is_active"`
	CreatedAt      string                     `json:"created_at"`
	LastLoginAt    *string                    `json:"last_login_at,omitempty"`
}

type EffectiveUserGroupResponse struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Level     int    `json:"level"`
	Inherited bool   `json:"inherited"`
}

type CreateUserRequest struct {
	Username    string          `json:"username" binding:"required,min=3,max=50"`
	Password    string          `json:"password" binding:"required,min=6"`
	Email       *string         `json:"email"`
	Nickname    *string         `json:"nickname"`
	Role        string          `json:"role"`
	Permissions json.RawMessage `json:"permissions"`
	IsActive    *bool           `json:"is_active"`
}

type UpdateUserRequest struct {
	Email       *string         `json:"email"`
	Nickname    *string         `json:"nickname"`
	Role        *string         `json:"role"`
	Permissions json.RawMessage `json:"permissions"`
	IsActive    *bool           `json:"is_active"`
}

type ResetPasswordRequest struct {
	Password string `json:"password" binding:"required,min=6"`
}

// SetGroupRequest 设置原始用户组；group_id 为 null 时动态继承当前默认组。
type SetGroupRequest struct {
	GroupID *int64 `json:"group_id"`
}

// SetGroup 设置用户所属分级组。
func (s *UserAdminService) SetGroup(userID int64, groupID *int64) (*UserResponse, error) {
	if groupID != nil && *groupID <= 0 {
		return nil, fmt.Errorf("%w: group_id must be a positive integer or null", ErrUserAdminInvalid)
	}
	if err := s.userRepo.SetGroup(userID, groupID); err != nil {
		return nil, err
	}
	user, err := s.userRepo.GetByIDIncludeInactive(userID)
	if err != nil {
		return nil, err
	}
	return toUserResponse(user), nil
}

func (s *UserAdminService) List(limit, offset int) ([]*UserResponse, int, error) {
	users, err := s.userRepo.ListAll(limit, offset)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.userRepo.CountAll()
	if err != nil {
		return nil, 0, err
	}

	result := make([]*UserResponse, len(users))
	for i, u := range users {
		result[i] = toUserResponse(u)
	}
	return result, total, nil
}

func (s *UserAdminService) Create(req *CreateUserRequest) (*UserResponse, error) {
	if err := validateUsername(req.Username); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUserAdminInvalid, err)
	}
	if err := validateUserPassword(req.Password); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUserAdminInvalid, err)
	}
	if err := validateUserNickname(req.Nickname); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUserAdminInvalid, err)
	}
	email, err := normalizeOptionalEmail(req.Email)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUserAdminInvalid, err)
	}

	existing, err := s.userRepo.GetByUsername(req.Username)
	if err != nil && !isNotFound(err) {
		return nil, err
	}
	if existing != nil {
		return nil, repository.ErrUserConflict
	}

	role := req.Role
	if role == "" {
		role = "user"
	}
	if err := validateRole(role); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUserAdminInvalid, err)
	}

	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}

	permissions := req.Permissions
	if len(permissions) == 0 {
		permissions = []byte(`{}`)
	}
	if !json.Valid(permissions) {
		return nil, fmt.Errorf("%w: permissions must be valid json", ErrUserAdminInvalid)
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}
	preferences, err := buildUserPreferences(nil)
	if err != nil {
		return nil, err
	}

	user := &model.User{
		Username:     req.Username,
		Email:        email,
		Nickname:     req.Nickname,
		PasswordHash: string(hashedPassword),
		Role:         role,
		IsActive:     isActive,
		Permissions:  permissions,
		Preferences:  preferences,
	}
	if err := s.userRepo.Create(user); err != nil {
		return nil, err
	}
	created, err := s.userRepo.GetByIDIncludeInactive(user.ID)
	if err != nil {
		return nil, err
	}
	return toUserResponse(created), nil
}

func (s *UserAdminService) Update(userID int64, req *UpdateUserRequest) (*UserResponse, error) {
	user, err := s.userRepo.GetByIDIncludeInactive(userID)
	if err != nil {
		return nil, err
	}
	invalidateActiveRuns := (req.Role != nil && *req.Role != user.Role) || (req.IsActive != nil && *req.IsActive != user.IsActive)
	email, err := normalizeOptionalEmail(req.Email)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUserAdminInvalid, err)
	}
	if err := validateUserNickname(req.Nickname); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUserAdminInvalid, err)
	}

	if req.Role != nil {
		if err := validateRole(*req.Role); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrUserAdminInvalid, err)
		}
		user.Role = *req.Role
	}
	if req.IsActive != nil {
		user.IsActive = *req.IsActive
	}
	if req.Email != nil {
		user.Email = email
	}
	if req.Nickname != nil {
		if *req.Nickname == "" {
			user.Nickname = nil
		} else {
			user.Nickname = req.Nickname
		}
	}
	if len(req.Permissions) > 0 {
		if !json.Valid(req.Permissions) {
			return nil, fmt.Errorf("%w: permissions must be valid json", ErrUserAdminInvalid)
		}
		user.Permissions = req.Permissions
	}

	if err := s.userRepo.UpdateAdminFields(user); err != nil {
		return nil, err
	}
	if s.runHub != nil && invalidateActiveRuns {
		s.runHub.CancelByUser(userID)
	}
	return toUserResponse(user), nil
}

// normalizeOptionalEmail 统一个人资料与管理员用户维护的邮箱口径。
//
// 前端为了保持表单可控，空输入会自然提交为空字符串；如果仍把 gin 的
// binding:"omitempty,email" 放在 *string 字段上，空字符串指针有时会先触发
// email 校验并返回一条很吵的错误，虽然用户本意只是“没有邮箱”。这里把校验
// 下沉到 service：nil 或空白都表示不设置邮箱，非空才按 email 地址校验。
func normalizeOptionalEmail(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(trimmed) > userEmailMaxRunes {
		return nil, fmt.Errorf("email must be at most %d characters", userEmailMaxRunes)
	}
	addr, err := mail.ParseAddress(trimmed)
	if err != nil || addr.Address != trimmed {
		return nil, fmt.Errorf("invalid email")
	}
	return &trimmed, nil
}

func (s *UserAdminService) ResetPassword(userID int64, req *ResetPasswordRequest) error {
	if err := validateUserPassword(req.Password); err != nil {
		return fmt.Errorf("%w: %v", ErrUserAdminInvalid, err)
	}
	if _, err := s.userRepo.GetByIDIncludeInactive(userID); err != nil {
		return err
	}
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}
	if err := s.userRepo.UpdatePassword(userID, string(hashedPassword)); err != nil {
		return err
	}
	if s.runHub != nil {
		s.runHub.CancelByUser(userID)
	}
	return nil
}

func toUserResponse(u *model.User) *UserResponse {
	effectiveGroup := EffectiveUserGroupResponse{Inherited: u.GroupID == nil}
	if u.EffectiveGroup != nil {
		effectiveGroup.ID = u.EffectiveGroup.ID
		effectiveGroup.Name = u.EffectiveGroup.Name
		effectiveGroup.Level = u.EffectiveGroup.Level
	}
	resp := &UserResponse{
		ID:             u.ID,
		Username:       u.Username,
		Email:          u.Email,
		Nickname:       u.Nickname,
		Role:           u.Role,
		GroupID:        u.GroupID,
		EffectiveGroup: effectiveGroup,
		Permissions:    json.RawMessage(u.Permissions),
		IsActive:       u.IsActive,
		CreatedAt:      u.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	if u.LastLoginAt != nil {
		s := u.LastLoginAt.Format("2006-01-02T15:04:05Z07:00")
		resp.LastLoginAt = &s
	}
	return resp
}

func validateRole(role string) error {
	if role != "admin" && role != "user" {
		return fmt.Errorf("invalid role: must be admin or user")
	}
	return nil
}

func validateUsername(username string) error {
	length := utf8.RuneCountInString(username)
	if length < adminUsernameMinRunes || length > adminUsernameMaxRunes {
		return fmt.Errorf("username must be between %d and %d characters", adminUsernameMinRunes, adminUsernameMaxRunes)
	}
	return nil
}

func validateUserNickname(nickname *string) error {
	if nickname != nil && utf8.RuneCountInString(*nickname) > userNicknameMaxRunes {
		return fmt.Errorf("nickname must be at most %d characters", userNicknameMaxRunes)
	}
	return nil
}

func validateUserPassword(password string) error {
	length := len(password)
	if length < userPasswordMinBytes || length > userPasswordMaxBytes {
		return fmt.Errorf("password must be between %d and %d bytes", userPasswordMinBytes, userPasswordMaxBytes)
	}
	return nil
}

func isNotFound(err error) bool {
	return errors.Is(err, repository.ErrNotFound)
}
