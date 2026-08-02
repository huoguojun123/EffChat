package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/pkg/logger"
)

type fileCleanupFailure struct {
	FileID    int64  `json:"file_id"`
	Code      string `json:"code"`
	Error     string `json:"error"`
	Retryable bool   `json:"retryable"`
}

func newFileCleanupFailure(fileID int64, code, message string) fileCleanupFailure {
	return fileCleanupFailure{FileID: fileID, Code: code, Error: message, Retryable: true}
}

func parseCleanupBoundedPositiveInt(c *gin.Context, key string, fallback, min, max int) (int, bool) {
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return fallback, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < min || value > max {
		writePublicError(c, http.StatusBadRequest, "file_cleanup_parameter_invalid", "invalid "+key, false)
		return 0, false
	}
	return value, true
}

// releaseOrCleanupFailure preserves the more important recovery failure. A
// cleanup operation may fail before finalize, but an unreleased claim also
// prevents immediate retry and therefore needs its own observable code.
func releaseOrCleanupFailure(c *gin.Context, fileRepo *repository.FileRepository, fileID int64, token, fallbackCode, fallbackMessage string) fileCleanupFailure {
	if err := fileRepo.ReleaseFileStorageCleanupClaim(c.Request.Context(), fileID, token); err != nil {
		logger.Error("failed to release file cleanup claim: file=%d err=%v", fileID, err)
		return newFileCleanupFailure(fileID, "file_cleanup_claim_release_failed", "failed to release file cleanup claim")
	}
	return newFileCleanupFailure(fileID, fallbackCode, fallbackMessage)
}
