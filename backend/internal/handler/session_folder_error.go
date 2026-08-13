package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/service"
)

func writeSessionFolderError(c *gin.Context, operation string, err error) {
	switch {
	case errors.Is(err, service.ErrSessionFolderInvalid):
		message := strings.TrimPrefix(err.Error(), service.ErrSessionFolderInvalid.Error()+": ")
		writePublicError(c, http.StatusBadRequest, "session_folder_invalid", message, false)
	case errors.Is(err, service.ErrSessionFolderNotFound):
		writePublicError(c, http.StatusNotFound, "session_folder_not_found", "session folder not found", false)
	default:
		writeServerError(c, http.StatusInternalServerError, "session_folder_"+operation+"_failed", "failed to "+operation+" session folder", err)
	}
}
