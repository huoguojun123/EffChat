package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
)

func TestRunErrorContractHidesInternalDetails(t *testing.T) {
	tests := []struct {
		name       string
		operation  string
		err        error
		wantStatus int
		wantCode   string
		requestID  bool
	}{
		{name: "missing durable run", operation: "status", err: repository.ErrNotFound, wantStatus: http.StatusNotFound, wantCode: "run_not_found"},
		{name: "missing live run", operation: "resume", err: service.ErrRunNotFound, wantStatus: http.StatusNotFound, wantCode: "run_not_found"},
		{name: "internal status failure", operation: "status", err: errors.New("postgres://fixture:secret@db.example/effchat /srv/private/runs"), wantStatus: http.StatusInternalServerError, wantCode: "run_status_failed", requestID: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/sessions/7/runs/fixture", nil)
			ctx.Set("request_id", "req-run-contract")

			writeRunError(ctx, tt.operation, tt.err)
			body := decodeRunError(t, recorder)
			if recorder.Code != tt.wantStatus || body.Code != tt.wantCode || body.Retryable != (tt.wantStatus >= 500) {
				t.Fatalf("response=%d %+v", recorder.Code, body)
			}
			if tt.requestID && body.RequestID != "req-run-contract" {
				t.Fatalf("missing request ID: %+v", body)
			}
			for _, secret := range []string{"postgres://", "fixture:secret", "/srv/private/runs"} {
				if strings.Contains(recorder.Body.String(), secret) {
					t.Fatalf("public response leaked %q: %s", secret, recorder.Body.String())
				}
			}
		})
	}
}

func TestRunHandlersValidateBeforeDependencyAccess(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		route   string
		path    string
		handler gin.HandlerFunc
		code    string
	}{
		{name: "invalid active session", method: http.MethodGet, route: "/sessions/:id/runs/active", path: "/sessions/0/runs/active", handler: ActiveRunHandler(nil, nil), code: "session_id_invalid"},
		{name: "invalid status run id", method: http.MethodGet, route: "/sessions/:id/runs/:run_id", path: "/sessions/7/runs/%20", handler: RunStatusHandler(nil, nil), code: "run_id_invalid"},
		{name: "invalid resume cursor", method: http.MethodGet, route: "/sessions/:id/runs/:run_id/resume", path: "/sessions/7/runs/fixture/resume?cursor=latest", handler: ResumeRunHandler(nil, nil, 0), code: "run_cursor_invalid"},
		{name: "invalid cancel session", method: http.MethodDelete, route: "/sessions/:id/runs/:run_id", path: "/sessions/not-a-number/runs/fixture", handler: CancelRunHandler(nil, nil), code: "session_id_invalid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := serveRunContractHandler(tt.method, tt.route, tt.path, 7, tt.handler)
			body := decodeRunError(t, recorder)
			if recorder.Code != http.StatusBadRequest || body.Code != tt.code || body.Retryable {
				t.Fatalf("response=%d %+v", recorder.Code, body)
			}
		})
	}
}

func TestRunHandlersClassifyMissingAndRepositoryFailures(t *testing.T) {
	env := setupTestEnv(t)
	session := createResumeTestSession(t, env)

	t.Run("missing durable run", func(t *testing.T) {
		quotaService := service.NewQuotaService(repository.NewQuotaRepository(env.db))
		handler := RunStatusHandler(quotaService, env.sessionService)
		path := fmt.Sprintf("/sessions/%d/runs/missing-run", session.ID)
		recorder := serveRunContractHandler(http.MethodGet, "/sessions/:id/runs/:run_id", path, env.userID, handler)
		assertRunError(t, recorder, http.StatusNotFound, "run_not_found", false)
	})

	t.Run("closed durable repository", func(t *testing.T) {
		closedDB := setupHandlerTestDB(t)
		if err := closedDB.Close(); err != nil {
			t.Fatalf("close quota database: %v", err)
		}
		quotaService := service.NewQuotaService(repository.NewQuotaRepository(closedDB))
		handler := RunStatusHandler(quotaService, env.sessionService)
		path := fmt.Sprintf("/sessions/%d/runs/fixture-run", session.ID)
		recorder := serveRunContractHandler(http.MethodGet, "/sessions/:id/runs/:run_id", path, env.userID, handler)
		assertRunError(t, recorder, http.StatusInternalServerError, "run_status_failed", true)
	})

	t.Run("missing live run", func(t *testing.T) {
		handler := ResumeRunHandler(service.NewRunHub(time.Minute, 1<<20), env.sessionService, 0)
		path := fmt.Sprintf("/sessions/%d/runs/missing-run/resume?cursor=0", session.ID)
		recorder := serveRunContractHandler(http.MethodGet, "/sessions/:id/runs/:run_id/resume", path, env.userID, handler)
		assertRunError(t, recorder, http.StatusNotFound, "run_not_found", false)
	})
}

type runErrorResponse struct {
	Code      string `json:"code"`
	Error     string `json:"error"`
	Retryable bool   `json:"retryable"`
	RequestID string `json:"request_id"`
}

func serveRunContractHandler(method, route, path string, userID int64, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", userID)
		c.Set("request_id", "req-run-handler")
		c.Next()
	})
	router.Handle(method, route, handler)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(method, path, nil))
	return recorder
}

func assertRunError(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string, requestID bool) {
	t.Helper()
	body := decodeRunError(t, recorder)
	if recorder.Code != status || body.Code != code || body.Retryable != (status >= 500) {
		t.Fatalf("response=%d %+v", recorder.Code, body)
	}
	if requestID && body.RequestID != "req-run-handler" {
		t.Fatalf("missing request ID: %+v", body)
	}
}

func decodeRunError(t *testing.T, recorder *httptest.ResponseRecorder) runErrorResponse {
	t.Helper()
	var body runErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v body=%s", err, recorder.Body.String())
	}
	return body
}
