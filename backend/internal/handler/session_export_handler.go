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
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session id"})
			return
		}
		includeTools := false
		if raw := c.Query("include_tools"); raw != "" {
			includeTools, err = strconv.ParseBool(raw)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "include_tools must be true or false"})
				return
			}
		}

		export, err := messageService.ExportSessionMarkdown(c.Request.Context(), sessionID, middleware.GetUserID(c), includeTools, time.Now())
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.Header("Content-Type", "text/markdown; charset=utf-8")
		c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": export.Filename}))
		c.Data(http.StatusOK, "text/markdown; charset=utf-8", export.Content)
	}
}
