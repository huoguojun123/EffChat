package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/service"
)

// ListUserGroupsHandler 分级组列表（admin）
func ListUserGroupsHandler(groupService *service.UserGroupService) gin.HandlerFunc {
	return func(c *gin.Context) {
		groups, err := groupService.List()
		if err != nil {
			writeServerError(c, http.StatusInternalServerError, "user_group_list_failed", "failed to list user groups", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"groups": groups, "total": len(groups)})
	}
}

// CreateUserGroupHandler 新建分级组（admin）
func CreateUserGroupHandler(groupService *service.UserGroupService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req service.CreateGroupRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}
		g, err := groupService.CreateContext(c.Request.Context(), &req)
		if err != nil {
			writeUserGroupError(c, "create", err)
			return
		}
		c.JSON(http.StatusCreated, g)
	}
}

// UpdateUserGroupHandler 更新分级组（admin）
func UpdateUserGroupHandler(groupService *service.UserGroupService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := userGroupID(c)
		if !ok {
			return
		}
		var req service.UpdateGroupRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}
		g, err := groupService.UpdateContext(c.Request.Context(), id, &req)
		if err != nil {
			writeUserGroupError(c, "update", err)
			return
		}
		c.JSON(http.StatusOK, g)
	}
}

// DeleteUserGroupHandler 删除分级组（admin）
func DeleteUserGroupHandler(groupService *service.UserGroupService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := userGroupID(c)
		if !ok {
			return
		}
		if err := groupService.DeleteContext(c.Request.Context(), id); err != nil {
			writeUserGroupError(c, "delete", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "user group deleted"})
	}
}
