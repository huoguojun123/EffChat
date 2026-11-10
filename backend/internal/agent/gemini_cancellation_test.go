package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/service"
)

func TestGeminiCancelDuringPartialEvent(t *testing.T) {
	partialSent := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"hello\"}]}}]}\n\ndata: {\"candidates\":")
		w.(http.Flusher).Flush()
		close(partialSent)
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	a := NewEinoAgent(service.NewChannelService(nil), nil, 4096, nil, nil, nil, nil, nil, nil)
	cm, err := a.buildChatModel(ctx, &ChatRequest{
		ModelID: "gemini-4-argon", Provider: "google",
		RuntimeChannel: &model.AIChannel{Adapter: service.AdapterGoogle, BaseURL: server.URL, APIKey: "fixture"},
	}, modelbank.SearchDecision{})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := cm.Stream(ctx, []*schema.Message{schema.UserMessage("fixture")})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	first, err := reader.Recv()
	if err != nil || first.Content != "hello" {
		t.Fatalf("first chunk = %v / %v", first, err)
	}
	<-partialSent
	cancel()
	_, err = reader.Recv()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("partial-event cancellation = %v, want context.Canceled", err)
	}
}
