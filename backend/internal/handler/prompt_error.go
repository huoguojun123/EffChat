package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/repository"
)

const (
	promptTitleMaxRunes     = 200
	promptGroupNameMaxRunes = 100
)

func writePromptError(c *gin.Context, operation string, err error) {
	if errors.Is(err, repository.ErrNotFound) {
		writePublicError(c, http.StatusNotFound, "prompt_not_found", "prompt not found", false)
		return
	}
	message := "failed to " + strings.ReplaceAll(operation, "_", " ") + " prompt"
	switch operation {
	case "list":
		message = "failed to list prompts"
	case "list_public":
		message = "failed to list public prompts"
	case "list_shared":
		message = "failed to list shared prompts"
	case "load", "load_shared":
		message = "failed to load prompt"
	}
	writeServerError(c, http.StatusInternalServerError, "prompt_"+operation+"_failed", message, err)
}

func writePromptValidationError(c *gin.Context, err error) {
	writePublicError(c, http.StatusBadRequest, "prompt_invalid", err.Error(), false)
}

func promptID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		writePublicError(c, http.StatusBadRequest, "prompt_id_invalid", "invalid prompt id", false)
		return 0, false
	}
	return id, true
}

func parsePromptPagination(c *gin.Context) (int, int, bool) {
	limit, offset := 50, 0
	if raw := c.Query("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 || value > 100 {
			writePublicError(c, http.StatusBadRequest, "prompt_pagination_invalid", "limit must be between 1 and 100", false)
			return 0, 0, false
		}
		limit = value
	}
	if raw := c.Query("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			writePublicError(c, http.StatusBadRequest, "prompt_pagination_invalid", "offset must be zero or greater", false)
			return 0, 0, false
		}
		offset = value
	}
	return limit, offset, true
}

func validateCreatePromptRequest(req *createPromptRequest) error {
	if err := validatePromptText(req.Title, req.Content); err != nil {
		return err
	}
	if req.GroupID != nil && *req.GroupID <= 0 {
		return fmt.Errorf("group_id must be a positive integer or null")
	}
	return validatePromptGroupName(req.GroupName)
}

func validateUpdatePromptRequest(req *updatePromptRequest) error {
	if req.Title != nil {
		if strings.TrimSpace(*req.Title) == "" {
			return fmt.Errorf("title is required")
		}
		if utf8.RuneCountInString(*req.Title) > promptTitleMaxRunes {
			return fmt.Errorf("title must be at most %d characters", promptTitleMaxRunes)
		}
	}
	if req.Content != nil && strings.TrimSpace(*req.Content) == "" {
		return fmt.Errorf("content is required")
	}
	if req.GroupID.Set && req.GroupID.Value != nil && *req.GroupID.Value <= 0 {
		return fmt.Errorf("group_id must be a positive integer or null")
	}
	if req.GroupName != nil {
		return validatePromptGroupName(*req.GroupName)
	}
	return nil
}

func validatePromptText(title, content string) error {
	if strings.TrimSpace(title) == "" {
		return fmt.Errorf("title is required")
	}
	if utf8.RuneCountInString(title) > promptTitleMaxRunes {
		return fmt.Errorf("title must be at most %d characters", promptTitleMaxRunes)
	}
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("content is required")
	}
	return nil
}

func validatePromptGroupName(groupName string) error {
	if utf8.RuneCountInString(groupName) > promptGroupNameMaxRunes {
		return fmt.Errorf("group_name must be at most %d characters", promptGroupNameMaxRunes)
	}
	return nil
}
