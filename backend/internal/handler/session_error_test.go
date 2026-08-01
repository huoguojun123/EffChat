package handler

import (
	"encoding/json"
	"errors"
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
