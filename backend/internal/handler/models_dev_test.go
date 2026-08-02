package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
)

func TestFetchModelsDevCatalogKeepsLastSuccessfulCacheOnRefreshFailure(t *testing.T) {
	resetModelsDevCatalogCache(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"openai":{"id":"openai","models":{}}}`))
	}))
	defer server.Close()

	catalog, err := fetchModelsDevCatalogFrom(context.Background(), server.Client(), server.URL)
	if err != nil || catalog["openai"].ID != "openai" {
		t.Fatalf("initial catalog = %#v, err=%v", catalog, err)
	}
	catalogCachedAt = time.Now().Add(-catalogCacheTTL - time.Second)
	server.Close()
	catalog, err = fetchModelsDevCatalogFrom(context.Background(), server.Client(), server.URL)
	if err != nil || catalog["openai"].ID != "openai" {
		t.Fatalf("stale fallback = %#v, err=%v", catalog, err)
	}
}

func TestFetchModelsDevCatalogRejectsOversizedResponse(t *testing.T) {
	resetModelsDevCatalogCache(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(make([]byte, modelsDevCatalogMaxBytes+1))
	}))
	defer server.Close()
	if _, err := fetchModelsDevCatalogFrom(context.Background(), server.Client(), server.URL); err == nil {
		t.Fatal("oversized catalog succeeded")
	}
}

func TestFetchModelsDevCatalogStopsOnCanceledContextWithoutCaching(t *testing.T) {
	resetModelsDevCatalogCache(t)
	requestStarted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := fetchModelsDevCatalogFrom(ctx, server.Client(), server.URL)
		done <- err
	}()
	<-requestStarted
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("fetch error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("catalog fetch did not stop after cancellation")
	}
	if cached := staleCatalog(); cached != nil {
		t.Fatalf("canceled fetch populated cache: %#v", cached)
	}
}

func TestFetchModelsDevCatalogCancellationWinsOverStaleCache(t *testing.T) {
	resetModelsDevCatalogCache(t)
	storeCatalog(map[string]modelsDevProvider{"stale": {ID: "stale"}})
	catalogCachedAt = time.Now().Add(-catalogCacheTTL - time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       cancelingReadCloser{cancel: cancel},
			Header:     make(http.Header),
		}, nil
	})}
	if _, err := fetchModelsDevCatalogFrom(ctx, client, "https://models.dev.test/api.json"); !errors.Is(err, context.Canceled) {
		t.Fatalf("fetch error = %v, want context canceled instead of stale cache", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type cancelingReadCloser struct {
	cancel context.CancelFunc
}

func (r cancelingReadCloser) Read([]byte) (int, error) {
	r.cancel()
	return 0, context.Canceled
}

func (cancelingReadCloser) Close() error { return nil }

var _ io.ReadCloser = cancelingReadCloser{}

func resetModelsDevCatalogCache(t *testing.T) {
	t.Helper()
	catalogCacheMu.Lock()
	previousCatalog, previousAt := catalogCache, catalogCachedAt
	catalogCache, catalogCachedAt = nil, time.Time{}
	catalogCacheMu.Unlock()
	t.Cleanup(func() {
		catalogCacheMu.Lock()
		catalogCache, catalogCachedAt = previousCatalog, previousAt
		catalogCacheMu.Unlock()
	})
}

func TestModelsDevToModel(t *testing.T) {
	meta := modelsDevModel{
		ID:        "claude-opus-4-5",
		Name:      "Claude Opus 4.5",
		Reasoning: true,
		ToolCall:  true,
	}
	meta.Modalities.Input = []string{"text", "image", "pdf"}
	meta.Limit.Context = 200000
	meta.Limit.Output = 64000

	m := modelsDevToModel("anthropic", "claude-opus-4-5", meta, 0)

	if m.ID != "claude-opus-4-5" || m.DisplayName != "Claude Opus 4.5" {
		t.Errorf("id/display mismatch: %s / %s", m.ID, m.DisplayName)
	}
	if m.Provider != "anthropic" {
		t.Errorf("provider: want anthropic, got %s", m.Provider)
	}
	if !m.Vision {
		t.Error("expected vision=true from image modality")
	}
	if !m.ToolUse || !m.Reasoning {
		t.Errorf("expected tool_use+reasoning true, got %v/%v", m.ToolUse, m.Reasoning)
	}
	if m.ContextWindow != 200000 || m.MaxOutput != 64000 {
		t.Errorf("limits: got context=%d output=%d", m.ContextWindow, m.MaxOutput)
	}
	if m.SearchImpl != "tool" {
		t.Errorf("anthropic search_impl: want tool, got %q", m.SearchImpl)
	}
	if m.ThinkingFormat != "auto" {
		t.Errorf("thinking_format: want auto, got %q", m.ThinkingFormat)
	}
	if m.Enabled {
		t.Error("imported catalog model should default to disabled")
	}
}

func TestSortedCatalogProvidersIncludesEveryProvider(t *testing.T) {
	providers := sortedCatalogProviders(map[string]modelsDevProvider{
		"openai": {ID: "openai"},
		"xai":    {ID: "xai"},
		"custom": {ID: "custom"},
	})
	want := []string{"custom", "openai", "xai"}
	if len(providers) != len(want) {
		t.Fatalf("providers = %v, want %v", providers, want)
	}
	for index, provider := range want {
		if providers[index] != provider {
			t.Fatalf("providers = %v, want %v", providers, want)
		}
	}
}

func TestModelsDevToModel_NoVisionTextOnly(t *testing.T) {
	meta := modelsDevModel{ID: "deepseek-v4", Name: "DeepSeek V4", ToolCall: true}
	meta.Modalities.Input = []string{"text"}
	meta.Limit.Context = 1000000

	m := modelsDevToModel("deepseek", "deepseek-v4", meta, 1)
	if m.Vision {
		t.Error("text-only model should not have vision")
	}
	if m.SearchImpl != "" {
		t.Errorf("deepseek search_impl should be empty, got %q", m.SearchImpl)
	}
}

func TestModelsDevToModel_FallbackDisplayName(t *testing.T) {
	meta := modelsDevModel{ID: "gpt-4o-mini"}
	m := modelsDevToModel("openai", "gpt-4o-mini", meta, 0)
	if m.DisplayName != "GPT 4o Mini" {
		t.Errorf("fallback display name: got %q", m.DisplayName)
	}
}

func TestModelsDevCatalogModelsUsesExistingRecordsWithoutPerModelLookup(t *testing.T) {
	catalog := map[string]modelsDevProvider{
		"openai": {
			ID: "openai",
			Models: map[string]modelsDevModel{
				"gpt-existing": {ID: "gpt-existing", Name: "Catalog name"},
				"gpt-new":      {ID: "gpt-new", Name: "New catalog model"},
			},
		},
	}
	existing := &model.Model{ID: "gpt-existing", DisplayName: "Local override", Provider: "openai", Enabled: true, SortOrder: 7}

	models := modelsDevCatalogModels(catalog, []*model.Model{existing})
	if len(models) != 2 {
		t.Fatalf("models length = %d, want 2", len(models))
	}
	byID := map[string]*model.Model{}
	for _, item := range models {
		byID[item.ID] = item
	}
	if byID[existing.ID] != existing {
		t.Fatalf("existing model was not reused: %#v", byID[existing.ID])
	}
	if byID["gpt-new"] == nil || byID["gpt-new"].Enabled || byID["gpt-new"].SortOrder != 2000 {
		t.Fatalf("new catalog model = %#v", byID["gpt-new"])
	}
}

func TestListModelsDevCatalogReturnsFailureWhenLocalModelsCannotLoad(t *testing.T) {
	resetModelsDevCatalogCache(t)
	storeCatalog(map[string]modelsDevProvider{
		"openai": {ID: "openai", Models: map[string]modelsDevModel{"gpt-draft": {ID: "gpt-draft", Name: "Draft"}}},
	})
	db := setupHandlerTestDB(t)
	if err := db.Close(); err != nil {
		t.Fatalf("close test db: %v", err)
	}

	router := gin.New()
	router.GET("/catalog", ListModelsDevCatalogHandler(service.NewModelService(repository.NewModelRepository(db))))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/catalog", nil))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusInternalServerError, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"code":"models_dev_local_models_load_failed"`) {
		t.Fatalf("unexpected error payload: %s", recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "gpt-draft") {
		t.Fatalf("handler returned catalog draft after local model failure: %s", recorder.Body.String())
	}
}

func TestGetModelsDevCatalogModelReturnsStableQueryErrors(t *testing.T) {
	resetModelsDevCatalogCache(t)
	storeCatalog(map[string]modelsDevProvider{
		"openai": {
			ID: "openai",
			Models: map[string]modelsDevModel{
				"gpt-present": {ID: "gpt-present", Name: "Present"},
			},
		},
	})
	router := gin.New()
	router.GET("/catalog/*id", GetModelsDevCatalogModelHandler())

	tests := []struct {
		name       string
		target     string
		wantStatus int
		wantCode   string
	}{
		{name: "missing id", target: "/catalog/", wantStatus: http.StatusBadRequest, wantCode: "models_dev_model_invalid"},
		{name: "missing provider", target: "/catalog/gpt-present?provider=unknown", wantStatus: http.StatusNotFound, wantCode: "models_dev_provider_not_found"},
		{name: "missing scoped model", target: "/catalog/gpt-missing?provider=openai", wantStatus: http.StatusNotFound, wantCode: "models_dev_model_not_found"},
		{name: "missing unscoped model", target: "/catalog/gpt-missing", wantStatus: http.StatusNotFound, wantCode: "models_dev_model_not_found"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.target, nil))
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body["code"] != test.wantCode || body["retryable"] != false {
				t.Fatalf("response = %#v", body)
			}
		})
	}
}

func TestModalitiesHaveImage(t *testing.T) {
	cases := []struct {
		input []string
		want  bool
	}{
		{[]string{"text", "image"}, true},
		{[]string{"text"}, false},
		{[]string{"Image"}, true},
		{nil, false},
	}
	for _, tc := range cases {
		if got := modalitiesHaveImage(tc.input); got != tc.want {
			t.Errorf("modalitiesHaveImage(%v): want %v, got %v", tc.input, tc.want, got)
		}
	}
}
