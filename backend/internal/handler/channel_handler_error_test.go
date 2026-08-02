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

func TestChannelErrorClassificationHidesInternalDetails(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "invalid", err: fmt.Errorf("%w: adapter is invalid", service.ErrChannelInvalid), wantStatus: http.StatusBadRequest, wantCode: "channel_invalid"},
		{name: "not found", err: service.ErrChannelNotFound, wantStatus: http.StatusNotFound, wantCode: "channel_not_found"},
		{name: "internal", err: errors.New("postgres://secret@internal/private/channel"), wantStatus: http.StatusInternalServerError, wantCode: "channel_save_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/channels", nil)
			ctx.Set("request_id", "req-channel")
			writeChannelError(ctx, "channel", "save", tt.err)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.wantStatus)
			}
			if strings.Contains(recorder.Body.String(), "secret") || strings.Contains(recorder.Body.String(), "/private/channel") {
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
