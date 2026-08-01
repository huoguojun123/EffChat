package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/pkg/logger"
	"golang.org/x/crypto/bcrypt"
)

type AuthService struct {
	userRepo  *repository.UserRepository
	jwtSecret []byte
	runHub    *RunHub
}

var (
	// ErrInternal 标记内部故障（DB 异常等），handler 应返回 500 而非 400。
	ErrInternal = errors.New("internal error")
	// ErrAuthenticationUnavailable 表示 token 对应的账号已不可用或已失效。
	ErrAuthenticationUnavailable = errors.New("authentication unavailable")
	ErrAccountInactive           = errors.New("account inactive")
	ErrUserProfileInvalid        = errors.New("invalid user profile request")
	ErrIncorrectOldPassword      = errors.New("incorrect old password")
	ErrUserRegistrationInvalid   = errors.New("invalid user registration request")
	ErrInvalidCredentials        = errors.New("invalid credentials")
)

func NewAuthService(userRepo *repository.UserRepository, jwtSecret string) *AuthService {
	return &AuthService{
		userRepo:  userRepo,
		jwtSecret: []byte(jwtSecret),
	}
}

func (s *AuthService) SetRunHub(runHub *RunHub) {
	s.runHub = runHub
}

type RegisterRequest struct {
	Username    string          `json:"username" binding:"required,min=3,max=50"`
	Password    string          `json:"password" binding:"required,min=6"`
	Email       string          `json:"email" binding:"omitempty,email"`
	Nickname    string          `json:"nickname"`
	Preferences json.RawMessage `json:"preferences,omitempty"`
}

type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type AuthResponse struct {
	Token string      `json:"token"`
	User  *model.User `json:"user"`
}

type RegisterResponse struct {
	Approved bool        `json:"approved"`
	Message  string      `json:"message"`
	Token    string      `json:"token,omitempty"`
	User     *model.User `json:"user,omitempty"`
}

// Register 用户注册
func (s *AuthService) Register(req *RegisterRequest) (*RegisterResponse, error) {
	if err := validateUsername(req.Username); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUserRegistrationInvalid, err)
	}
	if err := validateUserPassword(req.Password); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUserRegistrationInvalid, err)
	}
	nickname := req.Nickname
	if err := validateUserNickname(&nickname); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUserRegistrationInvalid, err)
	}
	email, err := normalizeOptionalEmail(&req.Email)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUserRegistrationInvalid, err)
	}
	preferences, err := buildUserPreferences(req.Preferences)
	if err != nil {
		if errors.Is(err, ErrInternal) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", ErrUserRegistrationInvalid, err)
	}

	// 检查用户名是否已存在（区分"不存在"和真实 DB 错误）
	existing, err := s.userRepo.GetByUsername(req.Username)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}
	if existing != nil {
		return nil, repository.ErrUserConflict
	}

	// 哈希密码
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}
	// 创建用户
	user := &model.User{
		Username:     req.Username,
		PasswordHash: string(hashedPassword),
		Role:         "user",
		IsActive:     false,
		Permissions:  []byte(`{}`),
		Preferences:  preferences,
	}

	if email != nil {
		user.Email = email
	}
	if nickname != "" {
		user.Nickname = &nickname
	}

	if err := s.userRepo.CreateRegistrationUser(user); err != nil {
		return nil, err
	}

	// 隐藏密码
	user.PasswordHash = ""

	if !user.IsActive {
		return &RegisterResponse{
			Approved: false,
			Message:  "注册成功，等待管理员审核后即可登录",
		}, nil
	}

	token, err := s.generateToken(user)
	if err != nil {
		return nil, fmt.Errorf("failed to generate token: %w", err)
	}

	return &RegisterResponse{
		Approved: true,
		Message:  "注册成功",
		Token:    token,
		User:     user,
	}, nil
}

const defaultUserPreferencesJSON = `{"theme":"dark","language":"zh-CN","timezone":"Asia/Shanghai","verbosity":"简洁","message_format":"v1"}`

const maxUserPreferencesBytes = 8 << 10

func buildUserPreferences(raw json.RawMessage) ([]byte, error) {
	if len(raw) > maxUserPreferencesBytes {
		return nil, fmt.Errorf("preferences must not exceed %d bytes", maxUserPreferencesBytes)
	}
	var preferences map[string]interface{}
	if err := json.Unmarshal([]byte(defaultUserPreferencesJSON), &preferences); err != nil {
		return nil, fmt.Errorf("failed to load default preferences: %w", ErrInternal)
	}
	if len(raw) > 0 {
		var custom map[string]interface{}
		if err := json.Unmarshal(raw, &custom); err != nil || custom == nil {
			return nil, fmt.Errorf("preferences must be a JSON object")
		}
		if len(custom) > 32 {
			return nil, fmt.Errorf("preferences may contain at most 32 fields")
		}
		for key, value := range custom {
			if len(key) == 0 || len(key) > 64 || !isPreferenceValue(value, 0) {
				return nil, fmt.Errorf("preferences contain an invalid field")
			}
			preferences[key] = value
		}
	}
	data, err := json.Marshal(preferences)
	if err != nil {
		return nil, fmt.Errorf("failed to encode preferences: %w", ErrInternal)
	}
	return data, nil
}

func isPreferenceValue(value interface{}, depth int) bool {
	if depth > 2 {
		return false
	}
	switch typed := value.(type) {
	case nil, bool, float64:
		return true
	case string:
		return len(typed) <= 512
	case []interface{}:
		if len(typed) > 16 {
			return false
		}
		for _, item := range typed {
			if !isPreferenceValue(item, depth+1) {
				return false
			}
		}
		return true
	case map[string]interface{}:
		if len(typed) > 16 {
			return false
		}
		for key, item := range typed {
			if len(key) == 0 || len(key) > 64 || !isPreferenceValue(item, depth+1) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// Login 用户登录
func (s *AuthService) Login(req *LoginRequest) (*AuthResponse, error) {
	// 查找用户
	user, err := s.userRepo.GetByUsername(req.Username)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}

	// 检查用户是否激活
	if !user.IsActive {
		return nil, fmt.Errorf("账号待审核或已停用: %w", ErrAccountInactive)
	}

	// 验证密码
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		return nil, ErrInvalidCredentials
	}

	// 更新最后登录时间
	if err := s.userRepo.UpdateLastLogin(user.ID); err != nil {
		logger.Error("update last login failed: user=%d err=%v", user.ID, err)
	}

	// 生成 JWT
	token, err := s.generateToken(user)
	if err != nil {
		return nil, fmt.Errorf("failed to generate token: %w", err)
	}

	// 隐藏密码
	user.PasswordHash = ""

	return &AuthResponse{
		Token: token,
		User:  user,
	}, nil
}

// generateToken 生成 JWT token
func (s *AuthService) generateToken(user *model.User) (string, error) {
	claims := jwt.MapClaims{
		"user_id":      user.ID,
		"auth_version": user.AuthVersion,
		"exp":          time.Now().Add(7 * 24 * time.Hour).Unix(), // 7 天过期
		"iat":          time.Now().Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.jwtSecret)
}

// ResolveAuthenticatedUser loads the current active account for an authenticated request.
func (s *AuthService) ResolveAuthenticatedUser(userID int64, authVersion int) (*model.User, error) {
	user, err := s.userRepo.GetByID(userID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, ErrAuthenticationUnavailable
	}
	if err != nil {
		return nil, fmt.Errorf("resolve authenticated user: %w", ErrInternal)
	}
	if authVersion <= 0 || user.AuthVersion != authVersion {
		return nil, ErrAuthenticationUnavailable
	}
	user.PasswordHash = ""
	return user, nil
}

// ValidateToken 验证 JWT token
func (s *AuthService) ValidateToken(tokenString string) (*jwt.MapClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, jwt.MapClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return s.jwtSecret, nil
	}, jwt.WithJSONNumber())

	if err != nil {
		return nil, fmt.Errorf("invalid token: %w", err)
	}

	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		return &claims, nil
	}

	return nil, fmt.Errorf("invalid token claims")
}

// UpdateProfileRequest 更新个人信息请求
type UpdateProfileRequest struct {
	Nickname *string `json:"nickname"`
	Email    *string `json:"email" binding:"omitempty,email"`
}

// GetProfile 获取用户信息（不含密码）
func (s *AuthService) GetProfile(userID int64) (*model.User, error) {
	return s.GetProfileContext(context.Background(), userID)
}

func (s *AuthService) GetProfileContext(ctx context.Context, userID int64) (*model.User, error) {
	user, err := s.userRepo.GetByIDContext(ctx, userID)
	if err != nil {
		return nil, err
	}
	user.PasswordHash = ""
	return user, nil
}

// UpdateProfile 更新用户个人信息
func (s *AuthService) UpdateProfile(userID int64, req *UpdateProfileRequest) (*model.User, error) {
	if err := validateUserNickname(req.Nickname); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUserProfileInvalid, err)
	}
	email, err := normalizeOptionalEmail(req.Email)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUserProfileInvalid, err)
	}

	user, err := s.userRepo.GetByID(userID)
	if err != nil {
		return nil, err
	}

	if req.Nickname != nil {
		user.Nickname = req.Nickname
	}
	if req.Email != nil {
		user.Email = email
	}

	if err := s.userRepo.Update(user); err != nil {
		return nil, err
	}

	user.PasswordHash = ""
	return user, nil
}

func (s *AuthService) UpdateAvatar(userID int64, avatarURL *string) (*model.User, error) {
	user, err := s.userRepo.GetByID(userID)
	if err != nil {
		return nil, err
	}
	user.AvatarURL = avatarURL
	if err := s.userRepo.Update(user); err != nil {
		return nil, err
	}
	user.PasswordHash = ""
	return user, nil
}

// ChangePasswordRequest 修改密码请求
type ChangePasswordRequest struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=6"`
}

// ChangePassword 修改密码
func (s *AuthService) ChangePassword(userID int64, req *ChangePasswordRequest) error {
	if err := validateUserPassword(req.NewPassword); err != nil {
		return fmt.Errorf("%w: %v", ErrUserProfileInvalid, err)
	}

	user, err := s.userRepo.GetByID(userID)
	if err != nil {
		return err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.OldPassword)); err != nil {
		return ErrIncorrectOldPassword
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
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
