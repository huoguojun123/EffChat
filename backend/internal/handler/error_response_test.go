package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestWriteServerErrorHidesInternalDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/usage", nil)
	ctx.Set("request_id", "req-test-123")

	writeServerError(ctx, http.StatusInternalServerError, "usage_summary_failed", "failed to load usage", errors.New("postgres://secret@internal"))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	if strings.Contains(recorder.Body.String(), "secret") {
		t.Fatalf("response leaked internal error: %s", recorder.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["error"] != "failed to load usage" || body["code"] != "usage_summary_failed" || body["request_id"] != "req-test-123" {
		t.Fatalf("unexpected response: %#v", body)
	}
}
