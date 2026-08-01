package handler

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/repository"
)

func writeOCRRetryMutationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		writePublicError(c, http.StatusNotFound, "file_not_found", "file not found", false)
	case errors.Is(err, repository.ErrOCRSourceUnavailable):
		writeOCRSourceUnavailable(c)
	default:
		writeServerError(c, http.StatusInternalServerError, "ocr_retry_failed", "failed to restart OCR", err)
	}
}

func writeOCRSourceUnavailable(c *gin.Context) {
	writePublicError(c, http.StatusConflict, "ocr_source_unavailable", "OCR 原文件已过期或不存在，无法重试", false)
}

// markOCRSourceUnavailable closes the state transition that RestartOCR already
// committed before the filesystem recheck. A failed compensation is an internal
// persistence failure, not a user-fixable missing-source conflict.
func markOCRSourceUnavailable(fileRepo *repository.FileRepository, fileID, userID int64, cause error) error {
	if err := fileRepo.FailOCR(fileID, userID, "ocr_source_missing", "OCR 原文件不存在，无法继续解析"); err != nil {
		return fmt.Errorf("mark OCR source unavailable after %v: %w", cause, err)
	}
	return nil
}
