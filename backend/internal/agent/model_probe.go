package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/modelstream"
)

const (
	modelProbeFirstOutputTimeout = 30 * time.Second
	modelProbeMaxOutputTokens    = 16
	modelProbeExpectedOutput     = "OK"
)

// ModelProbeResult 是后台“模型连通性探测”的最小结果。
// 这里只关心模型能否完成一次最短文本回复，不验证工具、多模态、搜索、思考参数或长上下文能力。
type ModelProbeResult struct {
	Output     string
	Matched    bool
	DurationMs int64
}

// PreparedModelProbe carries a fully resolved provider across the bounded
// setup boundary without retaining the setup context.
type PreparedModelProbe struct {
	chatModel einoModel.ToolCallingChatModel
	request   *ChatRequest
	startedAt time.Time
}

// TestModel 复用正式对话的 provider 构造链路，对指定模型发起一次极短连通性探测。
//
// 为什么不在 handler 里手写 HTTP 请求：
//   - 本项目同时支持 OpenAI 兼容、Anthropic 原生、Gemini 原生等不同协议；
//   - 正式对话已经把 baseURL、API key、thinking_format 等差异收敛在 buildChatModel；
//   - 探测复用同一条链路，才能检测“聊天实际会不会通”，而不是检测另一套临时代码。
//
// 探测不是用户业务对话，因此 SkipUsage=true，避免调试配置污染模型用量统计。
func (a *EinoAgent) TestModel(ctx context.Context, req *ChatRequest) (*ModelProbeResult, error) {
	prepared, err := a.PrepareModelProbe(ctx, req)
	if err != nil {
		return nil, err
	}
	return a.RunPreparedModelProbe(ctx, prepared)
}

// PrepareModelProbe resolves the provider under a short control-plane context.
// The caller may cancel setupCtx immediately after this method succeeds.
func (a *EinoAgent) PrepareModelProbe(setupCtx context.Context, req *ChatRequest) (*PreparedModelProbe, error) {
	if setupCtx == nil {
		return nil, fmt.Errorf("model probe setup context is required")
	}
	if req == nil {
		req = &ChatRequest{}
	}
	req = taskModelRequest(req, modelProbeMaxOutputTokens)
	req.SkipUsage = true
	// 可用性检测只验证“provider + baseURL + key + model_id 能否完成最小文本回复”。
	// 不下发 thinking 字段，避免 Claude manual budget 等格式在检测时放大 max_tokens，
	// 也避免把“思考预算配置是否正确”混进最基础的连通性判断。
	req.Reasoning = false
	req.ThinkingFormat = string(modelbank.ThinkingFormatNone)
	req.ThinkingEffort = ""
	startedAt := time.Now()
	chatModel, err := a.buildChatModel(setupCtx, req, modelbank.SearchDecision{})
	if err != nil {
		return nil, err
	}
	return &PreparedModelProbe{
		chatModel: chatModel,
		request:   req,
		startedAt: startedAt,
	}, nil
}

// RunPreparedModelProbe owns the provider stream with the request context.
// Its fixed timeout waits only for meaningful output; once output starts the
// stream is drained to EOF unless the request is canceled or the provider fails.
func (a *EinoAgent) RunPreparedModelProbe(runCtx context.Context, prepared *PreparedModelProbe) (*ModelProbeResult, error) {
	if runCtx == nil {
		return nil, fmt.Errorf("model probe run context is required")
	}
	if prepared == nil || prepared.chatModel == nil || prepared.request == nil {
		return nil, fmt.Errorf("prepared model probe is required")
	}
	resp, err := modelstream.Collect(runCtx, prepared.chatModel, []*schema.Message{
		schema.UserMessage("Reply with exactly: OK"),
	}, modelProbeFirstOutputTimeout)
	if err != nil {
		return nil, sanitizeModelRuntimeError(prepared.request.Provider, prepared.request.ModelID, err)
	}
	output, matched := classifyModelProbeOutput(resp)
	return &ModelProbeResult{
		Output:     truncateProbeOutput(output),
		Matched:    matched,
		DurationMs: time.Since(prepared.startedAt).Milliseconds(),
	}, nil
}

// classifyModelProbeOutput keeps transport success separate from probe success.
// The prompt asks for one exact sentinel, so an empty/nil response or any extra
// text proves only that the provider returned a stream, not that the configured
// model followed the minimal request contract.
func classifyModelProbeOutput(resp *schema.Message) (string, bool) {
	if resp == nil {
		return "", false
	}
	output := strings.TrimSpace(resp.Content)
	return output, output == modelProbeExpectedOutput
}

func truncateProbeOutput(output string) string {
	const maxRunes = 200
	runes := []rune(strings.TrimSpace(output))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	return string(runes[:maxRunes]) + "..."
}
