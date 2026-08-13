package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/service"
)

func TestSessionAndMessageReadErrorClassificationHidesInternalDetails(t *testing.T) {
	tests := []struct {
		name       string
		write      func(*gin.Context, error)
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "session missing", write: func(c *gin.Context, err error) { writeSessionLookupError(c, "load", err) }, err: service.ErrSessionNotFound, wantStatus: http.StatusNotFound, wantCode: "session_not_found"},
		{name: "session internal", write: func(c *gin.Context, err error) { writeSessionLookupError(c, "load", err) }, err: errors.New("postgres://secret@internal/private/session"), wantStatus: http.StatusInternalServerError, wantCode: "session_load_failed"},
		{name: "turn missing", write: func(c *gin.Context, err error) { writeMessageReadError(c, "window", err) }, err: service.ErrConversationTurnNotFound, wantStatus: http.StatusNotFound, wantCode: "conversation_turn_not_found"},
		{name: "message internal", write: func(c *gin.Context, err error) { writeMessageReadError(c, "list", err) }, err: errors.New("postgres://secret@internal/private/message"), wantStatus: http.StatusInternalServerError, wantCode: "message_list_failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/sessions/1/messages", nil)
			ctx.Set("request_id", "req-session")
			tt.write(ctx, tt.err)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.wantStatus)
			}
			if strings.Contains(recorder.Body.String(), "secret") || strings.Contains(recorder.Body.String(), "/private/") {
				t.Fatalf("response leaked internal error: %s", recorder.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body["code"] != tt.wantCode || body["retryable"] != (tt.wantStatus >= 500) {
				t.Fatalf("response = %#v", body)
			}
		})
	}
}

func TestSessionMutationErrorClassificationHidesInternalDetails(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		wantStatus    int
		wantCode      string
		wantError     string
		wantRequestID bool
	}{
		{name: "invalid input", err: fmt.Errorf("%w: max_tokens must be positive", service.ErrSessionInvalid), wantStatus: http.StatusBadRequest, wantCode: "session_invalid", wantError: "max_tokens must be positive"},
		{name: "default model missing", err: service.ErrDefaultModelNotConfigured, wantStatus: http.StatusBadRequest, wantCode: "default_model_not_configured", wantError: "default model is not configured"},
		{name: "missing session", err: service.ErrSessionNotFound, wantStatus: http.StatusNotFound, wantCode: "session_not_found", wantError: "session not found"},
		{name: "runtime model", err: &service.RuntimeModelError{Code: "session_model_disabled", Message: "model unavailable", Provider: "test-provider", ModelID: "test-model"}, wantStatus: http.StatusBadRequest, wantCode: "session_model_disabled", wantError: "model unavailable"},
		{name: "runtime unavailable", err: &service.RuntimeModelError{Code: "model_runtime_unavailable", Message: "model runtime unavailable", Retryable: true}, wantStatus: http.StatusServiceUnavailable, wantCode: "model_runtime_unavailable", wantError: "model runtime unavailable"},
		{name: "internal failure", err: errors.New("postgres://secret@internal/private/session"), wantStatus: http.StatusInternalServerError, wantCode: "session_update_failed", wantError: "failed to update session", wantRequestID: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPatch, "/api/v1/sessions/1", nil)
			ctx.Set("request_id", "req-session-mutation")
			writeSessionMutationError(ctx, "update", tt.err)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.wantStatus)
			}
			if strings.Contains(recorder.Body.String(), "secret") || strings.Contains(recorder.Body.String(), "/private/") {
				t.Fatalf("response leaked internal error: %s", recorder.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body["code"] != tt.wantCode || body["error"] != tt.wantError || body["retryable"] != (tt.wantStatus >= 500) {
				t.Fatalf("response = %#v", body)
			}
			if tt.wantRequestID && body["request_id"] != "req-session-mutation" {
				t.Fatalf("request id missing from server error: %#v", body)
			}
		})
	}
}

func TestListSessionsRejectsInvalidQuery(t *testing.T) {
	queries := []string{
		"limit=0",
		"limit=101",
		"limit=not-a-number",
		"offset=-1",
		"offset=not-a-number",
		"folder_id=0",
		"folder_id=unknown",
	}
	for _, query := range queries {
		t.Run(query, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/sessions?"+query, nil)

			ListSessionsHandler(nil)(ctx)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body["code"] != "session_list_query_invalid" || body["retryable"] != false {
				t.Fatalf("response = %#v", body)
			}
		})
	}
}

func TestRuntimeModelErrorFallbackHidesInternalDetails(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/sessions/1/messages", nil)
	ctx.Set("request_id", "req-model-validation")

	writeRuntimeModelError(ctx, errors.New("postgres://secret@internal/private/model"))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "secret") || strings.Contains(recorder.Body.String(), "/private/") {
		t.Fatalf("response leaked internal error: %s", recorder.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["code"] != "model_validation_failed" || body["retryable"] != true || body["request_id"] != "req-model-validation" {
		t.Fatalf("response = %#v", body)
	}
}
