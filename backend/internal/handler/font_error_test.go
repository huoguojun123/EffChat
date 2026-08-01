package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/testutil"
	_ "github.com/lib/pq"
)

func TestWriteFontErrorDoesNotExposeInternalCause(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/fonts", nil)
	ctx.Set("request_id", "req-font-contract")

	writeFontError(ctx, "list", errors.New("postgres://fixture:secret@db.example/effchat /srv/private/fonts"))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["code"] != "font_list_failed" || body["request_id"] != "req-font-contract" || body["retryable"] != true {
		t.Fatalf("response = %#v", body)
	}
	if strings.Contains(recorder.Body.String(), "secret") || strings.Contains(recorder.Body.String(), "/srv/private") {
		t.Fatalf("response leaked internal cause: %s", recorder.Body.String())
	}
}

func TestFontHandlerPostgresAndStorageFailureClassification(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := testutil.OpenPostgresTestDB(t)
	fontRepo := repository.NewFontRepository(db)
	fontPath := filepath.Join(t.TempDir(), "fixture.woff2")
	fontBytes := []byte("wOF2fictional-font-payload")
	if err := os.WriteFile(fontPath, fontBytes, 0o600); err != nil {
		t.Fatalf("write font fixture: %v", err)
	}
	font := &model.FontAsset{
		DisplayName: "Fixture Sans",
		FamilyName:  "Fixture Sans",
		FileName:    "fixture.woff2",
		FilePath:    fontPath,
		MimeType:    "font/woff2",
		FileSize:    int64(len(fontBytes)),
		Checksum:    strings.Repeat("a", 64),
		Weight:      400,
		Style:       "normal",
		Enabled:     true,
	}
	if err := fontRepo.Create(font); err != nil {
		t.Fatalf("create font fixture: %v", err)
	}

	missing := invokeFontHandler(UpdateFontHandler(fontRepo), http.MethodPatch, "/fonts/999999", "999999", `{}`)
	assertFontResponse(t, missing, http.StatusNotFound, "font_not_found", false)

	functionName := "fail_font_update_contract"
	if _, err := db.Exec(`
		CREATE OR REPLACE FUNCTION ` + functionName + `() RETURNS trigger AS $$
		BEGIN
			RAISE EXCEPTION 'postgres://fixture:secret@db.example/effchat /srv/private/fonts';
		END;
		$$ LANGUAGE plpgsql;
		CREATE TRIGGER fail_font_update_contract_trigger
		BEFORE UPDATE ON font_assets
		FOR EACH ROW WHEN (OLD.id = ` + fmt.Sprint(font.ID) + `)
		EXECUTE FUNCTION ` + functionName + `();
	`); err != nil {
		t.Fatalf("install update failure trigger: %v", err)
	}
	failedUpdate := invokeFontHandler(UpdateFontHandler(fontRepo), http.MethodPatch, "/fonts/1", fmt.Sprint(font.ID), `{"display_name":"Updated Fixture"}`)
	assertFontResponse(t, failedUpdate, http.StatusInternalServerError, "font_update_failed", true)
	if strings.Contains(failedUpdate.Body.String(), "secret") || strings.Contains(failedUpdate.Body.String(), "/srv/private") {
		t.Fatalf("update response leaked database failure: %s", failedUpdate.Body.String())
	}

	if _, err := db.Exec(`DROP TRIGGER fail_font_update_contract_trigger ON font_assets; DROP FUNCTION ` + functionName + `()`); err != nil {
		t.Fatalf("remove update failure trigger: %v", err)
	}
	downloaded := invokeFontHandler(DownloadFontFileHandler(fontRepo), http.MethodGet, "/fonts/1/file", fmt.Sprint(font.ID), "")
	if downloaded.Code != http.StatusOK || downloaded.Body.String() != string(fontBytes) {
		t.Fatalf("download status=%d body=%q", downloaded.Code, downloaded.Body.String())
	}
	if err := os.Remove(fontPath); err != nil {
		t.Fatalf("remove font fixture: %v", err)
	}
	missingFile := invokeFontHandler(DownloadFontFileHandler(fontRepo), http.MethodGet, "/fonts/1/file", fmt.Sprint(font.ID), "")
	assertFontResponse(t, missingFile, http.StatusInternalServerError, "font_file_open_failed", true)
}

func invokeFontHandler(handler gin.HandlerFunc, method, path, id, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("request_id", "req-font-postgres")
	if id != "" {
		ctx.Params = gin.Params{{Key: "id", Value: id}}
	}
	handler(ctx)
	return recorder
}

func assertFontResponse(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string, retryable bool) {
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
	if status >= http.StatusInternalServerError && body["request_id"] != "req-font-postgres" {
		t.Fatalf("request_id = %#v", body["request_id"])
	}
}

func TestFontHandlersDoNotMisclassifyClosedRepositoryAsClientFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := sql.Open("postgres", "postgres://fixture:secret@db.example/effchat?sslmode=disable")
	if err != nil {
		t.Fatalf("open database handle: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close database handle: %v", err)
	}
	fontRepo := repository.NewFontRepository(db)

	tests := []struct {
		name     string
		method   string
		path     string
		body     string
		handler  gin.HandlerFunc
		wantCode string
	}{
		{name: "list", method: http.MethodGet, path: "/fonts", handler: ListAdminFontsHandler(fontRepo), wantCode: "font_list_failed"},
		{name: "update lookup", method: http.MethodPatch, path: "/fonts/7", body: `{}`, handler: UpdateFontHandler(fontRepo), wantCode: "font_load_failed"},
		{name: "select lookup", method: http.MethodPut, path: "/fonts/selected", body: `{"font_id":7}`, handler: SelectFontHandler(fontRepo), wantCode: "font_load_failed"},
		{name: "delete lookup", method: http.MethodDelete, path: "/fonts/7", handler: DeleteFontHandler(fontRepo), wantCode: "font_load_failed"},
		{name: "download lookup", method: http.MethodGet, path: "/fonts/7/file", handler: DownloadFontFileHandler(fontRepo), wantCode: "font_load_failed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			ctx.Request.Header.Set("Content-Type", "application/json")
			ctx.Set("request_id", "req-font-handler")
			ctx.Params = gin.Params{{Key: "id", Value: "7"}}

			tc.handler(ctx)

			if recorder.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body["code"] != tc.wantCode || body["request_id"] != "req-font-handler" || body["retryable"] != true {
				t.Fatalf("response = %#v", body)
			}
		})
	}
}
