package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/middleware"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
)

type publicPromptResponse struct {
	ID          int64     `json:"id"`
	Title       string    `json:"title"`
	Content     string    `json:"content"`
	Description *string   `json:"description,omitempty"`
	Tags        []string  `json:"tags"`
	GroupID     *int64    `json:"group_id,omitempty"`
	GroupName   string    `json:"group_name"`
	IsPublic    bool      `json:"is_public"`
	UseCount    int       `json:"use_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type createPromptRequest struct {
	Title       string   `json:"title" binding:"required"`
	Content     string   `json:"content" binding:"required"`
	Description *string  `json:"description"`
	Tags        []string `json:"tags"`
	GroupID     *int64   `json:"group_id"`
	GroupName   string   `json:"group_name"`
	IsPublic    bool     `json:"is_public"`
}

type updatePromptRequest struct {
	Title       *string       `json:"title"`
	Content     *string       `json:"content"`
	Description *string       `json:"description"`
	Tags        []string      `json:"tags"`
	GroupID     optionalInt64 `json:"group_id"`
	GroupName   *string       `json:"group_name"`
	IsPublic    *bool         `json:"is_public"`
}

type optionalInt64 struct {
	Set   bool
	Value *int64
}

func (o *optionalInt64) UnmarshalJSON(data []byte) error {
	o.Set = true
	if string(data) == "null" {
		o.Value = nil
		return nil
	}
	var value int64
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	o.Value = &value
	return nil
}

// CreatePromptHandler 创建提示词
func CreatePromptHandler(promptRepo *repository.PromptRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)

		var req createPromptRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}
		if err := validateCreatePromptRequest(&req); err != nil {
			writePromptValidationError(c, err)
			return
		}

		p := &model.Prompt{
			UserID:      userID,
			Title:       req.Title,
			Content:     req.Content,
			Description: req.Description,
			Tags:        req.Tags,
			GroupID:     req.GroupID,
			GroupName:   req.GroupName,
			IsPublic:    false,
		}
		if p.Tags == nil {
			p.Tags = []string{}
		}

		if err := promptRepo.CreateContext(c.Request.Context(), p); err != nil {
			writePromptError(c, "create", err)
			return
		}

		c.JSON(http.StatusCreated, p)
	}
}

// ListPromptsHandler 获取用户自己的提示词
func ListPromptsHandler(promptRepo *repository.PromptRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		limit, offset, ok := parsePromptPagination(c)
		if !ok {
			return
		}

		prompts, err := promptRepo.ListByUser(userID, limit, offset)
		if err != nil {
			writePromptError(c, "list", err)
			return
		}
		if prompts == nil {
			prompts = []*model.Prompt{}
		}

		c.JSON(http.StatusOK, gin.H{"prompts": prompts, "total": len(prompts)})
	}
}

// ListPublicPromptsHandler 获取公开提示词
func ListPublicPromptsHandler(promptRepo *repository.PromptRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		limit, offset, ok := parsePromptPagination(c)
		if !ok {
			return
		}

		prompts, err := promptRepo.ListPublic(limit, offset)
		if err != nil {
			writePromptError(c, "list_public", err)
			return
		}
		if prompts == nil {
			prompts = []*model.Prompt{}
		}

		publicPrompts := make([]publicPromptResponse, 0, len(prompts))
		for _, prompt := range prompts {
			publicPrompts = append(publicPrompts, publicPromptResponse{
				ID:          prompt.ID,
				Title:       prompt.Title,
				Content:     prompt.Content,
				Description: prompt.Description,
				Tags:        prompt.Tags,
				GroupID:     prompt.GroupID,
				GroupName:   prompt.GroupName,
				IsPublic:    prompt.IsPublic,
				UseCount:    prompt.UseCount,
				CreatedAt:   prompt.CreatedAt,
				UpdatedAt:   prompt.UpdatedAt,
			})
		}

		c.JSON(http.StatusOK, gin.H{"prompts": publicPrompts, "total": len(publicPrompts)})
	}
}

// GetPromptHandler 获取单个提示词
func GetPromptHandler(promptRepo *repository.PromptRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		id, ok := promptID(c)
		if !ok {
			return
		}

		p, err := promptRepo.GetByID(id, userID)
		if err != nil {
			writePromptError(c, "load", err)
			return
		}

		if p.IsPublic {
			c.JSON(http.StatusOK, publicPromptResponse{
				ID:          p.ID,
				Title:       p.Title,
				Content:     p.Content,
				Description: p.Description,
				Tags:        p.Tags,
				GroupID:     p.GroupID,
				GroupName:   p.GroupName,
				IsPublic:    p.IsPublic,
				UseCount:    p.UseCount,
				CreatedAt:   p.CreatedAt,
				UpdatedAt:   p.UpdatedAt,
			})
			return
		}

		c.JSON(http.StatusOK, p)
	}
}

// UpdatePromptHandler 更新提示词
func UpdatePromptHandler(promptRepo *repository.PromptRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		id, ok := promptID(c)
		if !ok {
			return
		}

		p, err := promptRepo.GetByID(id, userID)
		if err != nil {
			writePromptError(c, "load", err)
			return
		}
		if p.UserID != userID || p.IsPublic {
			writePublicError(c, http.StatusForbidden, "prompt_read_only", "shared prompts are managed by administrators", false)
			return
		}

		var req updatePromptRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}
		if err := validateUpdatePromptRequest(&req); err != nil {
			writePromptValidationError(c, err)
			return
		}

		if req.Title != nil {
			p.Title = *req.Title
		}
		if req.Content != nil {
			p.Content = *req.Content
		}
		if req.Description != nil {
			p.Description = req.Description
		}
		if req.Tags != nil {
			p.Tags = req.Tags
		}
		if req.GroupID.Set {
			p.GroupID = req.GroupID.Value
		}
		if req.GroupName != nil {
			p.GroupName = *req.GroupName
		}
		if err := promptRepo.UpdateContext(c.Request.Context(), p); err != nil {
			writePromptError(c, "update", err)
			return
		}

		c.JSON(http.StatusOK, p)
	}
}

// DeletePromptHandler 删除提示词
func DeletePromptHandler(promptRepo *repository.PromptRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		id, ok := promptID(c)
		if !ok {
			return
		}

		p, err := promptRepo.GetByID(id, userID)
		if err != nil {
			writePromptError(c, "load", err)
			return
		}
		if p.UserID != userID || p.IsPublic {
			writePublicError(c, http.StatusForbidden, "prompt_read_only", "shared prompts are managed by administrators", false)
			return
		}

		if err := promptRepo.Delete(id, userID); err != nil {
			writePromptError(c, "delete", err)
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "prompt deleted"})
	}
}

func ListSharedPromptsHandler(promptRepo *repository.PromptRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		limit, offset, ok := parsePromptPagination(c)
		if !ok {
			return
		}

		prompts, err := promptRepo.ListShared(limit, offset)
		if err != nil {
			writePromptError(c, "list_shared", err)
			return
		}
		if prompts == nil {
			prompts = []*model.Prompt{}
		}

		c.JSON(http.StatusOK, gin.H{"prompts": prompts, "total": len(prompts)})
	}
}

func CreateSharedPromptHandler(promptRepo *repository.PromptRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		var req createPromptRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}
		if err := validateCreatePromptRequest(&req); err != nil {
			writePromptValidationError(c, err)
			return
		}
		p := &model.Prompt{
			UserID:      userID,
			Title:       req.Title,
			Content:     req.Content,
			Description: req.Description,
			Tags:        req.Tags,
			GroupName:   "默认分组",
			IsPublic:    true,
		}
		if p.Tags == nil {
			p.Tags = []string{}
		}
		if err := promptRepo.CreateContext(c.Request.Context(), p); err != nil {
			writePromptError(c, "create_shared", err)
			return
		}
		c.JSON(http.StatusCreated, p)
	}
}

func UpdateSharedPromptHandler(promptRepo *repository.PromptRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := promptID(c)
		if !ok {
			return
		}

		p, err := promptRepo.GetSharedByID(id)
		if err != nil {
			writePromptError(c, "load_shared", err)
			return
		}

		var req updatePromptRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}
		if err := validateUpdatePromptRequest(&req); err != nil {
			writePromptValidationError(c, err)
			return
		}

		if req.Title != nil {
			p.Title = *req.Title
		}
		if req.Content != nil {
			p.Content = *req.Content
		}
		if req.Description != nil {
			p.Description = req.Description
		}
		if req.Tags != nil {
			p.Tags = req.Tags
		}
		if err := promptRepo.UpdateShared(p); err != nil {
			writePromptError(c, "update_shared", err)
			return
		}

		c.JSON(http.StatusOK, p)
	}
}

func DeleteSharedPromptHandler(promptRepo *repository.PromptRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := promptID(c)
		if !ok {
			return
		}

		if err := promptRepo.DeleteShared(id); err != nil {
			writePromptError(c, "delete_shared", err)
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "prompt deleted"})
	}
}

func parsePagination(c *gin.Context) (int, int) {
	limit := 50
	offset := 0
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	if v := c.Query("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	return limit, offset
}
