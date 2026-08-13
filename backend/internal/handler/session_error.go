package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/service"
)

func writeSessionLookupError(c *gin.Context, operation string, err error) {
	if errors.Is(err, service.ErrSessionNotFound) {
		writePublicError(c, http.StatusNotFound, "session_not_found", "session not found", false)
		return
	}
	writeServerError(c, http.StatusInternalServerError, "session_"+operation+"_failed", "failed to "+operation+" session", err)
}

// writeSessionMutationError is deliberately session-specific: validation and
// model availability are safe client-facing failures, while every unclassified
// repository or infrastructure error must retain its cause only in server logs.
func writeSessionMutationError(c *gin.Context, operation string, err error) {
	var modelErr *service.RuntimeModelError
	switch {
	case errors.As(err, &modelErr):
		status := http.StatusBadRequest
		if modelErr.Retryable {
			status = http.StatusServiceUnavailable
		}
		payload := gin.H{
			"error":     modelErr.Message,
			"code":      modelErr.Code,
			"retryable": modelErr.Retryable,
		}
		if modelErr.Provider != "" {
			payload["provider"] = modelErr.Provider
		}
		if modelErr.ModelID != "" {
			payload["model_id"] = modelErr.ModelID
		}
		c.JSON(status, payload)
	case errors.Is(err, service.ErrDefaultModelNotConfigured):
		writePublicError(c, http.StatusBadRequest, "default_model_not_configured", "default model is not configured", false)
	case errors.Is(err, service.ErrSessionInvalid):
		message := strings.TrimPrefix(err.Error(), service.ErrSessionInvalid.Error()+": ")
		writePublicError(c, http.StatusBadRequest, "session_invalid", message, false)
	case errors.Is(err, service.ErrSessionNotFound):
		writePublicError(c, http.StatusNotFound, "session_not_found", "session not found", false)
	default:
		writeServerError(c, http.StatusInternalServerError, "session_"+operation+"_failed", "failed to "+operation+" session", err)
	}
}

func writeMessageReadError(c *gin.Context, operation string, err error) {
	switch {
	case errors.Is(err, service.ErrSessionNotFound):
		writePublicError(c, http.StatusNotFound, "session_not_found", "session not found", false)
	case errors.Is(err, service.ErrConversationTurnNotFound):
		writePublicError(c, http.StatusNotFound, "conversation_turn_not_found", "conversation turn not found", false)
	default:
		writeServerError(c, http.StatusInternalServerError, "message_"+operation+"_failed", "failed to "+operation+" messages", err)
	}
}
