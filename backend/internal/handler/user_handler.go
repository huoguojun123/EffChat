package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/middleware"
	"github.com/huoguojun123/EffChat/internal/service"
)

// GetMeHandler 获取当前用户信息
func GetMeHandler(authService *service.AuthService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		user, err := authService.GetProfile(userID)
		if err != nil {
			writeUserProfileError(c, "load", err)
			return
		}
		c.JSON(http.StatusOK, user)
	}
}

// UpdateMeHandler 更新当前用户个人信息
func UpdateMeHandler(authService *service.AuthService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)

		var req service.UpdateProfileRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}

		user, err := authService.UpdateProfile(userID, &req)
		if err != nil {
			writeUserProfileError(c, "update", err)
			return
		}

		c.JSON(http.StatusOK, user)
	}
}

// ChangePasswordHandler 修改密码
func ChangePasswordHandler(authService *service.AuthService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)

		var req service.ChangePasswordRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}

		if err := authService.ChangePassword(userID, &req); err != nil {
			writeUserProfileError(c, "change_password", err)
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "password changed"})
	}
}

// ListUsersHandler 管理员获取用户列表
func ListUsersHandler(userAdminService *service.UserAdminService) gin.HandlerFunc {
	return func(c *gin.Context) {
		limit, offset, ok := adminUserPagination(c)
		if !ok {
			return
		}

		users, total, err := userAdminService.List(limit, offset)
		if err != nil {
			writeAdminUserError(c, "list", err)
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"users":       users,
			"total":       total,
			"has_more":    offset+len(users) < total,
			"next_offset": offset + len(users),
		})
	}
}

// CreateUserHandler 管理员创建用户
func CreateUserHandler(userAdminService *service.UserAdminService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req service.CreateUserRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}

		user, err := userAdminService.Create(&req)
		if err != nil {
			writeAdminUserError(c, "create", err)
			return
		}

		c.JSON(http.StatusCreated, user)
	}
}

// UpdateUserHandler 管理员更新用户角色/状态
func UpdateUserHandler(userAdminService *service.UserAdminService, limiters ...*AuthRateLimiter) gin.HandlerFunc {
	limiter := firstAuthRateLimiter(limiters)
	return func(c *gin.Context) {
		userID, ok := adminUserID(c)
		if !ok {
			return
		}

		var req service.UpdateUserRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}
		user, err := userAdminService.Update(userID, &req)
		if err != nil {
			writeAdminUserError(c, "update", err)
			return
		}
		if req.IsActive != nil && *req.IsActive {
			limiter.ResetAccount(user.Username)
		}

		c.JSON(http.StatusOK, user)
	}
}

// ResetUserPasswordHandler 管理员重置用户密码
func ResetUserPasswordHandler(userAdminService *service.UserAdminService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := adminUserID(c)
		if !ok {
			return
		}

		var req service.ResetPasswordRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}

		if err := userAdminService.ResetPassword(userID, &req); err != nil {
			writeAdminUserError(c, "reset_password", err)
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "password reset"})
	}
}

// SetUserGroupHandler 管理员设置用户分级组（group_id 为 null 时清空）
func SetUserGroupHandler(userAdminService *service.UserAdminService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := adminUserID(c)
		if !ok {
			return
		}

		var req service.SetGroupRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}

		resp, err := userAdminService.SetGroup(userID, req.GroupID)
		if err != nil {
			writeAdminUserError(c, "set_group", err)
			return
		}
		c.JSON(http.StatusOK, resp)
	}
}
