package handler

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
)

func TestWriteSessionFolderErrorContract(t *testing.T) {
	internal := errors.New("postgres://fixture:secret@db.example/effchat /srv/private/folders")
	tests := []struct {
		name      string
		err       error
		status    int
		code      string
		retryable bool
		requestID bool
	}{
		{name: "invalid", err: fmt.Errorf("%w: name is required", service.ErrSessionFolderInvalid), status: http.StatusBadRequest, code: "session_folder_invalid"},
		{name: "not found", err: errors.Join(service.ErrSessionFolderNotFound, internal), status: http.StatusNotFound, code: "session_folder_not_found"},
		{name: "internal", err: internal, status: http.StatusInternalServerError, code: "session_folder_update_failed", retryable: true, requestID: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPatch, "/api/v1/session-folders/7", nil)
			ctx.Set("request_id", "req-session-folder")

			writeSessionFolderError(ctx, "update", tt.err)
			body := decodeUploadError(t, recorder)
			if recorder.Code != tt.status || body.Code != tt.code || body.Retryable != tt.retryable {
				t.Fatalf("response=%d %+v", recorder.Code, body)
			}
			if tt.requestID && body.RequestID != "req-session-folder" {
				t.Fatalf("missing request ID: %+v", body)
			}
			if !tt.requestID && body.RequestID != "" {
				t.Fatalf("unexpected request ID: %+v", body)
			}
			for _, secret := range []string{"postgres://", "fixture:secret", "/srv/private/folders"} {
				if strings.Contains(recorder.Body.String(), secret) {
					t.Fatalf("public response leaked %q: %s", secret, recorder.Body.String())
				}
			}
		})
	}
}

func TestSessionFolderHandlersClassifyFailures(t *testing.T) {
	t.Run("invalid name", func(t *testing.T) {
		svc := service.NewSessionFolderService(nil)
		recorder := serveSessionFolderHandler(http.MethodPost, "/session-folders", "/session-folders", []byte(`{"name":"   "}`), 81001, CreateSessionFolderHandler(svc))
		assertSessionFolderError(t, recorder, http.StatusBadRequest, "session_folder_invalid", false, false)
	})

	t.Run("missing folder", func(t *testing.T) {
		db := setupHandlerTestDB(t)
		svc := service.NewSessionFolderService(repository.NewSessionFolderRepository(db))
		recorder := serveSessionFolderHandler(http.MethodPatch, "/session-folders/:id", "/session-folders/999999999", []byte(`{"name":"fixture"}`), 81002, UpdateSessionFolderHandler(svc))
		assertSessionFolderError(t, recorder, http.StatusNotFound, "session_folder_not_found", false, false)
	})

	t.Run("closed repository", func(t *testing.T) {
		db := setupHandlerTestDB(t)
		svc := service.NewSessionFolderService(repository.NewSessionFolderRepository(db))
		if err := db.Close(); err != nil {
			t.Fatalf("close session folder database: %v", err)
		}
		recorder := serveSessionFolderHandler(http.MethodGet, "/session-folders", "/session-folders", nil, 81003, ListSessionFoldersHandler(svc))
		assertSessionFolderError(t, recorder, http.StatusInternalServerError, "session_folder_list_failed", true, true)
	})
}

func serveSessionFolderHandler(method, route, path string, body []byte, userID int64, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", userID)
		c.Set("request_id", "req-session-folder-handler")
		c.Next()
	})
	router.Handle(method, route, handler)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	router.ServeHTTP(recorder, request)
	return recorder
}

func assertSessionFolderError(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string, retryable, requestID bool) {
	t.Helper()
	body := decodeUploadError(t, recorder)
	if recorder.Code != status || body.Code != code || body.Retryable != retryable {
		t.Fatalf("response=%d %+v", recorder.Code, body)
	}
	if requestID && body.RequestID != "req-session-folder-handler" {
		t.Fatalf("missing request ID: %+v", body)
	}
	if !requestID && body.RequestID != "" {
		t.Fatalf("unexpected request ID: %+v", body)
	}
}
