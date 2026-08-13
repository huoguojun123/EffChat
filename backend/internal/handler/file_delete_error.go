package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/repository"
)

func writeFileDeletionError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		writePublicError(c, http.StatusNotFound, "file_not_found", "file not found", false)
	case errors.Is(err, repository.ErrAttachmentUnavailable):
		writePublicError(c, http.StatusConflict, "file_unavailable", "file is unavailable", false)
	default:
		writeServerError(c, http.StatusInternalServerError, "file_delete_failed", "failed to delete file", err)
	}
}
