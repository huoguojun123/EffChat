package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
)

func writeRunError(c *gin.Context, operation string, err error) {
	if errors.Is(err, repository.ErrNotFound) || errors.Is(err, service.ErrRunNotFound) {
		writePublicError(c, http.StatusNotFound, "run_not_found", "run not found", false)
		return
	}
	message := "failed to " + operation + " run"
	if operation == "status" {
		message = "failed to read run status"
	}
	writeServerError(c, http.StatusInternalServerError, "run_"+operation+"_failed", message, err)
}

func runSessionID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		writePublicError(c, http.StatusBadRequest, "session_id_invalid", "invalid session id", false)
		return 0, false
	}
	return id, true
}

func runID(c *gin.Context) (string, bool) {
	id := strings.TrimSpace(c.Param("run_id"))
	if id == "" {
		writePublicError(c, http.StatusBadRequest, "run_id_invalid", "invalid run id", false)
		return "", false
	}
	return id, true
}

func runCursor(c *gin.Context) (int64, bool) {
	raw := c.Query("cursor")
	if raw == "" {
		return 0, true
	}
	cursor, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || cursor < 0 {
		writePublicError(c, http.StatusBadRequest, "run_cursor_invalid", "invalid run cursor", false)
		return 0, false
	}
	return cursor, true
}
