package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/huoguojun123/EffChat/internal/model"
)

func TestAcceptedSetupFailureHasDurableMessageAndCanonicalReplay(t *testing.T) {
	quota, messages, userID, session := setupChatRunAdmission(t, "setup_failure")
	const runID = "setup-failure"
	user := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"user","content":"fixture input"}`)}
	if _, err := quota.AdmitChatMessage(context.Background(), admissionInput(userID, session.ID, runID, "send", "v1:setup", true), user); err != nil {
		t.Fatal(err)
	}
	input := ChatRunTransitionInput{RunID: runID, Status: "failed", PublicErrorCode: "conversation_context_incomplete", PublicErrorMessage: "本轮消息上下文未完整就绪，请重试", TerminalEvent: json.RawMessage(`{"event":"error","data":{"code":"conversation_context_incomplete","error":"本轮消息上下文未完整就绪，请重试","retryable":true,"request_id":"fixture-request"}}`), ExpiresAt: time.Now().Add(time.Minute)}
	if _, err := quota.db.Exec(`
		CREATE FUNCTION reject_fixture_failure() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.message_data->>'role'='assistant' THEN RAISE EXCEPTION 'fixture persistence failure'; END IF; RETURN NEW; END $$;
		CREATE TRIGGER reject_fixture_failure BEFORE INSERT ON messages FOR EACH ROW EXECUTE FUNCTION reject_fixture_failure();
	`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := quota.TransitionChatRun(context.Background(), input); err == nil {
		t.Fatal("failed message persistence unexpectedly committed terminal")
	}
	stillRunning, err := quota.GetChatRun(context.Background(), runID)
	if err != nil || stillRunning.Status != "running" {
		t.Fatalf("failed transaction changed run: %+v %v", stillRunning, err)
	}
	if _, err := quota.db.Exec(`DROP TRIGGER reject_fixture_failure ON messages; DROP FUNCTION reject_fixture_failure();`); err != nil {
		t.Fatal(err)
	}
	first, changed, err := quota.TransitionChatRun(context.Background(), input)
	if err != nil || !changed {
		t.Fatalf("terminal changed=%t err=%v", changed, err)
	}
	for i := 0; i < 2; i++ {
		history, err := messages.ListMessageWindow(session.ID, MessageWindowLatest, 0, 16)
		if err != nil {
			t.Fatal(err)
		}
		if len(history.Messages) != 2 {
			t.Fatalf("cold history has %d messages, want user and durable error", len(history.Messages))
		}
		failure := history.Messages[1]
		var data struct {
			Content  string
			Metadata struct {
				Ephemeral bool   `json:"ephemeral_error"`
				Code      string `json:"error_code"`
			}
		}
		if err := json.Unmarshal(failure.MessageData, &data); err != nil {
			t.Fatal(err)
		}
		if failure.ID != first.TerminalMessageID || failure.AnswerAttemptID == nil || !data.Metadata.Ephemeral || data.Metadata.Code != input.PublicErrorCode || data.Content != input.PublicErrorMessage {
			t.Fatalf("durable failure mismatch: %+v %s", failure, data.Content)
		}
		replayed, changed, err := quota.TransitionChatRun(context.Background(), input)
		if err != nil || changed || replayed.TerminalMessageID != first.TerminalMessageID {
			t.Fatalf("replay changed=%t record=%+v err=%v", changed, replayed, err)
		}
	}
	var reserved, released bool
	var attemptStatus string
	if err := quota.db.QueryRow(`SELECT r.message_reserved,r.released_at IS NOT NULL,a.status FROM chat_run_reservations r JOIN answer_attempts a ON a.run_id=r.run_id WHERE r.run_id=$1`, runID).Scan(&reserved, &released, &attemptStatus); err != nil {
		t.Fatal(err)
	}
	if reserved || !released || attemptStatus != AnswerAttemptStatusFailed {
		t.Fatalf("reserved=%t released=%t attempt=%s", reserved, released, attemptStatus)
	}
}

func TestTerminalWithoutAcceptedChatDoesNotCreateErrorMessage(t *testing.T) {
	for _, tc := range []struct {
		name, kind, status string
		bound              bool
	}{
		{"before_admission", "chat", "failed", false},
		{"compaction", "compaction", "failed", true},
		{"cancelled", "chat", "canceled", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			quota, messages, userID, session := setupChatRunAdmission(t, "terminal_scope")
			input := admissionInput(userID, session.ID, "terminal-scope", "send", "v1:scope", false)
			input.Kind = tc.kind
			if tc.kind == "compaction" {
				input.Operation = "compaction"
			}
			if _, err := quota.ReserveChatRun(context.Background(), input); err != nil {
				t.Fatal(err)
			}
			want := 0
			if tc.bound {
				user := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"user","content":"scope fixture"}`)}
				if err := messages.Create(user); err != nil {
					t.Fatal(err)
				}
				if _, err := quota.BindChatRunUserMessage(context.Background(), input.RunID, user.ID); err != nil {
					t.Fatal(err)
				}
				want = 1
			}
			_, changed, err := quota.TransitionChatRun(context.Background(), ChatRunTransitionInput{RunID: input.RunID, Status: tc.status, PublicErrorCode: "fixture", PublicErrorMessage: "fixture failure", ExpiresAt: time.Now().Add(time.Minute)})
			if err != nil || !changed {
				t.Fatalf("terminal changed=%t err=%v", changed, err)
			}
			window, err := messages.ListMessageWindow(session.ID, MessageWindowLatest, 0, 16)
			if err != nil || len(window.Messages) != want {
				t.Fatalf("unexpected error message: %+v err=%v", window, err)
			}
		})
	}
}
