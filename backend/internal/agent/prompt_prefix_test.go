package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/service"
)

// Measure synthetic wire bytes as well as the decoded system prefix. JSON field
// order is not a provider cache key; these observations do not assert cache hits.
func TestDefaultPromptWirePrefix(t *testing.T) {
	bodies := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			http.Error(w, "fixture read failed", http.StatusBadRequest)
			return
		}
		bodies <- body
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"fixture","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer server.Close()
	a := NewEinoAgent(service.NewChannelService(nil), nil, 4096, nil, nil, nil, nil, nil, nil)
	type sample struct {
		wire, canonical, system, tools []byte
	}
	capture := func(t *testing.T, mutate func(*ChatRequest), memory, history, toolDescription string) sample {
		t.Helper()
		req := &ChatRequest{
			SystemName: "Fixture Assistant", ModelID: "claude-sonnet-5-5", Provider: "anthropic",
			SessionTitle: "Synthetic session", UserName: "fixture-user",
			PromptTime:     time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC),
			RuntimeChannel: &model.AIChannel{Adapter: service.AdapterAnthropic, BaseURL: server.URL, APIKey: "fixture-key"},
			EnabledSkills:  []SkillInstruction{{ID: "fixture", Name: "Fixture skill", Description: "Synthetic skill"}},
		}
		if mutate != nil {
			mutate(req)
		}
		instruction, err := buildInstruction(nil, req, modelbank.SearchDecision{}, map[string]bool{"memory": true, "skill_read": true})
		if err != nil {
			t.Fatal(err)
		}
		instruction = appendMemoryInstruction(instruction, memory)
		cm, err := a.buildChatModel(t.Context(), req, modelbank.SearchDecision{})
		if err != nil {
			t.Fatal(err)
		}
		cm, err = cm.WithTools([]*schema.ToolInfo{{Name: "memory", Desc: toolDescription, ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{})}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = cm.Generate(t.Context(), []*schema.Message{schema.SystemMessage(instruction), schema.UserMessage(history)}); err != nil {
			t.Fatal(err)
		}
		wire := <-bodies
		var body struct {
			System []struct{ Text string } `json:"system"`
			Tools  json.RawMessage         `json:"tools"`
		}
		if err := json.Unmarshal(wire, &body); err != nil {
			t.Fatal(err)
		}
		if len(body.System) != 1 || body.System[0].Text != instruction {
			t.Fatal("adapter changed the rendered instruction")
		}
		// SDK extra-field maps may serialize output_config/thinking in either
		// order. Compare decoded JSON for determinism, retaining raw wire metrics
		// separately rather than treating key order as a provider cache miss.
		var decoded any
		if err := json.Unmarshal(wire, &decoded); err != nil {
			t.Fatal(err)
		}
		canonical, err := json.Marshal(decoded)
		if err != nil {
			t.Fatal(err)
		}
		return sample{wire, canonical, []byte(body.System[0].Text), body.Tools}
	}
	base := capture(t, nil, "Synthetic memory", "Synthetic question", "Synthetic tool")
	stableEnd := bytes.Index(base.system, []byte("# Runtime"))
	priorityEnd := bytes.Index(base.system, []byte("4. This prompt"))
	if priorityEnd < 0 || stableEnd < priorityEnd+len("4. This prompt") {
		t.Errorf("runtime context interrupts stable instructions at byte %d", stableEnd)
	}
	for _, tc := range []struct {
		name     string
		mutate   func(*ChatRequest)
		memory   string
		history  string
		tool     string
		sameText bool
	}{
		{name: "repeat", sameText: true},
		{name: "date", mutate: func(r *ChatRequest) { r.PromptTime = r.PromptTime.AddDate(0, 0, 1) }},
		{name: "title", mutate: func(r *ChatRequest) { r.SessionTitle = "Renamed synthetic session" }},
		{name: "user", mutate: func(r *ChatRequest) { r.UserName = "another-fixture-user" }},
		{name: "history", history: "Different synthetic question", sameText: true},
		{name: "model", mutate: func(r *ChatRequest) { r.ModelID = "claude-opus-5-5" }},
		{name: "memory", memory: "Changed synthetic memory"},
		{name: "skills", mutate: func(r *ChatRequest) { r.EnabledSkills[0].Description = "Changed synthetic skill" }},
		{name: "tools", tool: "Changed synthetic tool", sameText: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			memory, history, tool := tc.memory, tc.history, tc.tool
			if memory == "" {
				memory = "Synthetic memory"
			}
			if history == "" {
				history = "Synthetic question"
			}
			if tool == "" {
				tool = "Synthetic tool"
			}
			got := capture(t, tc.mutate, memory, history, tool)
			prefix := func(a, b []byte) int {
				i := 0
				for i < len(a) && i < len(b) && a[i] == b[i] {
					i++
				}
				return i
			}
			p := prefix(base.system, got.system)
			if p < stableEnd || (tc.sameText && !bytes.Equal(base.system, got.system)) {
				t.Errorf("stable system prefix=%d, want at least %d", p, stableEnd)
			}
			if toolsChanged := !bytes.Equal(base.tools, got.tools); toolsChanged != (tc.name == "tools") {
				t.Error("tool schema changed outside the tools case, or did not change inside it")
			}
			if tc.name == "repeat" && !bytes.Equal(base.canonical, got.canonical) {
				t.Error("identical synthetic requests produced different JSON values")
			}
			t.Logf("wire_bytes=%d wire_sha256=%x wire_common_prefix=%d canonical_sha256=%x system_bytes=%d system_sha256=%x system_common_prefix=%d tools_sha256=%x", len(got.wire), sha256.Sum256(got.wire), prefix(base.wire, got.wire), sha256.Sum256(got.canonical), len(got.system), sha256.Sum256(got.system), p, sha256.Sum256(got.tools))
		})
	}
	custom := "Administrator policy first. Date={{date}}; title={{session_title}}."
	got := capture(t, func(r *ChatRequest) { r.RuntimePromptTemplate = custom }, "Synthetic memory", "Synthetic question", "Synthetic tool")
	if !strings.HasPrefix(string(got.system), "Administrator policy first. Date=2026-10-25; title=Synthetic session.") {
		t.Fatal("administrator template order was rewritten")
	}
}
