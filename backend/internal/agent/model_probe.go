package agent

import (
	"context"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/huoguojun123/EffChat/internal/modelbank"
)

// ModelProbeResult 是后台“模型连通性探测”的最小结果。
// 这里只关心模型能否完成一次最短文本回复，不验证工具、多模态、搜索、思考参数或长上下文能力。
type ModelProbeResult struct {
	Output     string
	DurationMs int64
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
	if req == nil {
		req = &ChatRequest{}
	}
	req.SkipUsage = true
	// 可用性检测只验证“provider + baseURL + key + model_id 能否完成最小文本回复”。
	// 不下发 thinking 字段，避免 Claude manual budget 等格式在检测时放大 max_tokens，
	// 也避免把“思考预算配置是否正确”混进最基础的连通性判断。
	req.Reasoning = false
	req.ThinkingFormat = string(modelbank.ThinkingFormatNone)
	req.ThinkingEffort = ""
	if req.MaxTokens <= 0 {
		req.MaxTokens = 16
	}

	startedAt := time.Now()
	chatModel, err := a.buildChatModel(ctx, req, modelbank.SearchDecision{})
	if err != nil {
		return nil, err
	}
	resp, err := chatModel.Generate(ctx, []*schema.Message{
		schema.UserMessage("Reply with exactly: OK"),
	})
	if err != nil {
		return nil, sanitizeModelRuntimeError(req.Provider, req.ModelID, err)
	}
	output := ""
	if resp != nil {
		output = strings.TrimSpace(resp.Content)
	}
	return &ModelProbeResult{
		Output:     truncateProbeOutput(output),
		DurationMs: time.Since(startedAt).Milliseconds(),
	}, nil
}

func truncateProbeOutput(output string) string {
	const maxRunes = 200
	runes := []rune(strings.TrimSpace(output))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	return string(runes[:maxRunes]) + "..."
}
