package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/extractor"
)

func TestAdminSystemStatusHandlerReturnsPartialSafeStatus(t *testing.T) {
	sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Fatalf("path = %q, want /health", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer sidecar.Close()

	router := gin.New()
	router.GET("/status", AdminSystemStatusHandler(nil, extractor.NewSidecarClient(sidecar.URL, time.Second), time.Now().Add(-90*time.Second)))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/status", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response systemStatusResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Version == "" || response.Runtime.GoVersion == "" || response.UptimeSeconds < 89 {
		t.Fatalf("missing runtime identity: %#v", response)
	}
	if response.Database.OK || response.SchemaVersion != "" {
		t.Fatalf("nil database should be reported as unavailable: %#v", response.Database)
	}
	if !response.Extractor.Enabled || !response.Extractor.OK {
		t.Fatalf("healthy extractor not reported: %#v", response.Extractor)
	}
	for _, forbidden := range []string{sidecar.URL, "password", "storage/", "DB_HOST"} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, recorder.Body.String())
		}
	}
}

func TestReadUintFileRejectsUnlimitedAndInvalidValues(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "memory")
	for _, value := range []string{"max\n", "invalid", ""} {
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := readUintFile(path); got != nil {
			t.Fatalf("readUintFile(%q) = %d, want nil", value, *got)
		}
	}
	if err := os.WriteFile(path, []byte("12345\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readUintFile(path); got == nil || *got != 12345 {
		t.Fatalf("readUintFile numeric = %v, want 12345", got)
	}
}
