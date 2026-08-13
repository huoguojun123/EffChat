package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
	_ "github.com/lib/pq"
)

func TestWriteAnswerAttemptSelectionErrorContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name      string
		err       error
		status    int
		code      string
		retryable bool
		requestID bool
	}{
		{name: "session missing", err: repository.ErrNotFound, status: http.StatusNotFound, code: "session_not_found"},
		{name: "attempt missing", err: repository.ErrAnswerAttemptNotFound, status: http.StatusNotFound, code: "answer_attempt_not_found"},
		{name: "not selectable", err: repository.ErrAnswerAttemptNotSelectable, status: http.StatusConflict, code: "answer_attempt_not_selectable"},
		{name: "internal", err: errors.New("postgres://fixture:secret@db.example/effchat /srv/private/attempts"), status: http.StatusInternalServerError, code: "answer_attempt_select_failed", retryable: true, requestID: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/sessions/1/answer-attempts/2/select", nil)
			ctx.Set("request_id", "req-answer-attempt")

			writeAnswerAttemptSelectionError(ctx, tc.err)

			assertAnswerAttemptErrorResponse(t, recorder, tc.status, tc.code, tc.retryable, tc.requestID)
			if strings.Contains(recorder.Body.String(), "secret") || strings.Contains(recorder.Body.String(), "/srv/private") {
				t.Fatalf("response leaked internal cause: %s", recorder.Body.String())
			}
		})
	}
}

func TestWriteAnswerAttemptDeletionErrorContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name      string
		err       error
		status    int
		code      string
		retryable bool
		requestID bool
	}{
		{name: "session missing", err: repository.ErrNotFound, status: http.StatusNotFound, code: "session_not_found"},
		{name: "attempt missing", err: repository.ErrAnswerAttemptNotFound, status: http.StatusNotFound, code: "answer_attempt_not_found"},
		{name: "not selectable", err: repository.ErrAnswerAttemptNotSelectable, status: http.StatusConflict, code: "answer_attempt_not_selectable"},
		{name: "last remaining", err: repository.ErrAnswerAttemptLastRemaining, status: http.StatusConflict, code: "answer_attempt_last_remaining"},
		{name: "internal", err: errors.New("postgres://fixture:secret@db.example/effchat /srv/private/delete-attempt"), status: http.StatusInternalServerError, code: "answer_attempt_delete_failed", retryable: true, requestID: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/1/answer-attempts/2", nil)
			ctx.Set("request_id", "req-answer-attempt-delete")

			writeAnswerAttemptDeletionError(ctx, tc.err)

			assertAnswerAttemptErrorResponse(t, recorder, tc.status, tc.code, tc.retryable, tc.requestID)
			if strings.Contains(recorder.Body.String(), "secret") || strings.Contains(recorder.Body.String(), "/srv/private") {
				t.Fatalf("response leaked internal cause: %s", recorder.Body.String())
			}
		})
	}
}

func TestSelectAnswerAttemptHandlerClassifiesClosedRepositoryAsServerFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := sql.Open("postgres", "postgres://fixture:secret@db.example/effchat?sslmode=disable")
	if err != nil {
		t.Fatalf("open database handle: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close database handle: %v", err)
	}
	messageService := service.NewMessageService(nil, nil, nil, repository.NewAnswerAttemptRepository(db))
	handler := SelectAnswerAttemptHandler(messageService, nil, nil, nil)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/sessions/1/answer-attempts/2/select", nil)
	ctx.Params = gin.Params{{Key: "id", Value: "1"}, {Key: "attempt_id", Value: "2"}}
	ctx.Set("user_id", int64(7))
	ctx.Set("request_id", "req-answer-handler")

	handler(ctx)

	assertAnswerAttemptErrorResponse(t, recorder, http.StatusInternalServerError, "answer_attempt_select_failed", true, true)
}

func TestSelectAnswerAttemptHandlerRejectsInvalidAttemptID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := SelectAnswerAttemptHandler(nil, nil, nil, nil)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/sessions/1/answer-attempts/invalid/select", nil)
	ctx.Params = gin.Params{{Key: "id", Value: "1"}, {Key: "attempt_id", Value: "invalid"}}
	ctx.Set("user_id", int64(7))

	handler(ctx)

	assertAnswerAttemptErrorResponse(t, recorder, http.StatusBadRequest, "answer_attempt_invalid", false, false)
}

func assertAnswerAttemptErrorResponse(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string, retryable, requestID bool) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status = %d body=%s, want %d", recorder.Code, recorder.Body.String(), status)
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["code"] != code || body["retryable"] != retryable {
		t.Fatalf("response = %#v", body)
	}
	if requestID && body["request_id"] == "" {
		t.Fatalf("request_id = %#v", body["request_id"])
	}
	if !requestID {
		if _, ok := body["request_id"]; ok {
			t.Fatalf("unexpected request_id: %#v", body)
		}
	}
}
