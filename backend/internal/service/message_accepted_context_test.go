package service

import (
	"testing"

	"github.com/huoguojun123/EffChat/internal/model"
)

func TestValidateAcceptedUserMessageContextRequiresOriginalEnvelope(t *testing.T) {
	const raw = `{"role":"user","content":"完整的首轮消息\nwith unicode ✓","attachments":[{"file_id":9}]}`
	accepted := &model.Message{ID: 41, MessageData: []byte(raw)}
	for _, tc := range []struct {
		name, raw string
		id        int64
		valid     bool
	}{
		{"exact", raw, 41, true},
		{"truncated", `{"role":"user","content":"完整的首轮消息","attachments":[{"file_id":9}]}`, 41, false},
		{"changed_role", `{"role":"assistant","content":"完整的首轮消息\nwith unicode ✓","attachments":[{"file_id":9}]}`, 41, false},
		{"changed_attachment", `{"role":"user","content":"完整的首轮消息\nwith unicode ✓","attachments":[{"file_id":10}]}`, 41, false},
		{"absent", raw, 42, false},
		{"malformed", `{`, 41, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAcceptedUserMessageContext([]*model.Message{{ID: tc.id, MessageData: []byte(tc.raw)}}, accepted)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t err=%v", tc.valid, err)
			}
		})
	}
}
