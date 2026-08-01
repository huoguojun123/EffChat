package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/compose"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelstream"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
	internaltool "github.com/huoguojun123/EffChat/internal/tool"
	modelusage "github.com/huoguojun123/EffChat/internal/usage"
)

func TestToolGovernanceMiddleware_ReturnsStructuredError(t *testing.T) {
	runtime := service.ToolRuntimeConfigSet{
		"web_search": {Enabled: true, TimeoutSeconds: 20},
	}
	budget := &toolBudgetMiddleware{maxCalls: 8, maxResultTokens: 1000, maxContextTokens: 2000, maxSkillTokens: 1000}
	middleware := toolGovernanceMiddleware(runtime, nil, nil, budget).Invokable
	endpoint := middleware(func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
		return nil, errors.New("postgres://secret@internal/private/path")
	})

	out, err := endpoint(context.Background(), &compose.ToolInput{Name: "web_search", CallID: "call-1", Arguments: `{"query":"x"}`})
	if err != nil {
		t.Fatalf("middleware should convert tool errors to structured output, got error: %v", err)
	}
	if out == nil || out.Result == "" {
		t.Fatal("expected structured tool output")
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(out.Result), &payload); err != nil {
		t.Fatalf("tool output is not JSON: %v", err)
	}
	if payload["ok"] != false || payload["tool"] != "web_search" || payload["source"] != "tool_governance" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
	if payload["code"] != "tool_execution_failed" || payload["retryable"] != false {
		t.Fatalf("unexpected public classification: %#v", payload)
	}
	if strings.Contains(out.Result, "secret") || strings.Contains(out.Result, "/private/path") || strings.Contains(out.Result, "postgres") {
		t.Fatalf("tool output leaked internal error: %s", out.Result)
	}
	if budget.contextUsed == 0 || budget.contextReserved != 0 {
		t.Fatalf("structured Go error was not accounted: used=%d reserved=%d", budget.contextUsed, budget.contextReserved)
	}
}

func TestToolCallContextLeavesWebExtractToItsStagedBudgets(t *testing.T) {
	ctx, cancel := toolCallContext(t.Context(), "web_extract", 20*time.Millisecond)
	defer cancel()
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("web_extract must not inherit an outer absolute tool deadline")
	}

	parent, parentCancel := context.WithCancel(t.Context())
	ctx, cancel = toolCallContext(parent, "web_extract", time.Second)
	parentCancel()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("web_extract context did not preserve semantic parent cancellation")
	}
	cancel()
}

func TestToolCallContextKeepsAbsoluteTimeoutForOrdinaryTools(t *testing.T) {
	ctx, cancel := toolCallContext(t.Context(), "web_search", 20*time.Millisecond)
	defer cancel()
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("ordinary tool context must retain its absolute timeout")
	}
}

func TestToolUsageErrorTypePreservesFirstOutputTimeout(t *testing.T) {
	if got := toolUsageErrorType(modelstream.ErrFirstOutputTimeout, t.Context()); got != "first_output_timeout" {
		t.Fatalf("toolUsageErrorType() = %q, want first_output_timeout", got)
	}
}

func TestToolGovernanceMiddlewarePropagatesParentCancellationAfterUsageClosure(t *testing.T) {
	quota := service.NewQuotaService(fakeToolQuotaStore{reservationID: 77})
	usageStore := &fakeToolUsageStore{}
	usageService := modelusage.NewService(usageStore)
	budget := &toolBudgetMiddleware{maxCalls: 8, maxResultTokens: 1000, maxContextTokens: 2000, maxSkillTokens: 1000}
	started := make(chan struct{})
	endpoint := toolGovernanceMiddleware(service.ToolRuntimeConfigSet{
		"web_extract": {Enabled: true, TimeoutSeconds: 20},
	}, quota, usageService, budget).Invokable(func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
		close(started)
		<-ctx.Done()
		return nil, context.Cause(ctx)
	})

	stopCause := &testToolCancellation{message: "server draining"}
	baseCtx, cancel := context.WithCancelCause(modelusage.WithMeta(t.Context(), modelusage.Meta{
		UserID: 7, SessionID: 9, RunID: "run-cancel",
	}))
	result := make(chan struct {
		output *compose.ToolOutput
		err    error
	}, 1)
	go func() {
		output, err := endpoint(baseCtx, &compose.ToolInput{Name: "web_extract", CallID: "call-1", Arguments: `{}`})
		result <- struct {
			output *compose.ToolOutput
			err    error
		}{output: output, err: err}
	}()

	<-started
	cancel(stopCause)
	select {
	case got := <-result:
		if got.err != stopCause || got.output != nil {
			t.Fatalf("endpoint = (%#v, %v), want nil and exact parent cause %v", got.output, got.err, stopCause)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled Tool call did not return")
	}
	if usageStore.updated.ID != 77 || usageStore.updated.Success || usageStore.updated.ErrorType != "canceled" || usageStore.updated.ContextTokens != 0 {
		t.Fatalf("canceled usage terminal = %#v", usageStore.updated)
	}
	if budget.contextReserved != 0 || budget.actualCalls.Load() != 1 {
		t.Fatalf("canceled Tool budget = reserved:%d calls:%d", budget.contextReserved, budget.actualCalls.Load())
	}
}

func TestToolGovernanceMiddlewarePropagatesCancellationOverSuccessfulToolOutput(t *testing.T) {
	quota := service.NewQuotaService(fakeToolQuotaStore{reservationID: 78})
	usageStore := &fakeToolUsageStore{}
	usageService := modelusage.NewService(usageStore)
	budget := &toolBudgetMiddleware{maxCalls: 8, maxResultTokens: 1000, maxContextTokens: 2000, maxSkillTokens: 1000}
	stopCause := &testToolCancellation{message: "user stop"}
	baseCtx, cancel := context.WithCancelCause(modelusage.WithMeta(t.Context(), modelusage.Meta{
		UserID: 7, SessionID: 9, RunID: "run-cancel-success",
	}))
	endpoint := toolGovernanceMiddleware(service.ToolRuntimeConfigSet{
		"web_extract": {Enabled: true, TimeoutSeconds: 20},
	}, quota, usageService, budget).Invokable(func(context.Context, *compose.ToolInput) (*compose.ToolOutput, error) {
		cancel(stopCause)
		return &compose.ToolOutput{Result: `{"ok":true,"content":"must not reach the model"}`}, nil
	})

	output, err := endpoint(baseCtx, &compose.ToolInput{Name: "web_extract", CallID: "call-1", Arguments: `{}`})
	if err != stopCause || output != nil {
		t.Fatalf("endpoint = (%#v, %v), want nil and exact parent cause %v", output, err, stopCause)
	}
	if usageStore.updated.ID != 78 || usageStore.updated.Success || usageStore.updated.ErrorType != "canceled" || usageStore.updated.ContextTokens != 0 {
		t.Fatalf("canceled successful output usage = %#v", usageStore.updated)
	}
	if budget.contextReserved != 0 {
		t.Fatalf("canceled successful output kept context reservation: %d", budget.contextReserved)
	}
}

func TestToolGovernanceMiddlewarePropagatesWebExtractRefinementCancellationEndToEnd(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("Title: cancellation\nMarkdown Content:\nsource evidence"))
	}))
	defer server.Close()
	summarizer := &governanceBlockingSummarizer{started: make(chan struct{})}
	webExtract := internaltool.NewWebExtractTool(internaltool.WebExtractConfig{
		CrawlerProviders: []string{"jina"},
		JinaBaseURL:      server.URL,
		Summarizer:       summarizer,
		SummaryEnabled:   true,
		Timeout:          time.Second,
	})

	quota := service.NewQuotaService(fakeToolQuotaStore{reservationID: 80})
	usageStore := &fakeToolUsageStore{}
	usageService := modelusage.NewService(usageStore)
	endpoint := toolGovernanceMiddleware(service.ToolRuntimeConfigSet{
		"web_extract": {Enabled: true, TimeoutSeconds: 20},
	}, quota, usageService, nil).Invokable(func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
		result, err := webExtract.InvokableRun(ctx, input.Arguments)
		if err != nil {
			return nil, err
		}
		return &compose.ToolOutput{Result: result}, nil
	})

	stopCause := &testToolCancellation{message: "server draining"}
	ctx, cancel := context.WithCancelCause(modelusage.WithMeta(t.Context(), modelusage.Meta{
		UserID: 7, SessionID: 9, RunID: "run-web-extract-cancel",
	}))
	result := make(chan struct {
		output *compose.ToolOutput
		err    error
	}, 1)
	go func() {
		output, err := endpoint(ctx, &compose.ToolInput{
			Name:      "web_extract",
			CallID:    "call-1",
			Arguments: `{"url":"https://example.com/article","goal":"verify cancellation"}`,
		})
		result <- struct {
			output *compose.ToolOutput
			err    error
		}{output: output, err: err}
	}()

	<-summarizer.started
	cancel(stopCause)
	select {
	case got := <-result:
		if got.err != stopCause || got.output != nil {
			t.Fatalf("end-to-end endpoint = (%#v, %v), want nil and exact parent cause %v", got.output, got.err, stopCause)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("end-to-end web_extract cancellation did not return")
	}
	if usageStore.updated.ID != 80 || usageStore.updated.Success || usageStore.updated.ErrorType != "canceled" || usageStore.updated.ContextTokens != 0 {
		t.Fatalf("end-to-end canceled usage = %#v", usageStore.updated)
	}
}

func TestToolGovernanceMiddlewarePropagatesCancellationDuringQuotaReservation(t *testing.T) {
	started := make(chan struct{})
	quota := service.NewQuotaService(blockingToolQuotaStore{
		fakeToolQuotaStore: fakeToolQuotaStore{},
		started:            started,
	})
	budget := &toolBudgetMiddleware{maxCalls: 8, maxResultTokens: 1000, maxContextTokens: 2000, maxSkillTokens: 1000}
	called := false
	endpoint := toolGovernanceMiddleware(service.ToolRuntimeConfigSet{
		"web_extract": {Enabled: true, TimeoutSeconds: 20},
	}, quota, nil, budget).Invokable(func(context.Context, *compose.ToolInput) (*compose.ToolOutput, error) {
		called = true
		return &compose.ToolOutput{Result: `{"ok":true}`}, nil
	})

	stopCause := &testToolCancellation{message: "server draining"}
	ctx, cancel := context.WithCancelCause(modelusage.WithMeta(t.Context(), modelusage.Meta{UserID: 7, SessionID: 9, RunID: "run-quota-cancel"}))
	result := make(chan error, 1)
	go func() {
		output, err := endpoint(ctx, &compose.ToolInput{Name: "web_extract", CallID: "call-1", Arguments: `{}`})
		if output != nil {
			result <- errors.New("quota cancellation returned Tool JSON")
			return
		}
		result <- err
	}()

	<-started
	cancel(stopCause)
	select {
	case err := <-result:
		if err != stopCause {
			t.Fatalf("endpoint error = %v, want exact parent cause %v", err, stopCause)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("quota reservation did not observe parent cancellation")
	}
	if called {
		t.Fatal("Tool endpoint ran after quota reservation cancellation")
	}
	if budget.contextReserved != 0 || budget.actualCalls.Load() != 0 {
		t.Fatalf("quota cancellation leaked Tool budget: reserved=%d calls=%d", budget.contextReserved, budget.actualCalls.Load())
	}
}

func TestToolGovernanceMiddlewareKeepsOwnedToolTimeoutStructured(t *testing.T) {
	quota := service.NewQuotaService(fakeToolQuotaStore{reservationID: 79})
	usageStore := &fakeToolUsageStore{}
	usageService := modelusage.NewService(usageStore)
	endpoint := toolGovernanceMiddleware(service.ToolRuntimeConfigSet{
		"web_search": {Enabled: true, TimeoutSeconds: 1},
	}, quota, usageService, nil).Invokable(func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
		<-ctx.Done()
		return nil, context.Cause(ctx)
	})

	ctx := modelusage.WithMeta(t.Context(), modelusage.Meta{UserID: 7, SessionID: 9, RunID: "run-tool-timeout"})
	output, err := endpoint(ctx, &compose.ToolInput{Name: "web_search", CallID: "call-1", Arguments: `{}`})
	if err != nil {
		t.Fatalf("owned Tool timeout must remain structured JSON: %v", err)
	}
	if output == nil || !strings.Contains(output.Result, `"retryable":true`) || !strings.Contains(output.Result, `"ok":false`) {
		t.Fatalf("owned Tool timeout output = %#v", output)
	}
	if usageStore.updated.ID != 79 || usageStore.updated.Success || usageStore.updated.ErrorType != "timeout" {
		t.Fatalf("owned Tool timeout usage = %#v", usageStore.updated)
	}
}

func TestToolGovernanceMiddlewareDoesNotLogRawArguments(t *testing.T) {
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })

	runtime := service.ToolRuntimeConfigSet{"web_search": {Enabled: true, TimeoutSeconds: 20}}
	endpoint := toolGovernanceMiddleware(runtime, nil, nil, nil).Invokable(func(context.Context, *compose.ToolInput) (*compose.ToolOutput, error) {
		return &compose.ToolOutput{Result: `{"ok":true}`}, nil
	})
	if _, err := endpoint(context.Background(), &compose.ToolInput{Name: "web_search", CallID: "call-1", Arguments: `{"query":"private phrase"}`}); err != nil {
		t.Fatalf("endpoint error: %v", err)
	}
	if strings.Contains(logs.String(), "private phrase") || !strings.Contains(logs.String(), "args_chars=") {
		t.Fatalf("tool log exposed arguments: %s", logs.String())
	}
}

func TestToolGovernanceMiddleware_BlocksQuotaBeforeCall(t *testing.T) {
	runtime := service.ToolRuntimeConfigSet{
		"web_search": {Enabled: true, TimeoutSeconds: 20},
	}
	quota := service.NewQuotaService(fakeToolQuotaStore{
		reservationErr: &repository.ToolQuotaExceeded{
			Code:  "daily_web_search_limit_exceeded",
			Limit: 1,
			Used:  1,
		},
	})
	budget := &toolBudgetMiddleware{maxCalls: 8, maxResultTokens: 1000, maxContextTokens: 2000, maxSkillTokens: 1000}
	middleware := toolGovernanceMiddleware(runtime, quota, nil, budget).Invokable
	called := false
	endpoint := middleware(func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
		called = true
		return &compose.ToolOutput{Result: `{"ok":true}`}, nil
	})

	ctx := modelusage.WithMeta(context.Background(), modelusage.Meta{UserID: 7})
	out, err := endpoint(ctx, &compose.ToolInput{Name: "web_search", CallID: "call-1", Arguments: `{"query":"x"}`})
	if err != nil {
		t.Fatalf("middleware should return structured quota output, got error: %v", err)
	}
	if called {
		t.Fatal("tool endpoint should not be called after quota block")
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(out.Result), &payload); err != nil {
		t.Fatalf("tool output is not JSON: %v", err)
	}
	if payload["source"] != "tool_quota" || payload["code"] != "daily_web_search_limit_exceeded" {
		t.Fatalf("unexpected quota payload: %#v", payload)
	}
	if budget.contextUsed == 0 || budget.contextReserved != 0 || budget.actualCalls.Load() != 0 {
		t.Fatalf("quota terminal accounting mismatch: used=%d reserved=%d calls=%d", budget.contextUsed, budget.contextReserved, budget.actualCalls.Load())
	}
}

func TestToolGovernanceMiddlewareBlocksContextBudgetBeforeQuota(t *testing.T) {
	reserveCalls := 0
	quota := service.NewQuotaService(fakeToolQuotaStore{reserveCalls: &reserveCalls})
	budget := &toolBudgetMiddleware{
		maxCalls:         8,
		maxResultTokens:  1000,
		maxContextTokens: 1000,
		maxSkillTokens:   500,
		contextUsed:      900,
	}
	endpoint := toolGovernanceMiddleware(service.ToolRuntimeConfigSet{
		"web_search": {Enabled: true, TimeoutSeconds: 20},
	}, quota, nil, budget).Invokable(func(context.Context, *compose.ToolInput) (*compose.ToolOutput, error) {
		t.Fatal("tool endpoint should not run after context budget rejection")
		return nil, nil
	})

	out, err := endpoint(context.Background(), &compose.ToolInput{Name: "web_search", CallID: "call-1", Arguments: `{}`})
	if err != nil {
		t.Fatalf("context budget rejection should be structured: %v", err)
	}
	if reserveCalls != 0 {
		t.Fatalf("context budget rejection consumed quota: reserve calls=%d", reserveCalls)
	}
	if out == nil || !strings.Contains(out.Result, `"reason":"tool_context_budget_exhausted"`) {
		t.Fatalf("unexpected context budget output: %#v", out)
	}
}

func TestToolGovernanceMiddleware_UpdatesReservedUsageWithoutTruncatingResult(t *testing.T) {
	runtime := service.ToolRuntimeConfigSet{
		"file_read": {Enabled: true, TimeoutSeconds: 20},
	}
	quota := service.NewQuotaService(fakeToolQuotaStore{
		reservationID: 77,
	})
	usageStore := &fakeToolUsageStore{}
	usageService := modelusage.NewService(usageStore)
	middleware := toolGovernanceMiddleware(runtime, quota, usageService, nil).Invokable
	endpoint := middleware(func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
		return &compose.ToolOutput{Result: "abcdefghijklmnopqrstuvwxyz"}, nil
	})

	ctx := modelusage.WithMeta(context.Background(), modelusage.Meta{UserID: 7, SessionID: 9, RunID: "run-1"})
	out, err := endpoint(ctx, &compose.ToolInput{Name: "file_read", CallID: "call-1"})
	if err != nil {
		t.Fatalf("middleware should return original output, got error: %v", err)
	}
	if out.Result != "abcdefghijklmnopqrstuvwxyz" {
		t.Fatalf("tool result was unexpectedly changed: %q", out.Result)
	}
	if usageStore.updated.ID != 77 || !usageStore.updated.Success || usageStore.updated.ToolKey != "file_read" {
		t.Fatalf("reserved usage update mismatch: %#v", usageStore.updated)
	}
	if usageStore.updated.ContextTokens == 0 {
		t.Fatalf("expected estimated context tokens, got %#v", usageStore.updated)
	}
}

func TestInspectToolTerminalOutcome(t *testing.T) {
	tests := []struct {
		name      string
		result    string
		success   bool
		degraded  bool
		truncated bool
		errorType string
	}{
		{name: "plain success", result: "plain result", success: true},
		{name: "explicit success", result: `{"ok":true,"truncated":true}`, success: true, truncated: true},
		{name: "business error", result: `{"ok":false,"error_code":"fetch_failed","error":"upstream failed"}`, errorType: "fetch_failed"},
		{name: "legacy error", result: `{"error":"file unavailable"}`, errorType: "business_error"},
		{name: "search failed", result: `{"search_failed":true,"error":"no providers"}`, errorType: "search_failed"},
		{name: "budget blocked", result: `{"blocked":true,"reason":"tool_context_budget_exhausted"}`, errorType: "tool_context_budget_exhausted"},
		{name: "empty", result: "", errorType: "empty_result"},
		{name: "null JSON", result: "null", errorType: "invalid_result"},
		{name: "empty object", result: `{}`, errorType: "invalid_result"},
		{name: "invalid ok type", result: `{"ok":null}`, errorType: "invalid_result"},
		{name: "error code only", result: `{"error_code":"upstream_timeout"}`, errorType: "upstream_timeout"},
		{name: "degraded refinement", result: `{"ok":true,"degraded":true,"degradation_reason":"refinement_cooldown"}`, degraded: true, errorType: "degraded_refinement_cooldown"},
		{name: "degraded truncated source", result: `{"ok":true,"degraded":true,"degradation_reason":"source_truncated"}`, degraded: true, errorType: "degraded_source_truncated"},
		{name: "failed refinement", result: `{"ok":false,"error_code":"refinement_failed"}`, errorType: "refinement_failed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := inspectToolTerminalOutcome(tc.result)
			if got.Success != tc.success || got.Degraded != tc.degraded || got.Truncated != tc.truncated || got.ErrorType != tc.errorType {
				t.Fatalf("outcome = %#v", got)
			}
			if got.ContextTokens != modelusage.EstimateTextTokens(tc.result) {
				t.Fatalf("context tokens = %d, want %d", got.ContextTokens, modelusage.EstimateTextTokens(tc.result))
			}
		})
	}
}

func TestToolOutcomeCodeAndDiagnosticsMatchStorageLimits(t *testing.T) {
	code := normalizeToolOutcomeCode(strings.Repeat("A", 100))
	if len(code) != 60 {
		t.Fatalf("normalized error code length = %d, want 60", len(code))
	}
	got := sanitizeToolDiagnostic("first line\nAuthorization: Bearer-secret; api_key=private-value")
	if strings.Contains(got, "\n") || strings.Contains(got, "Bearer-secret") || strings.Contains(got, "private-value") {
		t.Fatalf("diagnostic was not sanitized: %q", got)
	}
}

func TestToolGovernanceCapsGoErrorDiagnosticWithoutLoggingSecret(t *testing.T) {
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })

	quota := service.NewQuotaService(fakeToolQuotaStore{reservationID: 91})
	usageStore := &fakeToolUsageStore{}
	usageService := modelusage.NewService(usageStore)
	budget := &toolBudgetMiddleware{
		maxCalls:         8,
		maxResultTokens:  512,
		maxContextTokens: 3000,
		maxSkillTokens:   1000,
	}
	endpoint := toolGovernanceMiddleware(service.ToolRuntimeConfigSet{
		"web_search": {Enabled: true, TimeoutSeconds: 20},
	}, quota, usageService, budget).Invokable(func(context.Context, *compose.ToolInput) (*compose.ToolOutput, error) {
		return nil, errors.New("api_key=private-value\n" + strings.Repeat("upstream unavailable ", 500))
	})

	ctx := modelusage.WithMeta(context.Background(), modelusage.Meta{UserID: 7, SessionID: 9, RunID: "run-1"})
	if _, err := endpoint(ctx, &compose.ToolInput{Name: "web_search", CallID: "call-1"}); err != nil {
		t.Fatalf("Go error should be converted to a structured result: %v", err)
	}
	// Truncated describes the model-visible Tool result, not the separately capped
	// internal diagnostic. The stable public error fits within the result budget.
	if usageStore.updated.Truncated || usageStore.updated.Success || usageStore.updated.ErrorType != "tool_error" {
		t.Fatalf("Go error usage mismatch: %#v", usageStore.updated)
	}
	if len([]rune(usageStore.updated.ErrorMessage)) != 500 {
		t.Fatalf("usage diagnostic length = %d, want storage cap", len([]rune(usageStore.updated.ErrorMessage)))
	}
	if strings.Contains(usageStore.updated.ErrorMessage, "private-value") || strings.Contains(usageStore.updated.ErrorMessage, "\n") {
		t.Fatalf("usage diagnostic exposed secret/control characters: %q", usageStore.updated.ErrorMessage)
	}
	if strings.Contains(logs.String(), "private-value") {
		t.Fatalf("tool logs exposed secret: %s", logs.String())
	}
}

func TestToolGovernanceMiddlewareRecordsBusinessFailureAndTruncation(t *testing.T) {
	quota := service.NewQuotaService(fakeToolQuotaStore{reservationID: 88})
	usageStore := &fakeToolUsageStore{}
	usageService := modelusage.NewService(usageStore)
	endpoint := toolGovernanceMiddleware(service.ToolRuntimeConfigSet{
		"web_extract": {Enabled: true, TimeoutSeconds: 20},
	}, quota, usageService, nil).Invokable(func(context.Context, *compose.ToolInput) (*compose.ToolOutput, error) {
		return &compose.ToolOutput{Result: `{"ok":false,"error_code":"refinement_failed","error":"summary unavailable","truncated":true}`}, nil
	})

	ctx := modelusage.WithMeta(context.Background(), modelusage.Meta{UserID: 7, SessionID: 9, RunID: "run-1"})
	out, err := endpoint(ctx, &compose.ToolInput{Name: "web_extract", CallID: "call-1"})
	if err != nil {
		t.Fatalf("business failure should remain a structured tool result: %v", err)
	}
	if out == nil || !strings.Contains(out.Result, `"ok":false`) {
		t.Fatalf("business result was not returned to the model: %#v", out)
	}
	if usageStore.updated.ID != 88 || usageStore.updated.Success || !usageStore.updated.Truncated {
		t.Fatalf("usage terminal state mismatch: %#v", usageStore.updated)
	}
	if usageStore.updated.ErrorType != "refinement_failed" || usageStore.updated.ContextTokens == 0 {
		t.Fatalf("usage failure details mismatch: %#v", usageStore.updated)
	}
}

func TestToolGovernanceMiddlewareRecordsDegradedBusinessResult(t *testing.T) {
	quota := service.NewQuotaService(fakeToolQuotaStore{reservationID: 89})
	usageStore := &fakeToolUsageStore{}
	usageService := modelusage.NewService(usageStore)
	endpoint := toolGovernanceMiddleware(service.ToolRuntimeConfigSet{
		"web_extract": {Enabled: true, TimeoutSeconds: 20},
	}, quota, usageService, nil).Invokable(func(context.Context, *compose.ToolInput) (*compose.ToolOutput, error) {
		return &compose.ToolOutput{Result: `{"ok":true,"content":"bounded source fallback","degraded":true,"degradation_reason":"refinement_cooldown","truncated":true}`}, nil
	})

	ctx := modelusage.WithMeta(context.Background(), modelusage.Meta{UserID: 7, SessionID: 9, RunID: "run-1"})
	out, err := endpoint(ctx, &compose.ToolInput{Name: "web_extract", CallID: "call-1"})
	if err != nil {
		t.Fatalf("degraded result should remain available to the model: %v", err)
	}
	if out == nil || !strings.Contains(out.Result, `"ok":true`) {
		t.Fatalf("degraded result was not returned to the model: %#v", out)
	}
	if usageStore.updated.Success || !usageStore.updated.Truncated {
		t.Fatalf("usage terminal state mismatch: %#v", usageStore.updated)
	}
	if usageStore.updated.ErrorType != "degraded_refinement_cooldown" || usageStore.updated.ContextTokens == 0 {
		t.Fatalf("usage degradation details mismatch: %#v", usageStore.updated)
	}
}

type fakeToolQuotaStore struct {
	limits         repository.UserQuotaLimits
	usage          repository.QuotaUsage
	reservationID  int64
	reservationErr error
	reserveCalls   *int
}

type blockingToolQuotaStore struct {
	fakeToolQuotaStore
	started chan struct{}
}

func (f blockingToolQuotaStore) ReserveToolCall(ctx context.Context, input repository.ToolCallReservationInput) (repository.ToolCallReservation, error) {
	close(f.started)
	<-ctx.Done()
	return repository.ToolCallReservation{}, context.Cause(ctx)
}

type governanceBlockingSummarizer struct {
	started chan struct{}
}

func (s *governanceBlockingSummarizer) Summarize(ctx context.Context, _, _, _, _ string) (string, error) {
	close(s.started)
	<-ctx.Done()
	return "", context.Cause(ctx)
}

type testToolCancellation struct {
	message string
}

func (e *testToolCancellation) Error() string {
	return e.message
}

func (e *testToolCancellation) Unwrap() error {
	return context.Canceled
}

func (f fakeToolQuotaStore) LimitsForUser(ctx context.Context, userID int64) (repository.UserQuotaLimits, error) {
	return f.limits, nil
}

func (f fakeToolQuotaStore) UsageForToday(ctx context.Context, userID int64) (repository.QuotaUsage, error) {
	return f.usage, nil
}

func (f fakeToolQuotaStore) ReserveToolCall(ctx context.Context, input repository.ToolCallReservationInput) (repository.ToolCallReservation, error) {
	if f.reserveCalls != nil {
		(*f.reserveCalls)++
	}
	if f.reservationErr != nil {
		return repository.ToolCallReservation{}, f.reservationErr
	}
	id := f.reservationID
	if id == 0 {
		id = 1
	}
	return repository.ToolCallReservation{ID: id, CreatedAt: time.Now()}, nil
}

func (f fakeToolQuotaStore) ReserveChatRun(context.Context, repository.ChatRunReservationInput) (repository.ChatRunReservation, error) {
	return repository.ChatRunReservation{}, nil
}

func (f fakeToolQuotaStore) AdmitChatMessage(context.Context, repository.ChatRunReservationInput, *model.Message) (repository.ChatRunAdmission, error) {
	return repository.ChatRunAdmission{}, nil
}

func (f fakeToolQuotaStore) AdmitRetryChatRun(context.Context, repository.ChatRunReservationInput, int64) (repository.ChatRunAdmission, error) {
	return repository.ChatRunAdmission{}, nil
}

func (f fakeToolQuotaStore) AdmitEditedRetryChatRun(context.Context, repository.ChatRunReservationInput, int64, *model.Message) (repository.ChatRunAdmission, error) {
	return repository.ChatRunAdmission{}, nil
}

func (f fakeToolQuotaStore) GetChatRun(context.Context, string) (repository.ChatRunRecord, error) {
	return repository.ChatRunRecord{}, repository.ErrNotFound
}

func (f fakeToolQuotaStore) ReserveOCRSubmission(context.Context, int64, int64, int64, int) (bool, error) {
	return true, nil
}

type fakeToolUsageStore struct {
	created modelusage.ToolEvent
	updated modelusage.ToolEvent
}

func (f *fakeToolUsageStore) Create(ctx context.Context, event *modelusage.Event) error {
	return nil
}

func (f *fakeToolUsageStore) Aggregate(ctx context.Context, start, end time.Time) (*modelusage.Summary, error) {
	return &modelusage.Summary{}, nil
}

func (f *fakeToolUsageStore) CreateToolEvent(ctx context.Context, event *modelusage.ToolEvent) error {
	f.created = *event
	return nil
}

func (f *fakeToolUsageStore) UpdateToolEvent(ctx context.Context, event *modelusage.ToolEvent) error {
	f.updated = *event
	return nil
}

func (f *fakeToolUsageStore) AggregateToolUsage(ctx context.Context, start, end time.Time) (modelusage.ToolTotals, []modelusage.ByTool, error) {
	return modelusage.ToolTotals{}, nil, nil
}

func (f *fakeToolUsageStore) QuotaUsersForToday(ctx context.Context) ([]modelusage.QuotaUserUsage, error) {
	return nil, nil
}
