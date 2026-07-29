package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/middleware"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
)

type promptGroupRequest struct {
	Name string `json:"name" binding:"required"`
}

func ListPromptGroupsHandler(repo *repository.PromptGroupRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		groups, err := repo.ListByUser(middleware.GetUserID(c))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list prompt groups"})
			return
		}
		if groups == nil {
			groups = []*model.PromptGroup{}
		}
		c.JSON(http.StatusOK, gin.H{"groups": groups})
	}
}

func CreatePromptGroupHandler(repo *repository.PromptGroupRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req promptGroupRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}
		group, err := repo.Create(middleware.GetUserID(c), req.Name)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusCreated, group)
	}
}

func UpdatePromptGroupHandler(repo *repository.PromptGroupRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := promptGroupID(c)
		if !ok {
			return
		}
		var req promptGroupRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}
		group, err := repo.UpdateContext(c.Request.Context(), id, middleware.GetUserID(c), req.Name)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "prompt group not found"})
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, group)
	}
}

func DeletePromptGroupHandler(repo *repository.PromptGroupRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := promptGroupID(c)
		if !ok {
			return
		}
		if err := repo.DeleteContext(c.Request.Context(), id, middleware.GetUserID(c)); err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "prompt group not found"})
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "prompt group deleted"})
	}
}

func promptGroupID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid prompt group id"})
		return 0, false
	}
	return id, true
}
