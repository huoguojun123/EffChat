package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/pkg/logger"
)

func writeInvalidJSON(c *gin.Context) {
	c.JSON(http.StatusBadRequest, gin.H{
		"error": "invalid request body",
		"code":  "invalid_request_body",
	})
}

func writeServerError(c *gin.Context, status int, code, message string, err error) {
	requestID := c.GetString("request_id")
	logger.Error("request failed: request_id=%q method=%s path=%s status=%d code=%s err=%v", requestID, c.Request.Method, c.Request.URL.Path, status, code, err)
	payload := gin.H{"error": message}
	if code != "" {
		payload["code"] = code
	}
	if requestID != "" {
		payload["request_id"] = requestID
	}
	c.JSON(status, payload)
}
