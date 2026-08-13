package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
	"github.com/huoguojun123/EffChat/pkg/config"
)

// ListConfigHandler 获取所有系统配置
func ListConfigHandler(configRepo *repository.ConfigRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		items, err := configRepo.ListAdminEditable()
		if err != nil {
			writeServerError(c, http.StatusInternalServerError, "config_list_failed", "failed to list config", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"config": items})
	}
}

// SystemInfoHandler 公开端点：仅下发前端首屏需要的白名单字段，
// 不暴露其他 admin 配置。供 sidebar、标签页和版本展示消费。
func SystemInfoHandler(configRepo *repository.ConfigRepository, fontRepo *repository.FontRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		var chatFont *model.FontAsset
		var chatFonts repository.ChatFonts
		if fontRepo != nil {
			if font, err := fontRepo.GetSelected(); err == nil && font != nil {
				attachFontURL(font)
				chatFont = font
			}
			if fonts, err := fontRepo.GetSelectedFonts(); err == nil {
				attachFontURL(fonts.Chinese)
				attachFontURL(fonts.Latin)
				attachFontURL(fonts.Code)
				chatFonts = fonts
			}
		}
		c.JSON(http.StatusOK, gin.H{
			"system_name": configRepo.GetString("system_name", "EffChat"),
			"version":     config.AppVersion,
			"chat_font":   chatFont,
			"chat_fonts":  chatFonts,
		})
	}
}

// UpdateConfigHandler 更新单个配置项
func UpdateConfigHandler(configRepo *repository.ConfigRepository, modelServices ...*service.ModelService) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.Param("key")

		var req struct {
			Value json.RawMessage `json:"value" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}
		if err := validateDefaultModelConfig(key, req.Value, modelServices...); err != nil {
			writeConfigError(c, "update", err)
			return
		}

		if err := configRepo.UpdateAdminEditable(key, req.Value); err != nil {
			writeConfigError(c, "update", err)
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "config updated", "key": key})
	}
}

func UpdateConfigBatchHandler(configRepo *repository.ConfigRepository, modelServices ...*service.ModelService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Updates map[string]json.RawMessage `json:"updates" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || len(req.Updates) == 0 {
			writeInvalidJSON(c)
			return
		}
		for key, value := range req.Updates {
			if err := validateDefaultModelConfig(key, value, modelServices...); err != nil {
				writeConfigError(c, "update", err)
				return
			}
		}
		if err := configRepo.UpdateAdminEditableBatchContext(c.Request.Context(), req.Updates); err != nil {
			writeConfigError(c, "update", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "config updated", "updated": len(req.Updates)})
	}
}

func validateDefaultModelConfig(key string, value json.RawMessage, modelServices ...*service.ModelService) error {
	if key != "default_model_id" {
		return nil
	}
	var modelID string
	if err := json.Unmarshal(value, &modelID); err != nil {
		return fmt.Errorf("%w: default_model_id must be a valid model id", repository.ErrConfigInvalid)
	}
	if len(modelServices) == 0 || modelServices[0] == nil {
		return fmt.Errorf("default model validation is unavailable")
	}
	if err := modelServices[0].ValidateDefaultModel(modelID); err != nil {
		return err
	}
	return nil
}

func writeConfigError(c *gin.Context, operation string, err error) {
	switch {
	case errors.Is(err, repository.ErrConfigInvalid), errors.Is(err, service.ErrModelInvalid):
		writePublicError(c, http.StatusBadRequest, "config_invalid", err.Error(), false)
	default:
		writeServerError(c, http.StatusInternalServerError, "config_"+operation+"_failed", "failed to "+operation+" config", err)
	}
}
