package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
)

func TestMessageMutationErrorWritersHideInternalDetails(t *testing.T) {
	tests := []struct {
		name       string
		write      func(*gin.Context)
		wantStatus int
		wantCode   string
		wantRetry  bool
	}{
		{name: "message creation internal", write: func(c *gin.Context) {
			writeMessageCreationError(c, errors.New("postgres://secret@internal/private/create"))
		}, wantStatus: http.StatusInternalServerError, wantCode: "message_create_failed", wantRetry: true},
		{name: "message creation missing session", write: func(c *gin.Context) {
			writeMessageCreationError(c, service.ErrSessionNotFound)
		}, wantStatus: http.StatusNotFound, wantCode: "session_not_found"},
		{name: "retry internal", write: func(c *gin.Context) {
			writeRetryContextError(c, false, errors.New("postgres://secret@internal/private/retry"))
		}, wantStatus: http.StatusInternalServerError, wantCode: "retry_context_load_failed", wantRetry: true},
		{name: "retry stale", write: func(c *gin.Context) { writeRetryContextError(c, false, service.ErrRetryTargetStale) }, wantStatus: http.StatusConflict, wantCode: "retry_target_stale"},
		{name: "profile internal", write: func(c *gin.Context) {
			writeUserProfileLoadError(c, errors.New("postgres://secret@internal/private/profile"))
		}, wantStatus: http.StatusInternalServerError, wantCode: "user_profile_load_failed", wantRetry: true},
		{name: "profile missing", write: func(c *gin.Context) { writeUserProfileLoadError(c, repository.ErrNotFound) }, wantStatus: http.StatusUnauthorized, wantCode: "account_unavailable"},
		{name: "undo internal", write: func(c *gin.Context) {
			writeCompactionUndoError(c, errors.New("postgres://secret@internal/private/undo"))
		}, wantStatus: http.StatusInternalServerError, wantCode: "compaction_undo_failed", wantRetry: true},
		{name: "undo missing", write: func(c *gin.Context) { writeCompactionUndoError(c, service.ErrCompactionNotFound) }, wantStatus: http.StatusNotFound, wantCode: "compaction_checkpoint_not_found"},
		{name: "undo denied", write: func(c *gin.Context) { writeCompactionUndoError(c, service.ErrCompactionUndoDenied) }, wantStatus: http.StatusConflict, wantCode: "compaction_undo_not_allowed"},
		{name: "undo stale", write: func(c *gin.Context) { writeCompactionUndoError(c, service.ErrCompactionUndoStale) }, wantStatus: http.StatusConflict, wantCode: "compaction_undo_stale"},
		{name: "stream internal", write: func(c *gin.Context) {
			writeStreamUnavailable(c, errors.New("postgres://secret@internal/private/stream"))
		}, wantStatus: http.StatusInternalServerError, wantCode: "stream_unavailable", wantRetry: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/sessions/1/messages", nil)
			ctx.Set("request_id", "req-message-mutation")
			tt.write(ctx)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.wantStatus)
			}
			if strings.Contains(recorder.Body.String(), "secret") || strings.Contains(recorder.Body.String(), "/private/") {
				t.Fatalf("response leaked internal details: %s", recorder.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body["code"] != tt.wantCode || body["retryable"] != tt.wantRetry {
				t.Fatalf("response = %#v", body)
			}
			if tt.wantStatus >= 500 && body["request_id"] != "req-message-mutation" {
				t.Fatalf("server response lacks request id: %#v", body)
			}
		})
	}
}
