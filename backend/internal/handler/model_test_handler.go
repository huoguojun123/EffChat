package handler

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/effchat/internal/agent"
)

type testModelRequest struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
}

// TestModelHandler 执行管理员模型的最小连通性探测。
//
// 返回约定：
//   - 400：请求本身缺少 model/provider，前端应提示管理员补字段；
//   - 200 + ok=false：请求合法，但上游模型不可用或配置不通。
//
// 这样设计是为了让“检测失败”成为模型状态的一部分，而不是管理后台的全局异常。
func TestModelHandler(einoAgent *agent.EinoAgent) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req testModelRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}
		modelID := strings.TrimSpace(req.ID)
		provider := strings.TrimSpace(req.Provider)
		if modelID == "" || provider == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "model id and provider are required"})
			return
		}
		if einoAgent == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "model probe is unavailable", "code": "model_probe_unavailable"})
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()

		result, err := einoAgent.TestModel(ctx, &agent.ChatRequest{
			ModelID:  modelID,
			Provider: provider,
		})
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"ok":       false,
				"model_id": modelID,
				"provider": provider,
				"error":    truncateModelTestError(err.Error()),
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"ok":          true,
			"model_id":    modelID,
			"provider":    provider,
			"duration_ms": result.DurationMs,
			"output":      result.Output,
			"scope":       "minimal_chat_connectivity",
		})
	}
}

func truncateModelTestError(message string) string {
	const maxRunes = 500
	runes := []rune(strings.TrimSpace(message))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	return string(runes[:maxRunes]) + "..."
}
