package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
	"github.com/huoguojun123/EffChat/pkg/streaming"
)

func TestADKAcceptedAttachmentContext(t *testing.T) {
	for _, content := range []string{"完整问题 with attachment ✓", ""} {
		t.Run(fmt.Sprintf("content_length_%d", len(content)), func(t *testing.T) {
			provider := newScriptedOpenAIProvider(t, func(w http.ResponseWriter, r *http.Request) {
				var payload struct {
					Messages []struct {
						Role    string
						Content string
					}
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				users := 0
				for _, m := range payload.Messages {
					if m.Role != "user" {
						continue
					}
					users++
					if !strings.Contains(m.Content, "[Attachment list]") || !strings.Contains(m.Content, "sample.docx") || (content != "" && !strings.HasPrefix(m.Content, content+"\n\n")) {
						t.Errorf("incomplete model projection: %q", m.Content)
					}
				}
				if users != 1 {
					t.Errorf("user turns = %d, want 1", users)
				}
				writeOpenAIStreamingCompletion(w, "attachment accepted")
			})
			env := setupTestEnv(t)
			h := newADKRunRegressionHarness(t, env, provider)
			session := h.createSession(t, "attachment context")
			file := &model.File{UserID: env.userID, SessionID: &session.ID, FileName: "sample.docx", FilePath: filepath.Join(t.TempDir(), "sample.txt"), FileType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", FileSize: 32, ExtractStatus: "ready"}
			if err := repository.NewFileRepository(env.db).Create(file); err != nil {
				t.Fatal(err)
			}
			runID := "accepted-attachment"
			stream := h.request(t, fmt.Sprintf("/api/v1/sessions/%d/messages/stream", session.ID), map[string]interface{}{"content": content, "attachments": []int64{file.ID}, "client_run_id": runID})
			if !strings.Contains(stream.Body.String(), "event: "+streaming.EventMessageComplete) {
				t.Fatalf("attachment run failed: %s", stream.Body.String())
			}
			if provider.calls.Load() != 1 {
				t.Fatalf("provider calls = %d", provider.calls.Load())
			}
			var stored string
			if err := env.db.QueryRow("SELECT message_data->>'content' FROM messages WHERE session_id=$1 AND role='user'", session.ID).Scan(&stored); err != nil {
				t.Fatal(err)
			}
			if stored != content {
				t.Fatalf("durable original changed: %q", stored)
			}
			// Replaying accepted admission must not duplicate a model call or turn.
			replay := h.request(t, fmt.Sprintf("/api/v1/sessions/%d/messages/stream", session.ID), map[string]interface{}{"content": content, "attachments": []int64{file.ID}, "client_run_id": runID})
			if !strings.Contains(replay.Body.String(), "event: "+streaming.EventMessageComplete) || provider.calls.Load() != 1 {
				t.Fatalf("replay failed: %s", replay.Body.String())
			}
		})
	}
}

func TestADKEditedRetryUsesBoundReplacement(t *testing.T) {
	const edited = "edited complete question ✓"
	provider := newScriptedOpenAIProvider(t, func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []struct {
				Role    string
				Content string
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		users := 0
		for _, m := range payload.Messages {
			if m.Role == "user" {
				users++
				if m.Content != edited {
					t.Errorf("executed old intent: %q", m.Content)
				}
			}
		}
		if users != 1 {
			t.Errorf("user turns=%d", users)
		}
		writeOpenAIStreamingCompletion(w, "edited answer")
	})
	env := setupTestEnv(t)
	h := newADKRunRegressionHarness(t, env, provider)
	session := h.createSession(t, "edited retry identity")
	old, err := env.messageService.CreateUserMessage(session.ID, env.userID, &service.SendMessageRequest{Content: "before", SchemaVersion: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.messageService.CreateErrorAssistantMessage(session.ID, env.userID, "synthetic failure", "v1"); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/sessions/%d/messages/%d/edit", session.ID, old.ID)
	stream := h.request(t, path, map[string]interface{}{"content": edited, "client_run_id": "edit-bound"})
	if !strings.Contains(stream.Body.String(), "event: "+streaming.EventMessageComplete) {
		t.Fatalf("edit failed: %s", stream.Body.String())
	}
	var bound, intent int64
	if err := env.db.QueryRow("SELECT user_message_id,retry_target_message_id FROM chat_run_reservations WHERE run_id='edit-bound'").Scan(&bound, &intent); err != nil {
		t.Fatal(err)
	}
	if bound == old.ID || intent != old.ID {
		t.Fatalf("bound=%d intent=%d old=%d", bound, intent, old.ID)
	}
	replay := h.request(t, path, map[string]interface{}{"content": edited, "client_run_id": "edit-bound"})
	if !strings.Contains(replay.Body.String(), "event: "+streaming.EventMessageComplete) || provider.calls.Load() != 1 {
		t.Fatalf("edit replay failed: %s", replay.Body.String())
	}
}

func TestAcceptedWorkerSetupFailureSurvivesColdHistory(t *testing.T) {
	env := setupTestEnv(t)
	session := createResumeTestSession(t, env)
	request := &service.SendMessageRequest{Content: "complete accepted fixture", SchemaVersion: "v1"}
	intent := service.BuildSendRunIntent(&session, request)
	hub := service.NewRunHub(time.Minute, 1<<20)
	quota := repository.NewQuotaRepository(env.db)
	hub.SetStore(quota)
	run, err := hub.StartWithIntent(session.ID, env.userID, 0, "worker-setup-failure", service.RunKindChat, intent)
	if err != nil {
		t.Fatal(err)
	}
	original, err := env.messageService.BuildUserMessagePreviewContext(context.Background(), session.ID, env.userID, request)
	if err != nil {
		t.Fatal(err)
	}
	var accepted *model.Message
	run, err = hub.PersistAdmission(context.Background(), run.RunID, func(ctx context.Context) (repository.ChatRunRecord, error) {
		admission, err := quota.AdmitChatMessage(ctx, repository.ChatRunReservationInput{
			RunID: run.RunID, UserID: env.userID, AuthVersion: 1, SessionID: session.ID, Kind: service.RunKindChat,
			Operation: intent.Operation, IntentVersion: intent.Version, IntentHash: intent.Hash, ReserveMessage: true, AcceptedAt: run.AcceptedAt, ExpiresAt: run.ExpiresAt,
		}, original)
		accepted = admission.Message
		return admission.Record, err
	})
	if err != nil {
		t.Fatal(err)
	}
	// Inject an admission-to-worker mismatch in memory only. The original row
	// stays intact; the real worker must fail before reaching any provider.
	corrupt := *accepted
	corrupt.MessageData = []byte(`{"role":"user","content":"truncated"}`)
	executeAgentRun(agentRunExecution{
		messageService: env.messageService, sessionService: env.sessionService, authService: env.authService,
		skillService: service.NewSkillService(nil, nil, nil), runHub: hub, sessionID: session.ID, userID: env.userID,
		userMessage: &corrupt, runSnapshot: run, requestID: "fixture-setup",
	})
	snapshot, ok := hub.Get(run.RunID, session.ID, env.userID)
	if !ok || snapshot.ErrorCode != "conversation_context_incomplete" || snapshot.TerminalMessageID <= 0 {
		t.Fatalf("terminal=%+v", snapshot)
	}
	// This is the authenticated HTTP history path used after refresh or by a
	// different client, with no local pending message or live RunHub state.
	for i := 0; i < 2; i++ {
		cold := env.doRequest(http.MethodGet, fmt.Sprintf("/api/v1/sessions/%d/messages", session.ID), nil)
		var history struct{ Messages []service.MessageResponse }
		if err := json.Unmarshal(cold.Body.Bytes(), &history); err != nil {
			t.Fatal(err)
		}
		if cold.Code != http.StatusOK || len(history.Messages) != 2 || history.Messages[1].ID != snapshot.TerminalMessageID || history.Messages[1].MessageData["content"] != "本轮消息上下文未完整就绪，请重试" {
			t.Fatalf("cold history: %s", cold.Body.String())
		}
	}
	events := runEventsForRegression(t, hub, run.RunID, session.ID, env.userID)
	if countRunEvents(events, streaming.EventError) != 1 {
		t.Fatalf("terminal events=%+v", events)
	}
}
