package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/huoguojun123/EffChat/internal/service"
	modelusage "github.com/huoguojun123/EffChat/internal/usage"
)

var sensitiveToolDiagnosticPattern = regexp.MustCompile(`(?i)(api[_-]?key|authorization|bearer|access[_-]?token|password|secret)(\s*[:=]?\s*)[^\s,;]+`)

type toolErrorOutput struct {
	OK        bool   `json:"ok"`
	Tool      string `json:"tool"`
	Code      string `json:"code,omitempty"`
	Error     string `json:"error"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	Source    string `json:"source"`
}

type toolTerminalOutcome struct {
	Success       bool
	Degraded      bool
	Truncated     bool
	ContextTokens int
	ErrorType     string
	ErrorMessage  string
}

func (a *EinoAgent) resolveToolRuntimeConfig() service.ToolRuntimeConfigSet {
	runtime, _ := a.resolveToolRuntimeConfigWithState()
	return runtime
}

func (a *EinoAgent) resolveToolRuntimeConfigWithState() (service.ToolRuntimeConfigSet, service.RuntimeConfigState) {
	if a == nil || a.toolService == nil {
		return service.NewToolConfigService(nil).ResolveRuntimeConfig()
	}
	return a.toolService.ResolveRuntimeConfig()
}

func appendToolIfEnabled(tools []einoTool.BaseTool, runtime service.ToolRuntimeConfigSet, key string, item einoTool.BaseTool) []einoTool.BaseTool {
	if !runtime.IsEnabled(key) {
		log.Printf("[tool_governance] tool_disabled tool=%s", key)
		return tools
	}
	return append(tools, item)
}

func toolGovernanceMiddleware(runtime service.ToolRuntimeConfigSet, quotaService *service.QuotaService, usageService *modelusage.Service, budget *toolBudgetMiddleware) compose.ToolMiddleware {
	return compose.ToolMiddleware{
		Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
				started := time.Now()
				if cause := semanticToolParentCause(ctx); cause != nil {
					return nil, cause
				}
				timeout := runtime.Timeout(input.Name)
				callCtx, cancel := toolCallContext(ctx, input.Name, timeout)
				defer cancel()
				meta := modelusage.MetaFromContext(ctx)

				log.Printf("[tool_governance] call_start user=%d session=%d run=%s tool=%s call_id=%s timeout_ms=%d args_chars=%d",
					meta.UserID, meta.SessionID, meta.RunID, input.Name, input.CallID, timeout.Milliseconds(), utf8.RuneCountInString(input.Arguments))
				arguments, grant, blocked := input.Arguments, toolContextGrant{}, ""
				if budget != nil {
					arguments, grant, blocked = budget.prepareToolCall(input.Name, input.Arguments)
					if blocked != "" {
						if cause := semanticToolParentCause(ctx); cause != nil {
							return nil, cause
						}
						return &compose.ToolOutput{Result: budget.accountUnreservedResult(input.Name, blocked)}, nil
					}
				}
				preparedInput := *input
				preparedInput.Arguments = arguments
				reservation, err := reserveToolCall(ctx, quotaService, meta, input)
				if err != nil {
					duration := time.Since(started)
					if cause := semanticToolParentCause(ctx); cause != nil {
						if budget != nil {
							budget.cancelToolGrant(grant, true)
						}
						log.Printf("[tool_governance] call_canceled user=%d session=%d run=%s tool=%s call_id=%s duration_ms=%d error_type=%s stage=quota",
							meta.UserID, meta.SessionID, meta.RunID, input.Name, input.CallID, duration.Milliseconds(), toolUsageErrorType(cause, ctx))
						return nil, cause
					}
					errText, code := quotaErrorText(err)
					result := marshalToolQuotaError(input.Name, code, errText)
					if budget != nil {
						budget.actualCalls.Add(-1)
						result = budget.finishToolResult(input.Name, result, grant)
					}
					log.Printf("[tool_governance] call_blocked_by_quota user=%d session=%d run=%s tool=%s call_id=%s duration_ms=%d error_type=%s",
						meta.UserID, meta.SessionID, meta.RunID, input.Name, input.CallID, duration.Milliseconds(), code)
					return &compose.ToolOutput{Result: result}, nil
				}
				if cause := semanticToolParentCause(ctx); cause != nil {
					return nil, finishCanceledToolCall(usageService, budget, grant, reservation, meta, input, started, cause, ctx, true)
				}
				output, err := next(callCtx, &preparedInput)
				duration := time.Since(started)
				if cause := semanticToolParentCause(ctx); cause != nil {
					return nil, finishCanceledToolCall(usageService, budget, grant, reservation, meta, input, started, cause, ctx, false)
				}
				if err != nil {
					errText := err.Error()
					code := "tool_execution_failed"
					publicMessage := "Tool call failed. Continue with the best available information."
					retryable := false
					if errors.Is(callCtx.Err(), context.DeadlineExceeded) {
						code = "tool_timeout"
						publicMessage = "Tool call timed out. Continue with the best available information."
						retryable = true
					}
					errorType := toolUsageErrorType(err, callCtx)
					log.Printf("[tool_governance] call_failed user=%d session=%d run=%s tool=%s call_id=%s duration_ms=%d error_type=%s retryable=%t",
						meta.UserID, meta.SessionID, meta.RunID, input.Name, input.CallID, duration.Milliseconds(), errorType, retryable)
					result := marshalToolError(input.Name, code, publicMessage, retryable)
					if budget != nil {
						result = budget.finishToolResult(input.Name, result, grant)
					}
					outcome := inspectToolTerminalOutcome(result)
					finishToolUsage(usageService, reservation, meta, input, false, outcome.ContextTokens, outcome.Truncated, duration, errorType, errText)
					return &compose.ToolOutput{Result: result}, nil
				}
				internalDiagnostic := ""
				if output != nil {
					output.Result, internalDiagnostic = sanitizeStructuredToolFailure(input.Name, output.Result)
				}
				if output != nil && budget != nil {
					output.Result = budget.finishToolResult(input.Name, output.Result, grant)
				} else if budget != nil {
					budget.cancelToolGrant(grant, false)
				}
				if output == nil || strings.TrimSpace(output.Result) == "" {
					result := marshalToolError(input.Name, "tool_empty_result", "Tool returned no result. Continue with the best available information.", false)
					if budget != nil {
						result = budget.accountUnreservedResult(input.Name, result)
					}
					output = &compose.ToolOutput{Result: result}
				}
				outcome := inspectToolTerminalOutcome(output.Result)
				resultLen := utf8.RuneCountInString(output.Result)
				if outcome.Success {
					log.Printf("[tool_governance] call_success user=%d session=%d run=%s tool=%s call_id=%s duration_ms=%d result_chars=%d context_tokens=%d truncated=%t",
						meta.UserID, meta.SessionID, meta.RunID, input.Name, input.CallID, duration.Milliseconds(), resultLen, outcome.ContextTokens, outcome.Truncated)
				} else if outcome.Degraded {
					log.Printf("[tool_governance] call_degraded user=%d session=%d run=%s tool=%s call_id=%s duration_ms=%d result_chars=%d context_tokens=%d truncated=%t error_type=%s",
						meta.UserID, meta.SessionID, meta.RunID, input.Name, input.CallID, duration.Milliseconds(), resultLen, outcome.ContextTokens, outcome.Truncated, outcome.ErrorType)
				} else {
					log.Printf("[tool_governance] call_business_failure user=%d session=%d run=%s tool=%s call_id=%s duration_ms=%d result_chars=%d context_tokens=%d truncated=%t error_type=%s",
						meta.UserID, meta.SessionID, meta.RunID, input.Name, input.CallID, duration.Milliseconds(), resultLen, outcome.ContextTokens, outcome.Truncated, outcome.ErrorType)
				}
				diagnostic := outcome.ErrorMessage
				if internalDiagnostic != "" {
					diagnostic = internalDiagnostic
				}
				finishToolUsage(usageService, reservation, meta, input, outcome.Success, outcome.ContextTokens, outcome.Truncated, duration, outcome.ErrorType, diagnostic)
				return output, nil
			}
		},
	}
}

// sanitizeStructuredToolFailure is the final trust boundary for Tool results
// that report failure inside a successful JSON envelope. Tool implementations
// may retain a private cause in the error field for usage diagnostics, but only
// their explicit public message may enter model context, RunHub events, or the
// frontend tool tree. Typed web failures already carry an error_code and are
// produced by the web tool's separate public classifier, so they remain intact.
func sanitizeStructuredToolFailure(toolName, result string) (string, string) {
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(result), &payload); err != nil || len(payload) == 0 {
		return result, ""
	}
	errorText, _ := payload["error"].(string)
	errorText = strings.TrimSpace(errorText)
	if errorText == "" {
		return result, ""
	}
	if errorCode, _ := payload["error_code"].(string); strings.TrimSpace(errorCode) != "" {
		return result, ""
	}
	publicMessage, _ := payload["message"].(string)
	publicMessage = strings.TrimSpace(publicMessage)
	if publicMessage == "" {
		return result, ""
	}
	payload["error"] = publicMessage
	payload["ok"] = false
	if code, _ := payload["code"].(string); strings.TrimSpace(code) == "" {
		payload["code"] = "tool_business_failure"
	}
	if _, ok := payload["retryable"].(bool); !ok {
		payload["retryable"] = false
	}
	if source, _ := payload["source"].(string); strings.TrimSpace(source) == "" {
		payload["source"] = "tool"
	}
	if toolKey, _ := payload["tool"].(string); strings.TrimSpace(toolKey) == "" && strings.TrimSpace(toolName) != "" {
		payload["tool"] = toolName
	}
	sanitized, err := json.Marshal(payload)
	if err != nil {
		return result, ""
	}
	return string(sanitized), errorText
}

func semanticToolParentCause(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return context.Cause(ctx)
}

// finishCanceledToolCall closes every durable side effect before returning the
// semantic parent cause to Eino. Cancellation must not become Tool JSON because
// that would let the ReAct loop continue after user stop, RunHub drain, or run
// invalidation. The reserved usage row is still finalized through its own
// bounded background context, and any undelivered context grant is released.
func finishCanceledToolCall(
	usageService *modelusage.Service,
	budget *toolBudgetMiddleware,
	grant toolContextGrant,
	reservation service.ToolCallQuotaReservation,
	meta modelusage.Meta,
	input *compose.ToolInput,
	started time.Time,
	cause error,
	parent context.Context,
	releaseCall bool,
) error {
	if budget != nil {
		budget.cancelToolGrant(grant, releaseCall)
	}
	duration := time.Since(started)
	errorType := toolUsageErrorType(cause, parent)
	log.Printf("[tool_governance] call_canceled user=%d session=%d run=%s tool=%s call_id=%s duration_ms=%d error_type=%s",
		meta.UserID, meta.SessionID, meta.RunID, input.Name, input.CallID, duration.Milliseconds(), errorType)
	finishToolUsage(usageService, reservation, meta, input, false, 0, false, duration, errorType, cause.Error())
	return cause
}

// toolCallContext keeps ordinary Tools under an absolute execution timeout.
// web_extract is different because it owns two independently bounded phases:
// crawler fallback first, then a streaming model refinement whose timeout only
// waits for first output. Wrapping both phases in another absolute deadline
// would make refinement inherit only the crawler's leftover time.
func toolCallContext(parent context.Context, toolName string, timeout time.Duration) (context.Context, context.CancelFunc) {
	if toolName == "web_extract" {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, timeout)
}

func inspectToolTerminalOutcome(result string) toolTerminalOutcome {
	outcome := toolTerminalOutcome{
		Success:       true,
		ContextTokens: modelusage.EstimateTextTokens(result),
	}
	if strings.TrimSpace(result) == "" {
		outcome.Success = false
		outcome.ErrorType = "empty_result"
		outcome.ErrorMessage = "tool returned an empty result"
		return outcome
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		if json.Valid([]byte(result)) {
			return invalidToolTerminalOutcome(outcome)
		}
		return outcome
	}
	if len(payload) == 0 {
		return invalidToolTerminalOutcome(outcome)
	}
	outcome.Truncated, _ = payload["truncated"].(bool)
	okValue, hasOK := payload["ok"].(bool)
	successValue, hasSuccess := payload["success"].(bool)
	if _, exists := payload["ok"]; exists && !hasOK {
		return invalidToolTerminalOutcome(outcome)
	}
	if _, exists := payload["success"]; exists && !hasSuccess {
		return invalidToolTerminalOutcome(outcome)
	}
	blocked, _ := payload["blocked"].(bool)
	searchFailed, _ := payload["search_failed"].(bool)
	degraded, hasDegraded := payload["degraded"].(bool)
	if _, exists := payload["degraded"]; exists && !hasDegraded {
		return invalidToolTerminalOutcome(outcome)
	}
	errorText, _ := payload["error"].(string)
	errorText = strings.TrimSpace(errorText)
	errorCode, _ := payload["error_code"].(string)
	hasErrorCode := strings.TrimSpace(errorCode) != ""
	if blocked {
		reason, _ := payload["reason"].(string)
		hasErrorCode = hasErrorCode || strings.TrimSpace(reason) != ""
	}
	if hasOK && !okValue {
		code, _ := payload["code"].(string)
		hasErrorCode = hasErrorCode || strings.TrimSpace(code) != ""
	}

	outcome.Success = (!hasOK || okValue) &&
		(!hasSuccess || successValue) &&
		!blocked &&
		!searchFailed &&
		!degraded &&
		!hasErrorCode &&
		errorText == ""
	if outcome.Success {
		return outcome
	}
	if degraded {
		outcome.Degraded = true
		outcome.ErrorType = "degraded_" + firstToolOutcomeCode(payload, false, false)
		outcome.ErrorMessage = "tool returned a degraded result"
		return outcome
	}
	outcome.ErrorType = firstToolOutcomeCode(payload, blocked, searchFailed)
	outcome.ErrorMessage = errorText
	if outcome.ErrorMessage == "" {
		if message, _ := payload["message"].(string); strings.TrimSpace(message) != "" {
			outcome.ErrorMessage = strings.TrimSpace(message)
		} else if degraded {
			outcome.ErrorMessage = "tool returned a degraded result"
		} else {
			outcome.ErrorMessage = "tool returned an unsuccessful result"
		}
	}
	return outcome
}

func invalidToolTerminalOutcome(outcome toolTerminalOutcome) toolTerminalOutcome {
	outcome.Success = false
	outcome.ErrorType = "invalid_result"
	outcome.ErrorMessage = "tool returned an invalid structured result"
	return outcome
}

func firstToolOutcomeCode(payload map[string]interface{}, blocked, searchFailed bool) string {
	for _, key := range []string{"degradation_reason", "reason", "error_code", "code"} {
		if value, _ := payload[key].(string); strings.TrimSpace(value) != "" {
			return normalizeToolOutcomeCode(value)
		}
	}
	if blocked {
		return "blocked"
	}
	if searchFailed {
		return "search_failed"
	}
	return "business_error"
}

func normalizeToolOutcomeCode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
			b.WriteRune(r)
		}
		if b.Len() >= 60 {
			break
		}
	}
	if b.Len() == 0 {
		return "business_error"
	}
	return b.String()
}

func marshalToolError(toolName, code, message string, retryable bool) string {
	out := toolErrorOutput{
		OK:        false,
		Tool:      toolName,
		Code:      code,
		Error:     message,
		Message:   message,
		Retryable: retryable,
		Source:    "tool_governance",
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return `{"ok":false,"error":"tool call failed","source":"tool_governance"}`
	}
	return string(raw)
}

func marshalToolQuotaError(toolName, code, errText string) string {
	out := toolErrorOutput{
		OK:        false,
		Tool:      toolName,
		Code:      code,
		Error:     errText,
		Message:   "Tool call was blocked by the current user-group quota. Explain the limit briefly and continue without this tool result.",
		Retryable: false,
		Source:    "tool_quota",
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return `{"ok":false,"error":"tool quota exceeded","source":"tool_quota"}`
	}
	return string(raw)
}

func reserveToolCall(ctx context.Context, quotaService *service.QuotaService, meta modelusage.Meta, input *compose.ToolInput) (service.ToolCallQuotaReservation, error) {
	if quotaService == nil || input == nil || meta.UserID <= 0 {
		return service.ToolCallQuotaReservation{}, nil
	}
	return quotaService.ReserveToolCall(ctx, service.ToolCallQuotaInput{
		UserID:    meta.UserID,
		SessionID: meta.SessionID,
		RunID:     meta.RunID,
		CallID:    input.CallID,
		ToolName:  input.Name,
	})
}

func finishToolUsage(usageService *modelusage.Service, reservation service.ToolCallQuotaReservation, meta modelusage.Meta, input *compose.ToolInput, success bool, contextTokens int, truncated bool, duration time.Duration, errorType, errorMessage string) {
	if usageService == nil || input == nil {
		return
	}
	usageService.FinishToolSync(modelusage.ToolEvent{
		ID:            reservation.ID,
		UserID:        meta.UserID,
		SessionID:     meta.SessionID,
		RunID:         meta.RunID,
		CallID:        input.CallID,
		ToolKey:       input.Name,
		Success:       success,
		ContextTokens: contextTokens,
		Truncated:     truncated,
		DurationMs:    duration.Milliseconds(),
		ErrorType:     errorType,
		ErrorMessage:  sanitizeToolDiagnostic(errorMessage),
	})
}

func quotaErrorText(err error) (string, string) {
	var quotaErr *service.QuotaError
	if errors.As(err, &quotaErr) {
		return quotaErr.Message, quotaErr.Code
	}
	if err == nil {
		return "quota exceeded", "quota_exceeded"
	}
	return err.Error(), "quota_error"
}

func toolUsageErrorType(err error, ctx context.Context) string {
	if modelusage.ErrorType(err) == "first_output_timeout" {
		return "first_output_timeout"
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	return "tool_error"
}

func sanitizeToolDiagnostic(value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.TrimSpace(value))
	return sensitiveToolDiagnosticPattern.ReplaceAllString(value, "$1=[redacted]")
}
