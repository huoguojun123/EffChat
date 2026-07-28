package middleware

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/effchat/internal/model"
	"github.com/huoguojun123/effchat/internal/service"
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
			c.JSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
			c.Abort()
			return
		}

		// 解析 Bearer token
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid authorization header format"})
			c.Abort()
			return
		}

		tokenString := parts[1]

		// 验证 token
		claims, err := authService.ValidateToken(tokenString)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			c.Abort()
			return
		}

		// 新 token 用 json.Number 保留整数精度；旧 token 仍兼容 float64。
		userID, ok := claimInt64((*claims)["user_id"])
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "malformed token claims"})
			c.Abort()
			return
		}
		authVersion := 1
		if value, exists := (*claims)["auth_version"]; exists {
			version, valid := claimInt64(value)
			if !valid || version > int64(maxInt) {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "malformed token claims"})
				c.Abort()
				return
			}
			authVersion = int(version)
		}

		user, err := resolveCurrentUser(userID, authVersion)
		if err != nil {
			status := http.StatusInternalServerError
			message := "authentication service unavailable"
			if errors.Is(err, service.ErrAuthenticationUnavailable) {
				status = http.StatusUnauthorized
				message = "account is unavailable or token has been invalidated"
			}
			c.JSON(status, gin.H{"error": message})
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
			c.JSON(http.StatusForbidden, gin.H{"error": "admin access required"})
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
