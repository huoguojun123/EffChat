package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/cloudwego/eino/schema"
)

type RuntimeError struct {
	Code         string               `json:"code"`
	Message      string               `json:"error"`
	Diagnostic   string               `json:"diagnostic,omitempty"`
	Category     RuntimeErrorCategory `json:"category"`
	Retryable    bool                 `json:"retryable"`
	Provider     string               `json:"-"`
	ModelID      string               `json:"-"`
	FinishReason string               `json:"finish_reason,omitempty"`
	Usage        *Usage               `json:"usage,omitempty"`
	cause        error
}

type RuntimeErrorCategory string

const (
	RuntimeErrorTransient     RuntimeErrorCategory = "transient"
	RuntimeErrorConfiguration RuntimeErrorCategory = "configuration"
	RuntimeErrorAccess        RuntimeErrorCategory = "access"
	RuntimeErrorContext       RuntimeErrorCategory = "context"
	RuntimeErrorTool          RuntimeErrorCategory = "tool"
	RuntimeErrorPersistence   RuntimeErrorCategory = "persistence"
	RuntimeErrorConnection    RuntimeErrorCategory = "connection"
	RuntimeErrorServerUpdate  RuntimeErrorCategory = "server_update"
)

func (e *RuntimeError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *RuntimeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func sanitizeModelRuntimeError(provider, modelID string, err error) error {
	var runtimeErr *RuntimeError
	if errors.As(err, &runtimeErr) {
		return runtimeErr
	}
	classification := classifyModelRuntimeError(err)
	return &RuntimeError{
		Code:       classification.Code,
		Message:    classification.Message,
		Diagnostic: classification.Diagnostic,
		Category:   classification.Category,
		Retryable:  classification.Retryable,
		Provider:   provider,
		ModelID:    modelID,
		cause:      err,
	}
}

func newModelEmptyResponseError(provider, modelID, finishReason string, usage *Usage) error {
	message := "模型这次没有返回可展示内容，请切换模型或稍后重试"
	if strings.TrimSpace(finishReason) != "" {
		message = fmt.Sprintf("%s（finish_reason=%s）", message, finishReason)
	}
	return &RuntimeError{
		Code:         "model_empty_response",
		Message:      message,
		Category:     RuntimeErrorTransient,
		Retryable:    true,
		Provider:     provider,
		ModelID:      modelID,
		FinishReason: finishReason,
		Usage:        usage,
	}
}

func attachRuntimeMeta(messages []map[string]interface{}, durationMs int64, tokensPerSecond float64) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i]["role"] == "assistant" {
			messages[i]["runtime"] = map[string]interface{}{
				"duration_ms":       durationMs,
				"tokens_per_second": tokensPerSecond,
			}
			return
		}
	}
}

func hasDisplayableAssistantOutput(messages []map[string]interface{}) bool {
	for _, message := range messages {
		if message["role"] != "assistant" {
			continue
		}
		if textFieldHasContent(message["content"]) {
			return true
		}
		if messageHasToolCalls(message) {
			return true
		}
	}
	return false
}

const reasoningOnlyAssistantFallback = "模型这次只返回了思考过程，没有给出正式回答。请重试一次；如果反复出现，可以在模型配置里关闭或降低该模型的思考模式。"

func materializeReasoningOnlyAssistantOutput(messages []map[string]interface{}) (string, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if message["role"] != "assistant" {
			continue
		}
		if textFieldHasContent(message["content"]) || messageHasToolCalls(message) {
			continue
		}
		if !assistantHasReasoning(message) {
			continue
		}
		message["content"] = reasoningOnlyAssistantFallback
		return reasoningOnlyAssistantFallback, true
	}
	return "", false
}

func assistantHasReasoning(message map[string]interface{}) bool {
	if textFieldHasContent(message["reasoning_content"]) || textFieldHasContent(message["reasoning-content"]) {
		return true
	}
	extra, ok := message["extra"].(map[string]interface{})
	if !ok {
		return false
	}
	return textFieldHasContent(extra["reasoning_content"]) || textFieldHasContent(extra["reasoning-content"])
}

func messageHasToolCalls(message map[string]interface{}) bool {
	toolCalls, ok := message["tool_calls"].([]interface{})
	return ok && len(toolCalls) > 0
}

func textFieldHasContent(value interface{}) bool {
	text, ok := value.(string)
	return ok && strings.TrimSpace(text) != ""
}

func usageFromTokenUsage(u *schema.TokenUsage) *Usage {
	if u == nil {
		return nil
	}
	return &Usage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
		CachedTokens:     u.PromptTokenDetails.CachedTokens,
		ReasoningTokens:  u.CompletionTokensDetails.ReasoningTokens,
	}
}

// unknownToolHandler 处理模型对"未挂载工具"的调用（eino UnknownToolsHandler 钩子）。
// 触发场景：跨轮切换搜索/记忆开关后，历史里残留的 web_search/web_extract 等调用被
// 重放；或模型凭训练习惯臆造了一个并不存在的工具名。默认会直接返回 error 中断整条
// 流式响应，体验上表现为"一搜就报错"。这里改为返回一条普通工具结果，让模型知道该
// 工具当前不可用并据此继续作答，与项目既有的搜索优雅降级保持一致。
func unknownToolHandler(_ context.Context, name, _ string) (string, error) {
	log.Printf("[eino] 模型调用了未挂载的工具 %q，已优雅降级（提示模型该工具不可用）", name)
	data, err := json.Marshal(map[string]interface{}{
		"ok":         false,
		"tool":       name,
		"error_code": "tool_unavailable",
		"error":      fmt.Sprintf("Tool %q is not available in this turn.", name),
		"message":    "Do not call this tool again. Continue with enabled capabilities and existing context.",
	})
	if err != nil {
		return `{"ok":false,"error_code":"tool_unavailable"}`, nil
	}
	return string(data), nil
}
