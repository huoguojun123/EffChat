package handler

import (
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/middleware"
	"github.com/huoguojun123/EffChat/internal/service"
)

func ExportSessionMarkdownHandler(messageService *service.MessageService) gin.HandlerFunc {
	return func(c *gin.Context) {
		sessionID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || sessionID <= 0 {
			writePublicError(c, http.StatusBadRequest, "session_id_invalid", "invalid session id", false)
			return
		}
		includeTools := false
		if raw := c.Query("include_tools"); raw != "" {
			includeTools, err = strconv.ParseBool(raw)
			if err != nil {
				writePublicError(c, http.StatusBadRequest, "session_export_options_invalid", "include_tools must be true or false", false)
				return
			}
		}

		export, err := messageService.ExportSessionMarkdown(c.Request.Context(), sessionID, middleware.GetUserID(c), includeTools, time.Now())
		if err != nil {
			writeMessageReadError(c, "export", err)
			return
		}
		c.Header("Content-Type", "text/markdown; charset=utf-8")
		c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": export.Filename}))
		c.Data(http.StatusOK, "text/markdown; charset=utf-8", export.Content)
	}
}
