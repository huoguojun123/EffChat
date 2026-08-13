package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/repository"
)

// writeFileLookupError keeps missing and unauthorized user-scoped files
// indistinguishable while preserving repository failures for the retryable
// request-ID path. Callers must not turn an unknown lookup failure into a 404,
// because that hides storage outages and prevents operators from tracing them.
func writeFileLookupError(c *gin.Context, err error) {
	if errors.Is(err, repository.ErrNotFound) {
		writePublicError(c, http.StatusNotFound, "file_not_found", "file not found", false)
		return
	}
	writeServerError(c, http.StatusInternalServerError, "file_load_failed", "failed to load file", err)
}
