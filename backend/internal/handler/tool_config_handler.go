package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/service"
)

func ListToolConfigsHandler(toolConfigService *service.ToolConfigService) gin.HandlerFunc {
	return func(c *gin.Context) {
		configs, err := toolConfigService.List()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list tool configs"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"tools": configs})
	}
}

func SaveToolConfigHandler(toolConfigService *service.ToolConfigService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req service.ToolConfigInput
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}
		item, err := toolConfigService.Save(&req)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, item)
	}
}
