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
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
)

func TestConfigErrorClassificationHidesInternalDetails(t *testing.T) {
	tests := []struct {
		name       string
		write      func(*gin.Context, error)
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "config invalid", write: func(c *gin.Context, err error) { writeConfigError(c, "update", err) }, err: fmt.Errorf("%w: system_name is required", repository.ErrConfigInvalid), wantStatus: http.StatusBadRequest, wantCode: "config_invalid"},
		{name: "model invalid", write: func(c *gin.Context, err error) { writeConfigError(c, "update", err) }, err: fmt.Errorf("%w: default model is unavailable", service.ErrModelInvalid), wantStatus: http.StatusBadRequest, wantCode: "config_invalid"},
		{name: "config internal", write: func(c *gin.Context, err error) { writeConfigError(c, "update", err) }, err: errors.New("postgres://secret@internal/private/config"), wantStatus: http.StatusInternalServerError, wantCode: "config_update_failed"},
		{name: "tool invalid", write: func(c *gin.Context, err error) { writeToolConfigError(c, "save", err) }, err: fmt.Errorf("%w: unknown tool", service.ErrToolConfigInvalid), wantStatus: http.StatusBadRequest, wantCode: "tool_config_invalid"},
		{name: "tool rollback missing", write: func(c *gin.Context, err error) { writeToolConfigError(c, "rollback", err) }, err: fmt.Errorf("%w: event", repository.ErrNotFound), wantStatus: http.StatusNotFound, wantCode: "governance_event_not_found"},
		{name: "tool rollback conflict", write: func(c *gin.Context, err error) { writeToolConfigError(c, "rollback", err) }, err: fmt.Errorf("%w: stale", repository.ErrGovernanceConflict), wantStatus: http.StatusConflict, wantCode: "governance_rollback_conflict"},
		{name: "tool internal", write: func(c *gin.Context, err error) { writeToolConfigError(c, "save", err) }, err: errors.New("postgres://secret@internal/private/tool"), wantStatus: http.StatusInternalServerError, wantCode: "tool_config_save_failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPatch, "/api/v1/admin/config/example", nil)
			ctx.Set("request_id", "req-config")
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
