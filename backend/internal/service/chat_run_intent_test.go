package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/huoguojun123/effchat/internal/model"
	"github.com/huoguojun123/effchat/internal/repository"
)

func TestBuildSendRunIntentNormalizesOnlySemanticRequestFields(t *testing.T) {
	session := &model.Session{MessageFormat: "v2"}
	base := BuildSendRunIntent(session, &SendMessageRequest{
		Content:        "keep exact whitespace  ",
		Attachments:    []int64{9, 4, 9},
		ThinkingEffort: "HIGH",
	})
	equivalent := BuildSendRunIntent(session, &SendMessageRequest{
		Content:        "keep exact whitespace  ",
		SchemaVersion:  "v2",
		Attachments:    []int64{9, 4},
		ThinkingEffort: "high",
	})
	if base.Operation != RunOperationSend || base.Version != ChatRunIntentVersion || base.Hash == "" {
		t.Fatalf("base intent = %+v", base)
	}
	if base.Hash != equivalent.Hash {
		t.Fatalf("equivalent send intents differ: %q != %q", base.Hash, equivalent.Hash)
	}

	for name, changed := range map[string]RunIntent{
		"content":          BuildSendRunIntent(session, &SendMessageRequest{Content: "keep exact whitespace", SchemaVersion: "v2", Attachments: []int64{9, 4}, ThinkingEffort: "high"}),
		"schema":           BuildSendRunIntent(session, &SendMessageRequest{Content: "keep exact whitespace  ", SchemaVersion: "v1", Attachments: []int64{9, 4}, ThinkingEffort: "high"}),
		"attachment order": BuildSendRunIntent(session, &SendMessageRequest{Content: "keep exact whitespace  ", SchemaVersion: "v2", Attachments: []int64{4, 9}, ThinkingEffort: "high"}),
		"thinking":         BuildSendRunIntent(session, &SendMessageRequest{Content: "keep exact whitespace  ", SchemaVersion: "v2", Attachments: []int64{9, 4}, ThinkingEffort: "low"}),
	} {
		t.Run(name, func(t *testing.T) {
			if changed.Hash == base.Hash {
				t.Fatalf("changed intent reused hash %q", base.Hash)
			}
		})
	}
}

func TestBuildOperationIntentsCannotCollideAcrossActions(t *testing.T) {
	retry := BuildRetryRunIntent(42)
	editedRetry := BuildEditRetryRunIntent(42, "changed")
	compaction := BuildCompactionRunIntent("auto", "high", 0)
	if retry.Operation != RunOperationRetry || retry.RetryTargetMessageID != 42 {
		t.Fatalf("retry intent = %+v", retry)
	}
	if compaction.Operation != RunOperationCompaction {
		t.Fatalf("compaction intent = %+v", compaction)
	}
	if editedRetry.Operation != RunOperationRetry || editedRetry.RetryTargetMessageID != 42 {
		t.Fatalf("edited retry intent = %+v", editedRetry)
	}
	if retry.Hash == "" || editedRetry.Hash == "" || compaction.Hash == "" ||
		retry.Hash == editedRetry.Hash || retry.Hash == compaction.Hash || editedRetry.Hash == compaction.Hash {
		t.Fatalf("operation hashes = retry:%q edited:%q compaction:%q", retry.Hash, editedRetry.Hash, compaction.Hash)
	}
}

func TestBuildEditRetryRunIntentIncludesExactContent(t *testing.T) {
	base := BuildEditRetryRunIntent(42, "keep whitespace  ")
	changedContent := BuildEditRetryRunIntent(42, "keep whitespace")
	changedTarget := BuildEditRetryRunIntent(43, "keep whitespace  ")
	if base.Hash == changedContent.Hash || base.Hash == changedTarget.Hash {
		t.Fatalf("edited retry hashes must include target and exact content: base=%q content=%q target=%q", base.Hash, changedContent.Hash, changedTarget.Hash)
	}
}

func TestBuildCompactionRunIntentIncludesProtectedBoundary(t *testing.T) {
	ordinary := BuildCompactionRunIntent("auto", "medium", 0)
	protected := BuildCompactionRunIntent("auto", "medium", 42)
	changedBoundary := BuildCompactionRunIntent("auto", "medium", 43)
	if ordinary.Hash == protected.Hash || protected.Hash == changedBoundary.Hash {
		t.Fatalf("compaction intent hashes must include protected boundary: ordinary=%q protected=%q changed=%q", ordinary.Hash, protected.Hash, changedBoundary.Hash)
	}
}

func TestRunHubRejectsSameRunIDWithDifferentIntent(t *testing.T) {
	hub := NewRunHub(0, 1<<20)
	first := BuildRetryRunIntent(10)
	if _, err := hub.StartWithIntent(1, 2, 0, "intent-run", RunKindChat, first); err != nil {
		t.Fatalf("start first intent: %v", err)
	}
	if _, err := hub.StartWithIntent(1, 2, 0, "intent-run", RunKindChat, first); err != nil {
		t.Fatalf("reuse matching intent: %v", err)
	}
	if _, err := hub.StartWithIntent(1, 2, 0, "intent-run", RunKindChat, BuildRetryRunIntent(11)); err != ErrRunIDConflict {
		t.Fatalf("different intent error = %v, want %v", err, ErrRunIDConflict)
	}
}

func TestRunHubRestoresDurableTerminalEvent(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)
	intent := BuildSendRunIntent(&model.Session{MessageFormat: "v1"}, &SendMessageRequest{Content: "done"})
	now := time.Now()
	event, err := json.Marshal(map[string]interface{}{
		"event": "message_complete",
		"data":  map[string]interface{}{"message_id": 99, "finish_reason": "stop"},
	})
	if err != nil {
		t.Fatal(err)
	}
	record := repository.ChatRunRecord{
		RunID: "stored-terminal", UserID: 2, SessionID: 1, Kind: RunKindChat,
		Operation: intent.Operation, IntentVersion: intent.Version, IntentHash: intent.Hash,
		Status: RunStatusCompleted, UserMessageID: 10, TerminalMessageID: 99,
		TerminalEvent: event, AcceptedAt: now, TerminalAt: &now, ExpiresAt: now.Add(time.Minute),
	}
	snapshot, err := hub.RestoreTerminal(record, intent)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != RunStatusCompleted || snapshot.UserMessageID != 10 || snapshot.TerminalMessageID != 99 {
		t.Fatalf("restored snapshot = %+v", snapshot)
	}
	events, ch, cleanup, _, err := hub.EventsAfter(record.RunID, record.SessionID, record.UserID, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if ch != nil || len(events) != 1 || events[0].Event != "message_complete" {
		t.Fatalf("restored events = %+v channel=%v", events, ch)
	}
}
