package service

import (
	"encoding/json"
	"testing"
	"time"
)

// markCompactionSummary 应在 metadata 中注入 compaction_summary=true，
// 且保留原有 metadata 字段；非法 JSON 原样返回。
func TestMarkCompactionSummary(t *testing.T) {
	t.Run("injects flag and preserves existing metadata", func(t *testing.T) {
		in := []byte(`{"role":"user","content":"summary","metadata":{"foo":"bar"}}`)
		out := markCompactionSummary(in, CompactionKindManual, 42)

		var data map[string]interface{}
		if err := json.Unmarshal(out, &data); err != nil {
			t.Fatalf("unmarshal output: %v", err)
		}
		meta, ok := data["metadata"].(map[string]interface{})
		if !ok {
			t.Fatalf("metadata missing or wrong type: %#v", data["metadata"])
		}
		if meta["compaction_summary"] != true {
			t.Errorf("compaction_summary = %v, want true", meta["compaction_summary"])
		}
		if meta["compaction_kind"] != CompactionKindManual {
			t.Errorf("compaction_kind = %v, want manual", meta["compaction_kind"])
		}
		if meta["compaction_before_message_id"] != float64(42) {
			t.Errorf("compaction_before_message_id = %v, want 42", meta["compaction_before_message_id"])
		}
		if meta["foo"] != "bar" {
			t.Errorf("existing metadata lost: foo = %v", meta["foo"])
		}
		if data["content"] != "summary" {
			t.Errorf("content altered: %v", data["content"])
		}
	})

	t.Run("auto kind recorded", func(t *testing.T) {
		out := markCompactionSummary([]byte(`{"role":"user","content":"x"}`), CompactionKindAuto, 7)
		var data map[string]interface{}
		_ = json.Unmarshal(out, &data)
		meta, _ := data["metadata"].(map[string]interface{})
		if meta["compaction_kind"] != CompactionKindAuto {
			t.Errorf("compaction_kind = %v, want auto", meta["compaction_kind"])
		}
	})

	t.Run("creates metadata when absent", func(t *testing.T) {
		out := markCompactionSummary([]byte(`{"role":"user","content":"x"}`), "", 0)
		var data map[string]interface{}
		if err := json.Unmarshal(out, &data); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		meta, ok := data["metadata"].(map[string]interface{})
		if !ok || meta["compaction_summary"] != true {
			t.Errorf("expected compaction_summary=true, got metadata=%#v", data["metadata"])
		}
	})

	t.Run("returns input unchanged on invalid json", func(t *testing.T) {
		in := []byte(`not json`)
		if got := markCompactionSummary(in, CompactionKindAuto, 9); string(got) != string(in) {
			t.Errorf("invalid json should pass through, got %q", got)
		}
	})
}

// Start 未指定 kind 时应回落为 chat；显式 compaction 应被保留。
func TestRunHubStartKind(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)

	chat, err := hub.Start(1, 2, 3, "", "")
	if err != nil {
		t.Fatalf("start chat: %v", err)
	}
	if chat.Kind != RunKindChat {
		t.Errorf("default kind = %q, want %q", chat.Kind, RunKindChat)
	}
	hub.Complete(chat.RunID, nil, nil)

	comp, err := hub.Start(1, 2, 0, "", RunKindCompaction)
	if err != nil {
		t.Fatalf("start compaction: %v", err)
	}
	if comp.Kind != RunKindCompaction {
		t.Errorf("kind = %q, want %q", comp.Kind, RunKindCompaction)
	}
}

func TestRunHubCancel(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)
	run, err := hub.Start(10, 20, 0, "run-x", "")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	canceled := make(chan struct{}, 1)
	hub.Bind(run.RunID, func() { canceled <- struct{}{} })

	// 归属不符：不应取消。
	if hub.Cancel(run.RunID, 999, 20) {
		t.Fatal("cancel succeeded with wrong session id")
	}
	if hub.Cancel(run.RunID, 10, 999) {
		t.Fatal("cancel succeeded with wrong user id")
	}

	// 归属正确：取消成功且 cancel 函数被调用。
	if !hub.Cancel(run.RunID, 10, 20) {
		t.Fatal("cancel failed for owner")
	}
	select {
	case <-canceled:
	default:
		t.Fatal("cancel func not invoked")
	}

	// 未知 run：返回 false。
	if hub.Cancel("nope", 10, 20) {
		t.Fatal("cancel succeeded for unknown run")
	}
}

func TestRunHubCancelAfterComplete(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)
	run, err := hub.Start(1, 2, 0, "done", "")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	hub.Bind(run.RunID, func() {})
	hub.Complete(run.RunID, nil, nil)

	// 已结束的 run 不可取消。
	if hub.Cancel(run.RunID, 1, 2) {
		t.Fatal("cancel succeeded on completed run")
	}
}

func TestRunHubCancelByUserCancelsBoundAndPendingRuns(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)
	bound, err := hub.Start(1, 42, 0, "bound", RunKindChat)
	if err != nil {
		t.Fatalf("start bound run: %v", err)
	}
	pending, err := hub.Start(2, 42, 0, "pending", RunKindCompaction)
	if err != nil {
		t.Fatalf("start pending run: %v", err)
	}
	if _, err := hub.Start(3, 99, 0, "other", RunKindChat); err != nil {
		t.Fatalf("start other run: %v", err)
	}

	boundCanceled := make(chan struct{}, 1)
	hub.Bind(bound.RunID, func() { boundCanceled <- struct{}{} })
	if got := hub.CancelByUser(42); got != 2 {
		t.Fatalf("canceled = %d, want 2", got)
	}
	select {
	case <-boundCanceled:
	default:
		t.Fatal("bound run was not canceled")
	}

	pendingCanceled := make(chan struct{}, 1)
	hub.Bind(pending.RunID, func() { pendingCanceled <- struct{}{} })
	select {
	case <-pendingCanceled:
	default:
		t.Fatal("pending run was not canceled after binding")
	}
	if hub.CancelByUser(99) != 1 {
		t.Fatal("other user run was not canceled")
	}
	hub.Complete(bound.RunID, nil, nil)
	snapshot, ok := hub.Get(bound.RunID, 1, 42)
	if !ok || snapshot.Status != RunStatusCanceled {
		t.Fatalf("account-canceled run was overwritten: %#v", snapshot)
	}
}
