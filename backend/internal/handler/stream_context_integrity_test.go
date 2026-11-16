package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

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
