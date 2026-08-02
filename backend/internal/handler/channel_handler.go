package handler

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
	"github.com/huoguojun123/EffChat/internal/tool"
)

type externalServiceOrderInput struct {
	Kind string   `json:"kind" binding:"required"`
	Keys []string `json:"keys" binding:"required"`
}

func ListAIChannelsHandler(channelService *service.ChannelService) gin.HandlerFunc {
	return func(c *gin.Context) {
		channels, err := channelService.ListAIChannels(true)
		if err != nil {
			writeServerError(c, http.StatusInternalServerError, "channel_list_failed", "failed to list channels", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"channels": channels})
	}
}

func SaveAIChannelHandler(channelService *service.ChannelService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req service.AIChannelInput
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}
		channel, err := channelService.SaveAIChannel(&req)
		if err != nil {
			writeChannelError(c, "channel", "save", err)
			return
		}
		c.JSON(http.StatusOK, channel)
	}
}

func DeleteAIChannelHandler(channelService *service.ChannelService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := channelService.DeleteAIChannel(strings.TrimSpace(c.Param("key"))); err != nil {
			writeChannelError(c, "channel", "delete", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "channel deleted"})
	}
}

func ListExternalServicesHandler(channelService *service.ChannelService) gin.HandlerFunc {
	return func(c *gin.Context) {
		services, err := channelService.ListExternalServices(true)
		if err != nil {
			writeServerError(c, http.StatusInternalServerError, "external_service_list_failed", "failed to list external services", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"services": services})
	}
}

func SaveExternalServiceHandler(channelService *service.ChannelService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req service.ExternalServiceInput
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}
		item, err := channelService.SaveExternalServiceContext(c.Request.Context(), &req)
		if err != nil {
			writeChannelError(c, "external_service", "save", err)
			return
		}
		c.JSON(http.StatusOK, item)
	}
}

func DeleteExternalServiceHandler(channelService *service.ChannelService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := channelService.DeleteExternalService(strings.TrimSpace(c.Param("key"))); err != nil {
			writeChannelError(c, "external_service", "delete", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "external service deleted"})
	}
}

func ReorderExternalServicesHandler(channelService *service.ChannelService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req externalServiceOrderInput
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}
		services, err := channelService.ReorderExternalServices(req.Kind, req.Keys)
		if err != nil {
			writeChannelError(c, "external_service", "reorder", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"services": services})
	}
}

func TestExternalServiceHandler(channelService *service.ChannelService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req service.ExternalServiceInput
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}
		item, err := service.ValidateExternalService(&req)
		if err != nil {
			writeChannelError(c, "external_service", "validate", err)
			return
		}
		if item.Kind == service.ServiceKindOCR {
			writePublicError(c, http.StatusBadRequest, "external_service_probe_unsupported", "OCR 服务请通过上传文件验证", false)
			return
		}
		if strings.TrimSpace(item.APIKey) == "" {
			saved, lookupErr := channelService.GetExternalService(item.Key)
			if lookupErr != nil {
				writeServerError(c, http.StatusInternalServerError, "external_service_load_failed", "failed to load external service", lookupErr)
				return
			}
			if service.CanReuseExternalServiceCredential(saved, item) {
				item.APIKey = saved.APIKey
			}
		}
		probe := *item
		probe.Enabled = true
		runtime := service.BuildSearchRuntimeConfig([]*model.ExternalService{&probe})
		ctx, cancel := context.WithTimeout(c.Request.Context(), 12*time.Second)
		defer cancel()
		started := time.Now()
		if item.Kind == service.ServiceKindSearch {
			err = tool.ProbeWebSearchService(ctx, tool.WebSearchConfig{
				Provider: runtime.SearchProvider, Providers: runtime.SearchProviders, SearXNGURL: runtime.SearXNGURL,
				TavilyAPIKey: runtime.TavilySearchAPIKey, TavilyURL: runtime.TavilySearchURL,
				BraveAPIKey: runtime.BraveSearchAPIKey, BraveURL: runtime.BraveSearchURL,
				ExaAPIKey: runtime.ExaSearchAPIKey, ExaURL: runtime.ExaSearchURL,
				BochaAPIKey: runtime.BochaSearchAPIKey, BochaURL: runtime.BochaSearchURL,
				Timeout: 12 * time.Second,
			})
		} else {
			err = tool.ProbeWebExtractService(ctx, tool.WebExtractConfig{
				CrawlerImpl: runtime.CrawlerImpl, CrawlerProviders: runtime.CrawlerProviders,
				FirecrawlAPIKey: runtime.FirecrawlAPIKey, FirecrawlBaseURL: runtime.FirecrawlBaseURL,
				JinaAPIKey: runtime.JinaAPIKey, JinaBaseURL: runtime.JinaBaseURL,
				TavilyAPIKey: runtime.TavilyExtractAPIKey, TavilyBaseURL: runtime.TavilyExtractURL,
				ExaAPIKey: runtime.ExaExtractAPIKey, ExaBaseURL: runtime.ExaExtractURL,
				Timeout: 12 * time.Second,
			})
		}
		if err != nil {
			log.Printf("[external_service] probe_failed key=%s kind=%s err=%v", item.Key, item.Kind, err)
			writeServerError(c, http.StatusBadGateway, "external_service_probe_failed", "服务测试失败，请检查地址、Key 和网络连接", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "duration_ms": time.Since(started).Milliseconds()})
	}
}

func writeChannelError(c *gin.Context, resource, operation string, err error) {
	switch {
	case errors.Is(err, service.ErrChannelInvalid):
		// ErrChannelInvalid only wraps local field and ordering validation.
		writePublicError(c, http.StatusBadRequest, resource+"_invalid", err.Error(), false)
	case errors.Is(err, service.ErrChannelNotFound), errors.Is(err, repository.ErrNotFound):
		writePublicError(c, http.StatusNotFound, resource+"_not_found", strings.ReplaceAll(resource, "_", " ")+" not found", false)
	default:
		writeServerError(c, http.StatusInternalServerError, resource+"_"+operation+"_failed", "failed to "+operation+" "+strings.ReplaceAll(resource, "_", " "), err)
	}
}
