package streaming

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func createSSEWriter(t *testing.T) (*SSEWriter, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	writer, err := NewSSEWriter(c)
	if err != nil {
		t.Fatalf("NewSSEWriter: %v", err)
	}
	return writer, w
}

func TestSSEWriter_Headers(t *testing.T) {
	_, w := createSSEWriter(t)

	checks := map[string]string{
		"Content-Type":      "text/event-stream",
		"Cache-Control":     "no-cache",
		"Connection":        "keep-alive",
		"X-Accel-Buffering": "no",
	}
	for key, want := range checks {
		got := w.Header().Get(key)
		if got != want {
			t.Errorf("header %s: want %q, got %q", key, want, got)
		}
	}
}

func TestSSEWriter_WriteEvent_Format(t *testing.T) {
	writer, w := createSSEWriter(t)

	err := writer.WriteEvent("content_delta", ContentDeltaEvent{Delta: "hello"})
	if err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}

	body := w.Body.String()

	if !strings.Contains(body, "event: content_delta\n") {
		t.Errorf("missing event line in: %q", body)
	}
	if !strings.Contains(body, `data: {"delta":"hello"}`) {
		t.Errorf("missing data line in: %q", body)
	}
	if !strings.HasSuffix(body, "\n\n") {
		t.Errorf("missing double newline terminator in: %q", body)
	}
}

func TestSSEWriter_WriteEvent_NoEventName(t *testing.T) {
	writer, w := createSSEWriter(t)

	err := writer.WriteEvent("", map[string]string{"key": "val"})
	if err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}

	body := w.Body.String()
	if strings.Contains(body, "event:") {
		t.Errorf("empty event name should not produce event line: %q", body)
	}
	if !strings.Contains(body, `data: {"key":"val"}`) {
		t.Errorf("missing data line: %q", body)
	}
}

func TestSSEWriter_WriteError(t *testing.T) {
	writer, w := createSSEWriter(t)

	err := writer.WriteError("something went wrong", map[string]interface{}{"code": "test_error"})
	if err != nil {
		t.Fatalf("WriteError: %v", err)
	}

	body := w.Body.String()
	if !strings.Contains(body, "event: error\n") {
		t.Errorf("WriteError should emit event=error: %q", body)
	}
	if !strings.Contains(body, `"error":"something went wrong"`) {
		t.Errorf("WriteError should include error message: %q", body)
	}
	if !strings.Contains(body, `"code":"test_error"`) {
		t.Errorf("WriteError should include structured fields: %q", body)
	}
}

func TestSSEWriter_MultipleEvents(t *testing.T) {
	writer, w := createSSEWriter(t)

	writer.WriteEvent(EventMessageStart, MessageStartEvent{MessageID: 1})
	writer.WriteEvent(EventContentDelta, ContentDeltaEvent{Delta: "hi"})
	writer.WriteEvent(EventMessageComplete, MessageCompleteEvent{MessageID: 1, FinishReason: "stop"})

	body := w.Body.String()
	events := strings.Split(strings.TrimSpace(body), "\n\n")
	if len(events) != 3 {
		t.Errorf("want 3 events, got %d in: %q", len(events), body)
	}
}

func TestSSEWriter_DropsEventRejectedByRecorder(t *testing.T) {
	writer, w := createSSEWriter(t)
	writer.SetEventHook(func(string, interface{}) bool { return false })

	if err := writer.WriteEvent(EventContentDelta, ContentDeltaEvent{Delta: "late"}); err != nil {
		t.Fatal(err)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("rejected event reached client: %q", w.Body.String())
	}
}

func TestSSEWriter_Close(t *testing.T) {
	writer, _ := createSSEWriter(t)
	if err := writer.Close(); err != nil {
		t.Errorf("Close should return nil: %v", err)
	}
}
