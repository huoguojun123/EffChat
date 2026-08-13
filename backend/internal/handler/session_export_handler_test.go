package handler

import (
	"encoding/json"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/service"
)

func TestExportSessionMarkdownHandlerUsesCompleteSelectedHistory(t *testing.T) {
	env := setupTestEnv(t)
	created := env.doRequest(http.MethodPost, "/api/v1/sessions", map[string]interface{}{
		"model_id": "gpt-4o-mini",
		"provider": env.channelKey,
		"title":    "导出：测试/会话",
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create session: status=%d body=%s", created.Code, created.Body.String())
	}
	var session model.Session
	if err := json.Unmarshal(created.Body.Bytes(), &session); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	userMessage, err := env.messageService.CreateUserMessage(session.ID, env.userID, &service.SendMessageRequest{Content: "导出问题", SchemaVersion: "v1"})
	if err != nil {
		t.Fatalf("create user message: %v", err)
	}
	first, err := env.messageService.CreateAssistantMessage(session.ID, env.userID, map[string]interface{}{"role": "assistant", "content": "未选中回答"}, "v1")
	if err != nil {
		t.Fatalf("create first answer: %v", err)
	}
	selected, err := env.messageService.CreateAssistantMessage(session.ID, env.userID, map[string]interface{}{"role": "assistant", "content": "当前选中回答"}, "v1")
	if err != nil {
		t.Fatalf("create selected answer: %v", err)
	}
	var firstAttempt, selectedAttempt int64
	if err := env.db.QueryRow(`
		INSERT INTO answer_attempts (session_id, user_message_id, attempt_number, status, selected)
		VALUES ($1, $2, 1, 'completed', false) RETURNING id
	`, session.ID, userMessage.ID).Scan(&firstAttempt); err != nil {
		t.Fatalf("create first attempt: %v", err)
	}
	if err := env.db.QueryRow(`
		INSERT INTO answer_attempts (session_id, user_message_id, attempt_number, status, selected)
		VALUES ($1, $2, 2, 'completed', true) RETURNING id
	`, session.ID, userMessage.ID).Scan(&selectedAttempt); err != nil {
		t.Fatalf("create selected attempt: %v", err)
	}
	if _, err := env.db.Exec(`
		UPDATE messages
		SET answer_attempt_id = CASE id WHEN $1::bigint THEN $2::bigint ELSE $3::bigint END
		WHERE id IN ($1::bigint, $4::bigint)
	`, first.ID, firstAttempt, selectedAttempt, selected.ID); err != nil {
		t.Fatalf("bind answer attempts: %v", err)
	}

	response := env.doRequest(http.MethodGet, "/api/v1/sessions/"+strconv.FormatInt(session.ID, 10)+"/export.md", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("export: status=%d body=%s", response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/markdown") {
		t.Fatalf("content type = %q", contentType)
	}
	_, params, err := mime.ParseMediaType(response.Header().Get("Content-Disposition"))
	if err != nil || params["filename"] != "导出：测试 会话.md" {
		t.Fatalf("content disposition=%q filename=%q err=%v", response.Header().Get("Content-Disposition"), params["filename"], err)
	}
	body := response.Body.String()
	if !strings.Contains(body, "导出问题") || !strings.Contains(body, "当前选中回答") || strings.Contains(body, "未选中回答") {
		t.Fatalf("unexpected export body:\n%s", body)
	}
}

func TestExportSessionMarkdownHandlerRejectsInvalidOptions(t *testing.T) {
	recorder := setupTestEnv(t).doRequest(http.MethodGet, "/api/v1/sessions/1/export.md?include_tools=maybe", nil)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
