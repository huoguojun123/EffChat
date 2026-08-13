package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/repository"
)

func writePromptGroupError(c *gin.Context, operation string, err error) {
	switch {
	case errors.Is(err, repository.ErrPromptGroupInvalid):
		message := strings.TrimPrefix(err.Error(), repository.ErrPromptGroupInvalid.Error()+": ")
		writePublicError(c, http.StatusBadRequest, "prompt_group_invalid", message, false)
	case errors.Is(err, repository.ErrNotFound):
		writePublicError(c, http.StatusNotFound, "prompt_group_not_found", "prompt group not found", false)
	case errors.Is(err, repository.ErrPromptGroupConflict):
		writePublicError(c, http.StatusConflict, "prompt_group_conflict", "prompt group name already exists", false)
	default:
		writeServerError(c, http.StatusInternalServerError, "prompt_group_"+operation+"_failed", "failed to "+operation+" prompt group", err)
	}
}
