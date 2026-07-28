package agent

import (
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"
)

const maxRawFinishReasonRunes = 64

type FinishReason string

const (
	FinishReasonStop             FinishReason = "stop"
	FinishReasonOutputLimit      FinishReason = "output_limit"
	FinishReasonSafety           FinishReason = "safety"
	FinishReasonMalformedTool    FinishReason = "malformed_tool"
	FinishReasonToolContinuation FinishReason = "tool_continuation"
	FinishReasonUnknown          FinishReason = "unknown"
)

type normalizedFinishReason struct {
	Canonical FinishReason
	Raw       string
}

func normalizeProviderFinishReason(raw string, hasToolCalls bool) normalizedFinishReason {
	boundedRaw := boundedFinishReason(raw)
	value := strings.NewReplacer("-", "_", " ", "_").Replace(strings.ToLower(strings.TrimSpace(raw)))
	var canonical FinishReason
	switch value {
	case "stop", "end_turn", "stop_sequence", "complete", "completed":
		canonical = FinishReasonStop
	case "length", "max_tokens", "max_output_tokens", "token_limit", "model_context_window_exceeded":
		canonical = FinishReasonOutputLimit
	case "content_filter", "safety", "blocked", "refusal", "recitation", "language", "blocklist",
		"prohibited_content", "spii", "image_safety", "image_prohibited_content", "no_image", "sensitive":
		canonical = FinishReasonSafety
	case "malformed_function_call", "unexpected_tool_call", "invalid_tool_arguments", "malformed_tool":
		canonical = FinishReasonMalformedTool
	case "tool_calls", "tool_use", "function_call", "pause_turn":
		canonical = FinishReasonToolContinuation
	default:
		canonical = FinishReasonUnknown
	}
	if hasToolCalls && (canonical == FinishReasonStop || canonical == FinishReasonToolContinuation || canonical == FinishReasonUnknown) {
		canonical = FinishReasonToolContinuation
	}
	return normalizedFinishReason{Canonical: canonical, Raw: boundedRaw}
}

func boundedFinishReason(raw string) string {
	runes := []rune(strings.TrimSpace(raw))
	if len(runes) > maxRawFinishReasonRunes {
		runes = runes[:maxRawFinishReasonRunes]
	}
	return string(runes)
}

func applyNormalizedFinishReason(data map[string]interface{}, normalized normalizedFinishReason) {
	if data == nil {
		return
	}
	meta, _ := data["response_meta"].(map[string]interface{})
	if meta == nil {
		meta = map[string]interface{}{}
		data["response_meta"] = meta
	}
	meta["finish_reason"] = string(normalized.Canonical)
	if normalized.Raw != "" {
		meta["raw_finish_reason"] = normalized.Raw
	}
}

func completionIsIncomplete(reason FinishReason) bool {
	return reason == FinishReasonOutputLimit
}

func validateCompactionCompletion(provider, modelID string, result *schema.Message) error {
	raw := ""
	hasToolCalls := false
	if result != nil {
		hasToolCalls = len(result.ToolCalls) > 0
		if result.ResponseMeta != nil {
			raw = result.ResponseMeta.FinishReason
		}
	}
	normalized := normalizeProviderFinishReason(raw, hasToolCalls)
	if normalized.Canonical == FinishReasonStop {
		return nil
	}

	category := RuntimeErrorServerUpdate
	retryable := true
	message := "压缩摘要未自然完成，未保存本次结果，请重试"
	switch normalized.Canonical {
	case FinishReasonOutputLimit:
		category = RuntimeErrorContext
		message = "压缩摘要达到输出上限，未保存不完整摘要，请重试"
	case FinishReasonSafety:
		category = RuntimeErrorAccess
		retryable = false
		message = "压缩摘要被模型安全策略终止，未保存本次结果"
	case FinishReasonMalformedTool:
		category = RuntimeErrorConfiguration
		retryable = false
		message = "压缩模型返回了无效工具调用，未保存本次结果"
	}
	return &RuntimeError{
		Code:         "compaction_incomplete",
		Message:      message,
		Category:     category,
		Retryable:    retryable,
		Provider:     provider,
		ModelID:      modelID,
		FinishReason: string(normalized.Canonical),
		cause:        fmt.Errorf("compaction finish reason: %s", normalized.Raw),
	}
}
