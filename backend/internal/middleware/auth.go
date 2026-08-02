package middleware

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/service"
	"github.com/huoguojun123/EffChat/pkg/logger"
)

const maxInt64ForJWTClaim = int64(^uint64(0) >> 1)
const maxInt = int(^uint(0) >> 1)

type authStateResolver func(userID int64, authVersion int) (*model.User, error)

// AuthMiddleware JWT 认证中间件
func AuthMiddleware(authService *service.AuthService) gin.HandlerFunc {
	return authMiddleware(authService, authService.ResolveAuthenticatedUser)
}

func authMiddleware(authService *service.AuthService, resolveCurrentUser authStateResolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 从 Header 获取 token
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			writeAuthError(c, http.StatusUnauthorized, "auth_header_missing", "missing authorization header", false)
			c.Abort()
			return
		}

		// 解析 Bearer token
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			writeAuthError(c, http.StatusUnauthorized, "auth_header_invalid", "invalid authorization header format", false)
			c.Abort()
			return
		}

		tokenString := parts[1]

		// 验证 token
		claims, err := authService.ValidateToken(tokenString)
		if err != nil {
			writeAuthError(c, http.StatusUnauthorized, "auth_token_invalid", "invalid or expired token", false)
			c.Abort()
			return
		}

		// 新 token 用 json.Number 保留整数精度；旧 token 仍兼容 float64。
		userID, ok := claimInt64((*claims)["user_id"])
		if !ok {
			writeAuthError(c, http.StatusUnauthorized, "auth_claims_invalid", "malformed token claims", false)
			c.Abort()
			return
		}
		authVersion := 1
		if value, exists := (*claims)["auth_version"]; exists {
			version, valid := claimInt64(value)
			if !valid || version > int64(maxInt) {
				writeAuthError(c, http.StatusUnauthorized, "auth_claims_invalid", "malformed token claims", false)
				c.Abort()
				return
			}
			authVersion = int(version)
		}

		user, err := resolveCurrentUser(userID, authVersion)
		if err != nil {
			if errors.Is(err, service.ErrAuthenticationUnavailable) {
				writeAuthError(c, http.StatusUnauthorized, "auth_session_invalid", "account is unavailable or token has been invalidated", false)
			} else {
				writeAuthServerError(c, "auth_state_load_failed", "authentication service unavailable", err)
			}
			c.Abort()
			return
		}
		c.Set("user_id", user.ID)
		c.Set("username", user.Username)
		c.Set("role", user.Role)
		c.Set("auth_version", user.AuthVersion)

		c.Next()
	}
}

func writeAuthError(c *gin.Context, status int, code, message string, retryable bool) {
	c.JSON(status, gin.H{"error": message, "code": code, "retryable": retryable})
}

func writeAuthServerError(c *gin.Context, code, message string, err error) {
	requestID := c.GetString("request_id")
	logger.Error("authentication request failed: request_id=%q method=%s path=%s code=%s err=%v", requestID, c.Request.Method, c.Request.URL.Path, code, err)
	payload := gin.H{"error": message, "code": code, "retryable": true}
	if requestID != "" {
		payload["request_id"] = requestID
	}
	c.JSON(http.StatusInternalServerError, payload)
}

func claimInt64(value interface{}) (int64, bool) {
	switch v := value.(type) {
	case json.Number:
		id, err := v.Int64()
		return id, err == nil && id > 0
	case float64:
		if v <= 0 || v > float64(maxInt64ForJWTClaim) || math.Trunc(v) != v {
			return 0, false
		}
		return int64(v), true
	default:
		return 0, false
	}
}

// AdminMiddleware 要求当前用户角色为 admin，需挂载在 AuthMiddleware 之后。
// 依赖 AuthMiddleware 已将 role 写入 context。
func AdminMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if GetRole(c) != "admin" {
			writeAuthError(c, http.StatusForbidden, "admin_access_required", "admin access required", false)
			c.Abort()
			return
		}
		c.Next()
	}
}

// GetUserID 从 context 获取 user_id
func GetUserID(c *gin.Context) int64 {
	userID, exists := c.Get("user_id")
	if !exists {
		return 0
	}
	return userID.(int64)
}

// GetUsername 从 context 获取 username
func GetUsername(c *gin.Context) string {
	username, exists := c.Get("username")
	if !exists {
		return ""
	}
	return username.(string)
}

// GetRole 从 context 获取 role
func GetRole(c *gin.Context) string {
	role, exists := c.Get("role")
	if !exists {
		return ""
	}
	return role.(string)
}

func GetAuthVersion(c *gin.Context) int {
	authVersion, exists := c.Get("auth_version")
	if !exists {
		return 0
	}
	return authVersion.(int)
}
