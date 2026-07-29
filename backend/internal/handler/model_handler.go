package handler

import (
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/middleware"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
)

// ListModelsHandler 模型列表（登录用户可读，?enabled=true 仅返回启用项）。
//
// 可见性：管理员（role=admin）看全部；普通用户只看 enabled 且 min_group_level <= 自身组等级
// 的模型。无论 ?enabled 参数如何，普通用户都不会看到未启用或超出其组等级的模型。
func ListModelsHandler(modelService *service.ModelService, userRepo *repository.UserRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		if middleware.GetRole(c) == "admin" {
			onlyEnabled := c.Query("enabled") == "true"
			models, err := modelService.List(onlyEnabled)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list models", "code": "model_list_failed"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"models": models, "total": len(models)})
			return
		}

		// 普通用户：按组等级过滤。取不到组等级时降级为最低级 0。
		level := 0
		if lv, err := userRepo.GetGroupLevel(middleware.GetUserID(c)); err == nil {
			level = lv
		}
		models, err := modelService.ListVisible(level)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list models", "code": "model_list_failed"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"models": models, "total": len(models)})
	}
}

// ListAvailableModelsHandler 从当前渠道拉取可用模型列表。
//
// 不直接写入 DB：已有模型返回 DB 中的完整参数；未知模型按 provider 进行保守推断，
// 由管理员确认后再通过 CreateModelHandler 导入。
func ListAvailableModelsHandler(modelService *service.ModelService, channelService *service.ChannelService) gin.HandlerFunc {
	return func(c *gin.Context) {
		requestedChannel := strings.TrimSpace(c.Query("provider"))
		channelConfig, ok, err := channelModelListConfig(channelService, requestedChannel)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no model channel is configured"})
			return
		}

		upstreamModels, err := fetchChannelModels(c.Request.Context(), channelConfig)
		if err != nil {
			writeServerError(c, http.StatusBadGateway, "model_catalog_fetch_failed", "failed to fetch model catalog", err)
			return
		}
		enabledProviders := enabledModelProviders(channelService)

		existingModels, err := modelService.List(false)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list models", "code": "model_list_failed"})
			return
		}

		models := make([]*model.Model, 0, len(upstreamModels))
		for i, meta := range upstreamModels {
			if existing := matchExistingGatewayModel(meta, existingModels, channelConfig.channelKey, enabledProviders); existing != nil {
				models = append(models, existing)
				continue
			}
			models = append(models, inferModelForChannel(meta, i, channelConfig, enabledProviders))
		}
		sort.Slice(models, func(i, j int) bool {
			return models[i].Provider < models[j].Provider ||
				(models[i].Provider == models[j].Provider && models[i].ID < models[j].ID)
		})

		c.JSON(http.StatusOK, gin.H{
			"models": models,
			"total":  len(models),
		})
	}
}

// GetModelHandler 获取单个模型。普通用户只能读取自己可见的启用模型，避免通过详情接口枚举受限模型。
func GetModelHandler(modelService *service.ModelService, userRepo *repository.UserRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := modelIDParam(c)

		m, err := modelService.Get(id)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		if middleware.GetRole(c) != "admin" {
			level, err := userRepo.GetGroupLevel(middleware.GetUserID(c))
			if err != nil || !m.Enabled || m.MinGroupLevel > level {
				c.JSON(http.StatusNotFound, gin.H{"error": "model not found"})
				return
			}
		}

		c.JSON(http.StatusOK, m)
	}
}

// CreateModelHandler 新建模型（admin）
func CreateModelHandler(modelService *service.ModelService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req service.CreateModelRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}

		m, err := modelService.Create(&req)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusCreated, m)
	}
}

// UpdateModelHandler 更新模型（admin，部分字段）
func UpdateModelHandler(modelService *service.ModelService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := modelIDParam(c)

		var req service.UpdateModelRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}

		m, err := modelService.Update(id, &req)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, m)
	}
}

// DeleteModelHandler 删除模型（admin）
func DeleteModelHandler(modelService *service.ModelService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := modelIDParam(c)

		if err := modelService.Delete(id); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "model deleted"})
	}
}

func modelIDParam(c *gin.Context) string {
	return strings.TrimPrefix(c.Param("id"), "/")
}
