package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/effchat/internal/service"
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
		g, err := groupService.Create(&req)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusCreated, g)
	}
}

// UpdateUserGroupHandler 更新分级组（admin）
func UpdateUserGroupHandler(groupService *service.UserGroupService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
			return
		}
		var req service.UpdateGroupRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}
		g, err := groupService.Update(id, &req)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, g)
	}
}

// DeleteUserGroupHandler 删除分级组（admin）
func DeleteUserGroupHandler(groupService *service.UserGroupService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
			return
		}
		if err := groupService.Delete(id); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "user group deleted"})
	}
}
