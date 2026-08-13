package extractor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSidecarExtractClassifiesFailuresWithoutPropagatingResponseBodies(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{name: "input too large", status: http.StatusRequestEntityTooLarge, body: `{"detail":"file too large"}`, want: ErrLimitExceeded},
		{name: "unsupported", status: http.StatusUnsupportedMediaType, body: `{"detail":"unsupported document type"}`, want: ErrUnsupported},
		{name: "no readable text", status: http.StatusUnprocessableEntity, body: `{"detail":"no readable text extracted"}`, want: ErrNoReadableText},
		{name: "malformed document", status: http.StatusUnprocessableEntity, body: `{"detail":"extract failed: /private/input.docx"}`, want: ErrUnprocessable},
		{name: "sidecar outage", status: http.StatusBadGateway, body: `{"detail":"postgres://secret@internal/provider"}`, want: ErrUnavailable},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()

			client := NewSidecarClient(server.URL, 5*time.Minute)
			_, err := client.Extract(context.Background(), []byte("fixture"), "application/pdf", "fixture.pdf")
			if !errors.Is(err, test.want) {
				t.Fatalf("Extract() error = %v, want class %v", err, test.want)
			}
			if strings.Contains(err.Error(), "/private/") || strings.Contains(err.Error(), "secret@") {
				t.Fatalf("Extract() exposed sidecar response body: %v", err)
			}
		})
	}
}

func TestSidecarExtractClassifiesInvalidAndEmptySuccessResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
		want error
	}{
		{name: "invalid JSON", body: `not-json`, want: ErrUnavailable},
		{name: "empty text", body: `{"text":"   "}`, want: ErrNoReadableText},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()

			client := NewSidecarClient(server.URL, 5*time.Minute)
			_, err := client.Extract(context.Background(), []byte("fixture"), "application/pdf", "fixture.pdf")
			if !errors.Is(err, test.want) {
				t.Fatalf("Extract() error = %v, want class %v", err, test.want)
			}
		})
	}
}
