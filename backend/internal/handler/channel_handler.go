package handler

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/effchat/internal/model"
	"github.com/huoguojun123/effchat/internal/service"
	"github.com/huoguojun123/effchat/internal/tool"
)

type externalServiceOrderInput struct {
	Kind string   `json:"kind" binding:"required"`
	Keys []string `json:"keys" binding:"required"`
}

func ListAIChannelsHandler(channelService *service.ChannelService) gin.HandlerFunc {
	return func(c *gin.Context) {
		channels, err := channelService.ListAIChannels(true)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list channels"})
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
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, channel)
	}
}

func DeleteAIChannelHandler(channelService *service.ChannelService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := channelService.DeleteAIChannel(strings.TrimSpace(c.Param("key"))); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "channel deleted"})
	}
}

func ListExternalServicesHandler(channelService *service.ChannelService) gin.HandlerFunc {
	return func(c *gin.Context) {
		services, err := channelService.ListExternalServices(true)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list external services"})
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
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, item)
	}
}

func DeleteExternalServiceHandler(channelService *service.ChannelService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := channelService.DeleteExternalService(strings.TrimSpace(c.Param("key"))); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if item.Kind == service.ServiceKindOCR {
			c.JSON(http.StatusBadRequest, gin.H{"error": "OCR 服务请通过上传文件验证"})
			return
		}
		if strings.TrimSpace(item.APIKey) == "" {
			if saved, lookupErr := channelService.GetExternalService(item.Key); lookupErr == nil && service.CanReuseExternalServiceCredential(saved, item) {
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
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "服务测试失败，请检查地址、Key 和网络连接"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "duration_ms": time.Since(started).Milliseconds()})
	}
}
