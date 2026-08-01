package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSearchConversationsHandlerRejectsInvalidQueries(t *testing.T) {
	tests := []string{
		"q=a",
		"q=" + strings.Repeat("界", 121),
		"q=valid&scope=unknown",
		"q=valid&scope=folder",
		"q=valid&scope=folder&folder_id=0",
		"q=valid&limit=0",
		"q=valid&limit=51",
		"q=valid&limit=not-a-number",
	}
	for _, query := range tests {
		t.Run(query, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodGet, "/?"+query, nil)
			SearchConversationsHandler(nil)(context)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body["code"] != "conversation_search_query_invalid" || body["retryable"] != false {
				t.Fatalf("response = %#v", body)
			}
		})
	}
}
