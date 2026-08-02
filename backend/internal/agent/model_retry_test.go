package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/huoguojun123/EffChat/internal/providerhttp"
	openaisdk "github.com/openai/openai-go/v3"
	"google.golang.org/genai"
)

func TestTransientModelRetryConfigRetriesOnlyZeroOutputTransientErrors(t *testing.T) {
	var observed []ModelRetryTrace
	config := transientModelRetryConfig(func(trace ModelRetryTrace) {
		observed = append(observed, trace)
	})
	if config.MaxRetries != 1 {
		t.Fatalf("MaxRetries = %d, want 1", config.MaxRetries)
	}

	cases := []struct {
		name     string
		err      error
		output   *schema.Message
		wantRun  bool
		category RuntimeErrorCategory
	}{
		{name: "gateway timeout", err: fmt.Errorf("status code: 504 Gateway Timeout"), wantRun: true, category: RuntimeErrorTransient},
		{name: "network deadline", err: context.DeadlineExceeded, wantRun: true, category: RuntimeErrorConnection},
		{name: "authentication", err: fmt.Errorf("status code: 401"), wantRun: false},
		{name: "unknown internal error", err: errors.New("unexpected adapter state"), wantRun: false},
		{name: "user canceled", err: context.Canceled, wantRun: false},
		{name: "partial content", err: errors.New("connection reset"), output: &schema.Message{Content: "partial"}, wantRun: false},
		{name: "partial reasoning", err: errors.New("connection reset"), output: &schema.Message{ReasoningContent: "partial"}, wantRun: false},
		{
			name:    "named tool call",
			err:     errors.New("connection reset"),
			output:  &schema.Message{ToolCalls: []schema.ToolCall{{Function: schema.FunctionCall{Name: "web_search"}}}},
			wantRun: false,
		},
		{
			name:     "incomplete tool shell",
			err:      errors.New("connection reset"),
			output:   &schema.Message{ToolCalls: []schema.ToolCall{{ID: "call_1"}}},
			wantRun:  true,
			category: RuntimeErrorConnection,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			observed = nil
			decision := config.ShouldRetry(context.Background(), &adk.RetryContext{
				RetryAttempt:  1,
				Err:           testCase.err,
				OutputMessage: testCase.output,
			})
			if decision.Retry != testCase.wantRun {
				t.Fatalf("Retry = %t, want %t", decision.Retry, testCase.wantRun)
			}
			if !testCase.wantRun {
				if len(observed) != 0 {
					t.Fatalf("non-retryable error emitted retry trace: %+v", observed)
				}
				return
			}
			if decision.Backoff <= 0 || len(observed) != 1 {
				t.Fatalf("retry decision=%+v observed=%+v", decision, observed)
			}
			if observed[0].Attempt != 1 || observed[0].MaxAttempts != 2 || observed[0].Category != testCase.category {
				t.Fatalf("retry trace = %+v", observed[0])
			}
			if observed[0].Delay != decision.Backoff {
				t.Fatalf("trace delay=%s decision delay=%s", observed[0].Delay, decision.Backoff)
			}
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	decision := config.ShouldRetry(ctx, &adk.RetryContext{Err: context.DeadlineExceeded})
	if decision.Retry {
		t.Fatal("expired run context must not retry")
	}

	observed = nil
	decision = config.ShouldRetry(context.Background(), &adk.RetryContext{
		RetryAttempt: 2,
		Err:          &einoopenai.APIError{HTTPStatusCode: http.StatusServiceUnavailable},
	})
	if decision.Retry || len(observed) != 0 {
		t.Fatalf("exhausted retry emitted false progress: decision=%+v observed=%+v", decision, observed)
	}
}

func TestClassifyModelRuntimeErrorUsesStructuredProviderSignals(t *testing.T) {
	anthropicResponse := &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{
		"Retry-After": []string{"120"},
	}}
	transportClient := providerhttp.NewAnthropicSingleAttemptClient(roundTripFuncForAgentTest(func(*http.Request) (*http.Response, error) {
		return nil, syscall.ECONNREFUSED
	}))
	transportResponse, err := transportClient.Get("https://provider.invalid/v1/messages")
	if err != nil {
		t.Fatalf("create synthetic Anthropic transport response: %v", err)
	}
	defer transportResponse.Body.Close()
	cases := []struct {
		name       string
		err        error
		code       string
		category   RuntimeErrorCategory
		retryable  bool
		status     int
		retryAfter time.Duration
	}{
		{
			name: "openai responses rate limit",
			err: &openaisdk.Error{
				StatusCode: http.StatusTooManyRequests,
				Message:    "rate limited",
				Response: &http.Response{Header: http.Header{
					"Retry-After": []string{"12"},
				}},
			},
			code:       "model_rate_limited",
			category:   RuntimeErrorTransient,
			retryable:  true,
			status:     http.StatusTooManyRequests,
			retryAfter: 12 * time.Second,
		},
		{
			name:      "openai rate limit",
			err:       &einoopenai.APIError{HTTPStatusCode: http.StatusTooManyRequests},
			code:      "model_rate_limited",
			category:  RuntimeErrorTransient,
			retryable: true,
			status:    http.StatusTooManyRequests,
		},
		{
			name:      "openai context overflow",
			err:       &einoopenai.APIError{HTTPStatusCode: http.StatusBadRequest, Message: "maximum context length exceeded"},
			code:      "model_context_exceeded",
			category:  RuntimeErrorContext,
			retryable: false,
			status:    http.StatusBadRequest,
		},
		{
			name:      "openai gateway quota",
			err:       &einoopenai.APIError{HTTPStatusCode: http.StatusForbidden, Message: "token quota is not enough (request id: req-quota-123)"},
			code:      "model_quota_exceeded",
			category:  RuntimeErrorAccess,
			retryable: false,
			status:    http.StatusForbidden,
		},
		{
			name:      "openai gateway has no channel",
			err:       &einoopenai.APIError{HTTPStatusCode: http.StatusServiceUnavailable, Message: "No available channel for model sample under group default (request id: req-channel-123)"},
			code:      "model_channel_unavailable",
			category:  RuntimeErrorTransient,
			retryable: true,
			status:    http.StatusServiceUnavailable,
		},
		{
			name:       "anthropic retry after is capped",
			err:        &anthropic.Error{StatusCode: http.StatusTooManyRequests, Response: anthropicResponse},
			code:       "model_rate_limited",
			category:   RuntimeErrorTransient,
			retryable:  true,
			status:     http.StatusTooManyRequests,
			retryAfter: maxModelRetryDelay,
		},
		{
			name:      "anthropic synthetic transport error",
			err:       &anthropic.Error{StatusCode: transportResponse.StatusCode, Response: transportResponse},
			code:      "model_connection_failed",
			category:  RuntimeErrorConnection,
			retryable: true,
			status:    599,
		},
		{
			name:      "gemini access denied",
			err:       genai.APIError{Code: http.StatusForbidden},
			code:      "model_access_denied",
			category:  RuntimeErrorAccess,
			retryable: false,
			status:    http.StatusForbidden,
		},
		{
			name:      "network reset",
			err:       syscall.ECONNRESET,
			code:      "model_connection_failed",
			category:  RuntimeErrorConnection,
			retryable: true,
		},
		{
			name:      "context overflow",
			err:       errors.New("maximum context length exceeded"),
			code:      "model_context_exceeded",
			category:  RuntimeErrorContext,
			retryable: false,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := classifyModelRuntimeError(testCase.err)
			if got.Code != testCase.code || got.Category != testCase.category || got.Retryable != testCase.retryable {
				t.Fatalf("classification = %+v", got)
			}
			if got.HTTPStatus != testCase.status || got.RetryAfter != testCase.retryAfter {
				t.Fatalf("transport metadata = status:%d retry-after:%s", got.HTTPStatus, got.RetryAfter)
			}
		})
	}
}

type roundTripFuncForAgentTest func(*http.Request) (*http.Response, error)

func (f roundTripFuncForAgentTest) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestClassifyModelRuntimeErrorExposesOnlySafeUpstreamDiagnostics(t *testing.T) {
	got := classifyModelRuntimeError(&einoopenai.APIError{
		HTTPStatusCode: http.StatusForbidden,
		Message:        "token quota is not enough, api_key=secret (request id: 202607280303169303035728268d9d6ovFBnE28)",
	})
	if got.Message != "上游模型额度不足，请在模型网关补充额度后重试" {
		t.Fatalf("message = %q", got.Message)
	}
	if got.Diagnostic != "HTTP 403 · 上游额度不足 · 请求 ID 202607280303169303035728268d9d6ovFBnE28" {
		t.Fatalf("diagnostic = %q", got.Diagnostic)
	}
	if strings.Contains(got.Diagnostic, "secret") || strings.Contains(got.Diagnostic, "api_key") {
		t.Fatalf("diagnostic leaked upstream detail: %q", got.Diagnostic)
	}
}

func TestClassifyModelRuntimeErrorKeepsStatusFromUnstructuredQuotaError(t *testing.T) {
	got := classifyModelRuntimeError(errors.New(
		"error, status code: 403, message: token quota is not enough (request id: req-unstructured-123)",
	))
	if got.Code != "model_quota_exceeded" || got.HTTPStatus != http.StatusForbidden {
		t.Fatalf("classification = %+v", got)
	}
	if got.Diagnostic != "HTTP 403 · 上游额度不足 · 请求 ID req-unstructured-123" {
		t.Fatalf("diagnostic = %q", got.Diagnostic)
	}
}

func TestSanitizeModelRuntimeErrorKeepsCausePrivate(t *testing.T) {
	upstream := errors.New("api_key=secret upstream body")
	err := sanitizeModelRuntimeError("provider-secret", "model-secret", upstream)
	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) {
		t.Fatalf("error type = %T, want *RuntimeError", err)
	}
	if !errors.Is(err, upstream) {
		t.Fatal("runtime error should preserve its private cause for logs")
	}
	if strings.Contains(runtimeErr.Message, "secret") || strings.Contains(runtimeErr.Message, "upstream body") {
		t.Fatalf("public message leaked upstream detail: %q", runtimeErr.Message)
	}
}
