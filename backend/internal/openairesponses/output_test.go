package openairesponses

import (
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestMessageConverterPreservesPromptTokenDetails(t *testing.T) {
	for _, tc := range []struct {
		name  string
		usage *schema.TokenUsage
	}{
		{name: "usage absent"},
		{name: "cache fields zero", usage: &schema.TokenUsage{PromptTokens: 20, CompletionTokens: 3, TotalTokens: 23}},
		{name: "cache read and write", usage: &schema.TokenUsage{
			PromptTokens: 20, CompletionTokens: 3, TotalTokens: 23,
			PromptTokenDetails: schema.PromptTokenDetails{CachedTokens: 8, CacheWriteTokens: 6},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := &schema.AgenticMessage{
				Role:         schema.AgenticRoleTypeAssistant,
				ResponseMeta: &schema.AgenticResponseMeta{TokenUsage: tc.usage},
			}
			got, err := NewMessageConverter().Convert(input)
			if err != nil {
				t.Fatal(err)
			}
			if tc.usage == nil {
				if got.ResponseMeta.Usage != nil {
					t.Fatal("absent usage must not become observed zero usage")
				}
				return
			}
			if got.ResponseMeta.Usage == nil || *got.ResponseMeta.Usage != *tc.usage {
				t.Fatalf("usage = %+v, want %+v", got.ResponseMeta.Usage, tc.usage)
			}
		})
	}
}
