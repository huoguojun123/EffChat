package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/agent"
	"github.com/huoguojun123/EffChat/pkg/logger"
)

type testModelRequest struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
}

const modelProbeSetupTimeout = 10 * time.Second

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
			writePublicError(c, http.StatusBadRequest, "model_probe_invalid", "model id and provider are required", false)
			return
		}
		if einoAgent == nil {
			writeServerError(c, http.StatusServiceUnavailable, "model_probe_unavailable", "model probe is unavailable", errors.New("model probe runtime is unavailable"))
			return
		}

		setupCtx, setupCancel := context.WithTimeout(c.Request.Context(), modelProbeSetupTimeout)
		prepared, err := einoAgent.PrepareModelProbe(setupCtx, &agent.ChatRequest{
			ModelID:  modelID,
			Provider: provider,
		})
		setupCancel()
		if err != nil {
			writeModelProbeFailure(c, modelID, provider, "setup", err)
			return
		}
		result, err := einoAgent.RunPreparedModelProbe(c.Request.Context(), prepared)
		if err != nil {
			writeModelProbeFailure(c, modelID, provider, "run", err)
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

func writeModelProbeFailure(c *gin.Context, modelID, provider, phase string, err error) {
	code := "model_probe_failed"
	message := "model probe failed; verify the model and channel configuration"
	retryable := true
	diagnostic := ""
	if phase == "setup" {
		code = "model_probe_setup_failed"
		message = "model probe setup failed; verify the model and channel configuration"
	} else {
		var runtimeErr *agent.RuntimeError
		if errors.As(err, &runtimeErr) {
			if runtimeErr.Code != "" {
				code = runtimeErr.Code
			}
			if runtimeErr.Message != "" {
				message = runtimeErr.Message
			}
			retryable = runtimeErr.Retryable
			diagnostic = runtimeErr.Diagnostic
		}
	}
	requestID := c.GetString("request_id")
	logErr := err
	if cause := errors.Unwrap(err); cause != nil {
		logErr = cause
	}
	logger.Error("model probe failed: request_id=%q model=%q provider=%q phase=%s code=%s err=%v", requestID, modelID, provider, phase, code, logErr)
	payload := gin.H{
		"ok":        false,
		"model_id":  modelID,
		"provider":  provider,
		"error":     message,
		"code":      code,
		"retryable": retryable,
	}
	if diagnostic != "" {
		payload["diagnostic"] = diagnostic
	}
	if requestID != "" {
		payload["request_id"] = requestID
	}
	c.JSON(http.StatusOK, payload)
}
