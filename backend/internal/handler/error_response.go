package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/pkg/logger"
)

func writeInvalidJSON(c *gin.Context) {
	writePublicError(c, http.StatusBadRequest, "invalid_request_body", "invalid request body", false)
}

func writePublicError(c *gin.Context, status int, code, message string, retryable bool) {
	payload := gin.H{"error": message, "retryable": retryable}
	if code != "" {
		payload["code"] = code
	}
	c.JSON(status, payload)
}

func writeServerError(c *gin.Context, status int, code, message string, err error) {
	requestID := c.GetString("request_id")
	logger.Error("request failed: request_id=%q method=%s path=%s status=%d code=%s err=%v", requestID, c.Request.Method, c.Request.URL.Path, status, code, err)
	payload := gin.H{"error": message, "retryable": true}
	if code != "" {
		payload["code"] = code
	}
	if requestID != "" {
		payload["request_id"] = requestID
	}
	c.JSON(status, payload)
}
