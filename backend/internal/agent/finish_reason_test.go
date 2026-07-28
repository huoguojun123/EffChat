package agent

import (
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestNormalizeProviderFinishReason(t *testing.T) {
	cases := []struct {
		raw          string
		hasToolCalls bool
		want         FinishReason
	}{
		{raw: "stop", want: FinishReasonStop},
		{raw: "end_turn", want: FinishReasonStop},
		{raw: "STOP", want: FinishReasonStop},
		{raw: "stop_sequence", want: FinishReasonStop},
		{raw: "length", want: FinishReasonOutputLimit},
		{raw: "max_tokens", want: FinishReasonOutputLimit},
		{raw: "MAX_TOKENS", want: FinishReasonOutputLimit},
		{raw: "model_context_window_exceeded", want: FinishReasonOutputLimit},
		{raw: "content_filter", want: FinishReasonSafety},
		{raw: "refusal", want: FinishReasonSafety},
		{raw: "PROHIBITED_CONTENT", want: FinishReasonSafety},
		{raw: "MALFORMED_FUNCTION_CALL", want: FinishReasonMalformedTool},
		{raw: "MALFORMED_FUNCTION_CALL", hasToolCalls: true, want: FinishReasonMalformedTool},
		{raw: "UNEXPECTED_TOOL_CALL", want: FinishReasonMalformedTool},
		{raw: "tool_calls", want: FinishReasonToolContinuation},
		{raw: "tool_use", want: FinishReasonToolContinuation},
		{raw: "pause_turn", want: FinishReasonToolContinuation},
		{raw: "stop", hasToolCalls: true, want: FinishReasonToolContinuation},
		{raw: "", want: FinishReasonUnknown},
		{raw: "new_provider_reason", want: FinishReasonUnknown},
	}

	for _, testCase := range cases {
		t.Run(testCase.raw, func(t *testing.T) {
			got := normalizeProviderFinishReason(testCase.raw, testCase.hasToolCalls)
			if got.Canonical != testCase.want {
				t.Fatalf("canonical = %q, want %q", got.Canonical, testCase.want)
			}
		})
	}
}

func TestApplyNormalizedFinishReasonPreservesBoundedRawDiagnostic(t *testing.T) {
	raw := strings.Repeat("x", maxRawFinishReasonRunes+20)
	normalized := normalizeProviderFinishReason(raw, false)
	data := map[string]interface{}{
		"role":    "assistant",
		"content": "answer",
		"response_meta": map[string]interface{}{
			"usage": map[string]interface{}{"total_tokens": float64(42)},
		},
	}

	applyNormalizedFinishReason(data, normalized)

	meta, ok := data["response_meta"].(map[string]interface{})
	if !ok {
		t.Fatalf("response_meta = %#v", data["response_meta"])
	}
	if meta["finish_reason"] != string(FinishReasonUnknown) {
		t.Fatalf("finish_reason = %#v", meta["finish_reason"])
	}
	rawDiagnostic, _ := meta["raw_finish_reason"].(string)
	if len([]rune(rawDiagnostic)) != maxRawFinishReasonRunes {
		t.Fatalf("raw diagnostic length = %d", len([]rune(rawDiagnostic)))
	}
	if _, ok := meta["usage"]; !ok {
		t.Fatal("normalization discarded existing response usage")
	}
}

func TestCompletionIncompleteOnlyForOutputLimit(t *testing.T) {
	for _, reason := range []FinishReason{
		FinishReasonStop,
		FinishReasonSafety,
		FinishReasonMalformedTool,
		FinishReasonToolContinuation,
		FinishReasonUnknown,
	} {
		if completionIsIncomplete(reason) {
			t.Fatalf("%q must not be marked as output truncation", reason)
		}
	}
	if !completionIsIncomplete(FinishReasonOutputLimit) {
		t.Fatal("output limit must be marked incomplete")
	}
}

func TestValidateCompactionCompletionFailsClosed(t *testing.T) {
	truncated := schema.AssistantMessage("partial summary", nil)
	truncated.ResponseMeta = &schema.ResponseMeta{FinishReason: "max_tokens"}
	err := validateCompactionCompletion("provider", "model", truncated)
	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) {
		t.Fatalf("error = %T, want *RuntimeError", err)
	}
	if runtimeErr.Code != "compaction_incomplete" || runtimeErr.FinishReason != string(FinishReasonOutputLimit) {
		t.Fatalf("runtime error = %+v", runtimeErr)
	}

	complete := schema.AssistantMessage("complete summary", nil)
	complete.ResponseMeta = &schema.ResponseMeta{FinishReason: "end_turn"}
	if err := validateCompactionCompletion("provider", "model", complete); err != nil {
		t.Fatalf("natural completion rejected: %v", err)
	}
}
