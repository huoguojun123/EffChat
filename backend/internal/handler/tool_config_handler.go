package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/service"
)

func ListToolConfigsHandler(toolConfigService *service.ToolConfigService) gin.HandlerFunc {
	return func(c *gin.Context) {
		configs, err := toolConfigService.List()
		if err != nil {
			writeServerError(c, http.StatusInternalServerError, "tool_config_list_failed", "failed to list tool configs", err)
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
			writeToolConfigError(c, "save", err)
			return
		}
		c.JSON(http.StatusOK, item)
	}
}

func writeToolConfigError(c *gin.Context, operation string, err error) {
	if errors.Is(err, service.ErrToolConfigInvalid) {
		writePublicError(c, http.StatusBadRequest, "tool_config_invalid", err.Error(), false)
		return
	}
	writeServerError(c, http.StatusInternalServerError, "tool_config_"+operation+"_failed", "failed to "+operation+" tool config", err)
}
