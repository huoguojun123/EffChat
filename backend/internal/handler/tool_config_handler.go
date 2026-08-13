package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/middleware"
	"github.com/huoguojun123/EffChat/internal/repository"
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
		item, event, err := toolConfigService.Save(middleware.GetUserID(c), &req)
		if err != nil {
			writeToolConfigError(c, "save", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"tool": item, "event": event})
	}
}

func ListToolConfigHistoryHandler(toolConfigService *service.ToolConfigService) gin.HandlerFunc {
	return func(c *gin.Context) {
		events, err := toolConfigService.ListHistory(c.Param("key"))
		if err != nil {
			writeToolConfigError(c, "list_history", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"events": events})
	}
}

func RollbackToolConfigHandler(toolConfigService *service.ToolConfigService) gin.HandlerFunc {
	return func(c *gin.Context) {
		eventID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || eventID <= 0 {
			writePublicError(c, http.StatusBadRequest, "governance_event_invalid", "invalid governance event id", false)
			return
		}
		var req struct {
			Reason string `json:"reason"`
		}
		if c.Request.ContentLength > 0 {
			if err := c.ShouldBindJSON(&req); err != nil {
				writeInvalidJSON(c)
				return
			}
		}
		item, event, err := toolConfigService.Rollback(middleware.GetUserID(c), eventID, req.Reason)
		if err != nil {
			writeToolConfigError(c, "rollback", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"tool": item, "event": event})
	}
}

func writeToolConfigError(c *gin.Context, operation string, err error) {
	if errors.Is(err, service.ErrToolConfigInvalid) {
		writePublicError(c, http.StatusBadRequest, "tool_config_invalid", err.Error(), false)
		return
	}
	if errors.Is(err, repository.ErrNotFound) {
		writePublicError(c, http.StatusNotFound, "governance_event_not_found", "governance event not found", false)
		return
	}
	if errors.Is(err, repository.ErrGovernanceConflict) {
		writePublicError(c, http.StatusConflict, "governance_rollback_conflict", "resource changed after this event or the event was already rolled back", false)
		return
	}
	writeServerError(c, http.StatusInternalServerError, "tool_config_"+operation+"_failed", "failed to "+operation+" tool config", err)
}
