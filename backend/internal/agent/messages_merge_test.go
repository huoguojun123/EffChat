package agent

import (
	"testing"

	"github.com/cloudwego/eino/schema"
)

func userText(s string) *schema.Message {
	return &schema.Message{Role: schema.User, Content: s}
}

func userImage(text, imgURL string) *schema.Message {
	parts := make([]schema.MessageInputPart, 0, 2)
	if text != "" {
		parts = append(parts, schema.MessageInputPart{Type: schema.ChatMessagePartTypeText, Text: text})
	}
	parts = append(parts, schema.MessageInputPart{
		Type: schema.ChatMessagePartTypeImageURL,
		Image: &schema.MessageInputImage{
			MessagePartCommon: schema.MessagePartCommon{
				URL:      &imgURL,
				MIMEType: "image/png",
			},
			Detail: schema.ImageURLDetailAuto,
		},
	})
	return &schema.Message{Role: schema.User, UserInputMultiContent: parts}
}

func TestMergeConsecutiveUserMessages_TextOnly(t *testing.T) {
	in := []*schema.Message{userText("你好"), userText("？"), userText("？")}
	out := mergeConsecutiveUserMessages(in)
	if len(out) != 1 {
		t.Fatalf("want 1 merged message, got %d", len(out))
	}
	if out[0].Content != "你好\n\n？\n\n？" {
		t.Errorf("merged content = %q", out[0].Content)
	}
	if len(out[0].MultiContent) != 0 {
		t.Error("text-only merge must not produce MultiContent")
	}
	if len(out[0].UserInputMultiContent) != 0 {
		t.Error("text-only merge must not produce UserInputMultiContent")
	}
}

func TestMergeConsecutiveUserMessages_ConsecutiveImagesMerge(t *testing.T) {
	in := []*schema.Message{
		userImage("第一张", "data:image/png;base64,AAA"),
		userImage("第二张", "data:image/png;base64,BBB"),
	}
	out := mergeConsecutiveUserMessages(in)
	if len(out) != 1 {
		t.Fatalf("want 1 merged message, got %d", len(out))
	}
	m := out[0]
	if m.Content != "" {
		t.Errorf("merged multimodal message must have empty Content, got %q", m.Content)
	}
	// 两条各 (text + image) → 合并后 4 个片段，顺序保留。
	if len(m.UserInputMultiContent) != 4 {
		t.Fatalf("want 4 parts, got %d: %+v", len(m.UserInputMultiContent), m.UserInputMultiContent)
	}
	if m.UserInputMultiContent[0].Text != "第一张" || m.UserInputMultiContent[2].Text != "第二张" {
		t.Errorf("text parts out of order: %+v", m.UserInputMultiContent)
	}
	if m.UserInputMultiContent[1].Type != schema.ChatMessagePartTypeImageURL ||
		m.UserInputMultiContent[3].Type != schema.ChatMessagePartTypeImageURL {
		t.Errorf("image parts misplaced: %+v", m.UserInputMultiContent)
	}
}

func TestMergeConsecutiveUserMessages_MixedTextThenImage(t *testing.T) {
	in := []*schema.Message{
		userText("先说一句"),
		userImage("再发图", "data:image/png;base64,CCC"),
	}
	out := mergeConsecutiveUserMessages(in)
	if len(out) != 1 {
		t.Fatalf("want 1 merged message, got %d", len(out))
	}
	m := out[0]
	if m.Content != "" {
		t.Errorf("merged message with image must have empty Content, got %q", m.Content)
	}
	// 文本条 → 1 文本片段；图片条 → text + image。共 3。
	if len(m.UserInputMultiContent) != 3 {
		t.Fatalf("want 3 parts, got %d: %+v", len(m.UserInputMultiContent), m.UserInputMultiContent)
	}
	if m.UserInputMultiContent[0].Text != "先说一句" {
		t.Errorf("first part should be leading text, got %+v", m.UserInputMultiContent[0])
	}
}

func TestMergeConsecutiveUserMessages_AssistantBreaksRun(t *testing.T) {
	in := []*schema.Message{
		userText("问题一"),
		{Role: schema.Assistant, Content: "回答"},
		userText("问题二"),
	}
	out := mergeConsecutiveUserMessages(in)
	if len(out) != 3 {
		t.Fatalf("assistant between users must prevent merge, got %d messages", len(out))
	}
}
