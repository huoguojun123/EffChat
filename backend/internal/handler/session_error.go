package handler

import (
	"errors"
	"net/http"

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
