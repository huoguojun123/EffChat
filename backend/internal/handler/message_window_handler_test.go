package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/repository"
)

func TestMessageWindowQuery(t *testing.T) {
	tests := []struct {
		query  string
		mode   repository.MessageWindowMode
		target int64
		ok     bool
	}{
		{query: "", mode: repository.MessageWindowLatest, ok: true},
		{query: "latest=true", mode: repository.MessageWindowLatest, ok: true},
		{query: "before_turn_id=12", mode: repository.MessageWindowBefore, target: 12, ok: true},
		{query: "after_turn_id=13", mode: repository.MessageWindowAfter, target: 13, ok: true},
		{query: "around_turn_id=14", mode: repository.MessageWindowAround, target: 14, ok: true},
		{query: "latest=false", ok: false},
		{query: "before_turn_id=0", ok: false},
		{query: "before_turn_id=12&around_turn_id=14", ok: false},
	}
	for _, test := range tests {
		t.Run(test.query, func(t *testing.T) {
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			context.Request = httptest.NewRequest("GET", "/?"+test.query, nil)
			mode, target, err := messageWindowQuery(context)
			if (err == nil) != test.ok {
				t.Fatalf("error = %v", err)
			}
			if test.ok && (mode != test.mode || target != test.target) {
				t.Fatalf("got mode=%s target=%d", mode, target)
			}
		})
	}
}
