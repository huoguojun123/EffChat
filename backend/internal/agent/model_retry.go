package agent

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/huoguojun123/EffChat/internal/modelstream"
	"github.com/huoguojun123/EffChat/internal/providerhttp"
	openaisdk "github.com/openai/openai-go/v3"
	"google.golang.org/genai"
)

const (
	maxModelRetries    = 1
	maxModelRetryDelay = 30 * time.Second
)

type ModelRetryTrace struct {
	Attempt     int
	MaxAttempts int
	Delay       time.Duration
	Category    RuntimeErrorCategory
}

type modelErrorClassification struct {
	Code       string
	Message    string
	Diagnostic string
	Category   RuntimeErrorCategory
	Retryable  bool
	HTTPStatus int
	RetryAfter time.Duration
}

var upstreamRequestIDPattern = regexp.MustCompile(`(?i)request[_ -]?id\s*[:=]\s*([A-Za-z0-9._-]{6,128})`)

func transientModelRetryConfig(observer func(ModelRetryTrace)) *adk.ModelRetryConfig {
	return &adk.ModelRetryConfig{
		MaxRetries: maxModelRetries,
		ShouldRetry: func(ctx context.Context, retryCtx *adk.RetryContext) *adk.RetryDecision {
			if ctx.Err() != nil || retryCtx == nil || retryCtx.Err == nil || hasModelOutput(retryCtx.OutputMessage) {
				return &adk.RetryDecision{}
			}
			classification := classifyModelRuntimeError(retryCtx.Err)
			if !shouldAutomaticallyRetryModelError(classification) || retryCtx.RetryAttempt > maxModelRetries {
				return &adk.RetryDecision{}
			}
			delay := modelRetryDelay(classification, retryCtx.RetryAttempt)
			trace := ModelRetryTrace{
				Attempt:     retryCtx.RetryAttempt,
				MaxAttempts: maxModelRetries + 1,
				Delay:       delay,
				Category:    classification.Category,
			}
			if observer != nil {
				observer(trace)
			}
			log.Printf("[model_retry] retrying zero-output model failure attempt=%d category=%s delay=%s",
				trace.Attempt, trace.Category, trace.Delay)
			return &adk.RetryDecision{
				Retry:        true,
				Backoff:      delay,
				RejectReason: classification.Category,
			}
		},
	}
}

func shouldAutomaticallyRetryModelError(classification modelErrorClassification) bool {
	return classification.Retryable &&
		(classification.Category == RuntimeErrorTransient || classification.Category == RuntimeErrorConnection)
}

func modelRetryDelay(classification modelErrorClassification, attempt int) time.Duration {
	if classification.RetryAfter > 0 {
		return min(classification.RetryAfter, maxModelRetryDelay)
	}
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Second * time.Duration(1<<min(attempt-1, 3))
	return min(delay, maxModelRetryDelay)
}

func classifyModelRuntimeError(err error) modelErrorClassification {
	if err == nil {
		return modelErrorClassification{
			Code:      "model_request_failed",
			Message:   "模型请求失败，请稍后重试",
			Category:  RuntimeErrorServerUpdate,
			Retryable: true,
		}
	}

	if errors.Is(err, context.Canceled) {
		return modelErrorClassification{
			Code:     "model_request_canceled",
			Message:  "模型请求已取消",
			Category: RuntimeErrorConnection,
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return modelErrorClassification{
			Code:      "model_connection_failed",
			Message:   "模型服务响应超时，请稍后重试",
			Category:  RuntimeErrorConnection,
			Retryable: true,
		}
	}

	status, headers, detail := structuredModelHTTPError(err)
	if status > 0 {
		if providerhttp.IsAnthropicTransportError(headers) {
			return modelErrorClassification{
				Code:       "model_connection_failed",
				Message:    "连接模型服务失败，请稍后重试",
				Diagnostic: modelUpstreamDiagnostic(status, "上游连接失败", ""),
				Category:   RuntimeErrorConnection,
				Retryable:  true,
				HTTPStatus: status,
			}
		}
		if status == http.StatusRequestEntityTooLarge || isModelContextErrorText(detail) {
			return modelErrorClassification{
				Code:       "model_context_exceeded",
				Message:    "本轮上下文超过模型可处理范围，请压缩会话或减少附件后重试",
				Diagnostic: modelUpstreamDiagnostic(status, "上下文超过模型限制", detail),
				Category:   RuntimeErrorContext,
				HTTPStatus: status,
			}
		}
		classification := classifyModelHTTPError(status, detail)
		classification.RetryAfter = retryAfterFromHeader(headers)
		return classification
	}

	if isModelNetworkError(err) {
		return modelErrorClassification{
			Code:      "model_connection_failed",
			Message:   "连接模型服务失败，请稍后重试",
			Category:  RuntimeErrorConnection,
			Retryable: true,
		}
	}

	lower := strings.ToLower(err.Error())
	textStatus := modelHTTPStatusFromText(lower)
	if isModelContextErrorText(lower) {
		return modelErrorClassification{
			Code:       "model_context_exceeded",
			Message:    "本轮上下文超过模型可处理范围，请压缩会话或减少附件后重试",
			Diagnostic: modelUpstreamDiagnostic(textStatus, "上下文超过模型限制", lower),
			Category:   RuntimeErrorContext,
			HTTPStatus: textStatus,
		}
	}
	if isModelQuotaErrorText(lower) {
		return classifyModelHTTPError(textStatus, lower)
	}
	if isModelChannelUnavailableText(lower) {
		return classifyModelHTTPError(textStatus, lower)
	}
	if containsAny(lower,
		"connection reset", "connection refused", "connection closed",
		"unexpected eof", "temporary network", "network is unreachable",
	) {
		return modelErrorClassification{
			Code:      "model_connection_failed",
			Message:   "连接模型服务失败，请稍后重试",
			Category:  RuntimeErrorConnection,
			Retryable: true,
		}
	}
	if textStatus > 0 {
		return classifyModelHTTPError(textStatus, lower)
	}
	if containsAny(lower, "bad gateway", "service unavailable", "gateway timeout", "temporarily unavailable") {
		return modelErrorClassification{
			Code:      "model_upstream_unavailable",
			Message:   "模型服务暂时不可用，请稍后重试",
			Category:  RuntimeErrorTransient,
			Retryable: true,
		}
	}
	if containsAny(lower, "unauthorized", "forbidden", "invalid api key", "authentication") {
		return modelErrorClassification{
			Code:     "model_access_denied",
			Message:  "模型渠道鉴权或访问权限无效，请联系管理员检查配置",
			Category: RuntimeErrorAccess,
		}
	}
	if containsAny(lower, "invalid character '<'", "<html", "<!doctype", "model not found", "invalid model") {
		return modelErrorClassification{
			Code:     "model_configuration_invalid",
			Message:  "模型或渠道配置无效，请联系管理员检查配置",
			Category: RuntimeErrorConfiguration,
		}
	}
	return modelErrorClassification{
		Code:      "model_request_failed",
		Message:   "模型请求失败，请稍后重试",
		Category:  RuntimeErrorServerUpdate,
		Retryable: true,
	}
}

func classifyModelHTTPError(status int, detail string) modelErrorClassification {
	if status == http.StatusPaymentRequired || isModelQuotaErrorText(detail) {
		return modelErrorClassification{
			Code:       "model_quota_exceeded",
			Message:    "上游模型额度不足，请在模型网关补充额度后重试",
			Diagnostic: modelUpstreamDiagnostic(status, "上游额度不足", detail),
			Category:   RuntimeErrorAccess,
			HTTPStatus: status,
		}
	}
	if isModelChannelUnavailableText(detail) {
		return modelErrorClassification{
			Code:       "model_channel_unavailable",
			Message:    "当前模型暂无可用上游渠道，请稍后重试或切换模型",
			Diagnostic: modelUpstreamDiagnostic(status, "上游无可用模型渠道", detail),
			Category:   RuntimeErrorTransient,
			Retryable:  true,
			HTTPStatus: status,
		}
	}
	classification := classifyModelHTTPStatus(status)
	classification.HTTPStatus = status
	classification.Diagnostic = modelUpstreamDiagnostic(status, modelHTTPReason(classification.Code), detail)
	return classification
}

func modelHTTPStatusFromText(value string) int {
	for _, candidate := range []int{400, 401, 402, 403, 404, 408, 409, 425, 429, 500, 502, 503, 504} {
		if strings.Contains(value, "status code: "+strconv.Itoa(candidate)) ||
			strings.Contains(value, "status="+strconv.Itoa(candidate)) ||
			strings.Contains(value, "http "+strconv.Itoa(candidate)) {
			return candidate
		}
	}
	return 0
}

func structuredModelHTTPError(err error) (int, http.Header, string) {
	var openAIErr *einoopenai.APIError
	if errors.As(err, &openAIErr) {
		return openAIErr.HTTPStatusCode, nil, openAIErr.Message
	}
	// The typed Responses component uses openai-go/v3 directly rather than
	// Eino's legacy Chat Completions APIError. Preserve status and Retry-After
	// so both OpenAI wire protocols remain under the same EffChat retry owner.
	var responsesErr *openaisdk.Error
	if errors.As(err, &responsesErr) {
		var headers http.Header
		if responsesErr.Response != nil {
			headers = responsesErr.Response.Header
		}
		return responsesErr.StatusCode, headers, responsesErr.Message
	}
	var anthropicErr *anthropic.Error
	if errors.As(err, &anthropicErr) {
		if anthropicErr.Response != nil {
			return anthropicErr.StatusCode, anthropicErr.Response.Header, ""
		}
		return anthropicErr.StatusCode, nil, ""
	}
	var geminiErr genai.APIError
	if errors.As(err, &geminiErr) {
		return geminiErr.Code, nil, geminiErr.Message
	}
	return 0, nil, ""
}

func classifyModelHTTPStatus(status int) modelErrorClassification {
	switch status {
	case http.StatusRequestTimeout, http.StatusConflict, http.StatusTooEarly:
		return modelErrorClassification{
			Code:      "model_upstream_unavailable",
			Message:   "模型服务暂时不可用，请稍后重试",
			Category:  RuntimeErrorTransient,
			Retryable: true,
		}
	case http.StatusTooManyRequests:
		return modelErrorClassification{
			Code:      "model_rate_limited",
			Message:   "模型服务请求过多，请稍后重试",
			Category:  RuntimeErrorTransient,
			Retryable: true,
		}
	case http.StatusUnauthorized, http.StatusForbidden:
		return modelErrorClassification{
			Code:     "model_access_denied",
			Message:  "模型渠道鉴权或访问权限无效，请联系管理员检查配置",
			Category: RuntimeErrorAccess,
		}
	case http.StatusBadRequest, http.StatusNotFound, http.StatusUnprocessableEntity:
		return modelErrorClassification{
			Code:     "model_configuration_invalid",
			Message:  "模型或渠道配置无效，请联系管理员检查配置",
			Category: RuntimeErrorConfiguration,
		}
	default:
		if status >= 500 && status <= 599 {
			return modelErrorClassification{
				Code:      "model_upstream_unavailable",
				Message:   "模型服务暂时不可用，请稍后重试",
				Category:  RuntimeErrorTransient,
				Retryable: true,
			}
		}
		return modelErrorClassification{
			Code:     "model_request_failed",
			Message:  "模型请求失败，请检查配置后重试",
			Category: RuntimeErrorConfiguration,
		}
	}
}

func retryAfterFromHeader(headers http.Header) time.Duration {
	if headers == nil {
		return 0
	}
	if milliseconds, err := strconv.ParseFloat(strings.TrimSpace(headers.Get("Retry-After-Ms")), 64); err == nil && milliseconds > 0 {
		return min(time.Duration(milliseconds*float64(time.Millisecond)), maxModelRetryDelay)
	}
	value := strings.TrimSpace(headers.Get("Retry-After"))
	if seconds, err := strconv.ParseFloat(value, 64); err == nil && seconds > 0 {
		return min(time.Duration(seconds*float64(time.Second)), maxModelRetryDelay)
	}
	if at, err := http.ParseTime(value); err == nil {
		return min(max(time.Until(at), 0), maxModelRetryDelay)
	}
	return 0
}

func isModelNetworkError(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ENETUNREACH) || errors.Is(err, syscall.EHOSTUNREACH) {
		return true
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func isModelContextErrorText(value string) bool {
	lower := strings.ToLower(value)
	return containsAny(lower,
		"maximum context length", "context length exceeded", "context window",
		"too many tokens", "prompt is too long", "request too large",
	)
}

func isModelQuotaErrorText(value string) bool {
	lower := strings.ToLower(value)
	return containsAny(lower,
		"token quota is not enough", "insufficient quota", "insufficient_quota",
		"insufficient balance", "balance is not enough", "not enough balance",
		"credit balance", "billing hard limit",
	)
}

func isModelChannelUnavailableText(value string) bool {
	lower := strings.ToLower(value)
	return containsAny(lower,
		"no available channel for model", "no available channels for model",
		"no channel available for model",
	)
}

func modelHTTPReason(code string) string {
	switch code {
	case "model_rate_limited":
		return "上游请求限流"
	case "model_access_denied":
		return "上游拒绝访问"
	case "model_configuration_invalid":
		return "上游拒绝请求"
	case "model_upstream_unavailable":
		return "上游服务异常"
	default:
		return "上游请求失败"
	}
}

func modelUpstreamDiagnostic(status int, reason, detail string) string {
	parts := make([]string, 0, 3)
	if status > 0 {
		parts = append(parts, "HTTP "+strconv.Itoa(status))
	}
	if reason != "" {
		parts = append(parts, reason)
	}
	if matches := upstreamRequestIDPattern.FindStringSubmatch(detail); len(matches) == 2 {
		parts = append(parts, "请求 ID "+matches[1])
	}
	return strings.Join(parts, " · ")
}

func hasModelOutput(message *schema.Message) bool {
	return modelstream.HasMeaningfulOutput(message)
}
