package service

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConversationTurnPreviewBoundsIndexPayload(t *testing.T) {
	turns := make([]ConversationTurnResponse, 500)
	for index := range turns {
		turns[index] = ConversationTurnResponse{
			ID:            int64(index + 1),
			Sequence:      int64(index + 1),
			UserMessageID: int64(index + 1),
			UserPreview:   conversationTurnPreview(strings.Repeat("长会话导航", 40)),
			CreatedAt:     "2026-07-26T00:00:00Z",
		}
	}
	payload, err := json.Marshal(ConversationTurnPage{Turns: turns, Total: 500})
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) >= 128*1024 {
		t.Fatalf("500-turn index payload = %d bytes", len(payload))
	}
	if got := len([]rune(conversationTurnPreview(strings.Repeat("a", 200)))); got != 110 {
		t.Fatalf("ASCII preview length = %d", got)
	}
}
