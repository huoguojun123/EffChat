package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/agent"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/service"
)

func TestModelErrorClassificationHidesInternalDetails(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "invalid", err: fmt.Errorf("%w: provider is required", service.ErrModelInvalid), wantStatus: http.StatusBadRequest, wantCode: "model_invalid"},
		{name: "exists", err: service.ErrModelExists, wantStatus: http.StatusConflict, wantCode: "model_exists"},
		{name: "not found", err: service.ErrModelNotFound, wantStatus: http.StatusNotFound, wantCode: "model_not_found"},
		{name: "internal", err: errors.New("postgres://secret@internal/private/model"), wantStatus: http.StatusInternalServerError, wantCode: "model_update_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPatch, "/api/v1/admin/models/example", nil)
			ctx.Set("request_id", "req-model")
			writeModelError(ctx, "update", tt.err)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.wantStatus)
			}
			if strings.Contains(recorder.Body.String(), "secret") || strings.Contains(recorder.Body.String(), "/private/model") {
				t.Fatalf("response leaked internal error: %s", recorder.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body["code"] != tt.wantCode || body["retryable"] != (tt.wantStatus >= 500) {
				t.Fatalf("response = %#v", body)
			}
		})
	}
}

func TestModelWildcardRouteAllowsSlashIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.PATCH("/admin/models/*id", func(c *gin.Context) {
		c.String(http.StatusOK, modelIDParam(c))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/admin/models/deepseek-ai%2FDeepSeek-V4-Flash", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Body.String(); got != "deepseek-ai/DeepSeek-V4-Flash" {
		t.Fatalf("model id = %q, want slash-preserved id", got)
	}
}

func TestModelTestHandlerRejectsMissingModelOrProvider(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/admin/models/test", TestModelHandler(nil))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/models/test", strings.NewReader(`{"id":"gpt-4o"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["code"] != "model_probe_invalid" || body["retryable"] != false {
		t.Fatalf("response = %#v", body)
	}
}

func TestModelTestHandlerRejectsUnavailableProbeRuntime(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/admin/models/test", TestModelHandler(nil))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/models/test", strings.NewReader(`{"id":"gpt-4o","provider":"openai"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["code"] != "model_probe_unavailable" || body["retryable"] != true {
		t.Fatalf("response = %#v", body)
	}
}

func TestInvalidJSONResponseIsStable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/admin/models/test", TestModelHandler(nil))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/models/test", strings.NewReader(`{`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "invalid_request_body") {
		t.Fatalf("body = %s, want stable invalid_request_body code", w.Body.String())
	}
}

func TestModelListConfigForChannelSupportsNativeAdapters(t *testing.T) {
	for _, tt := range []struct {
		name    string
		adapter string
	}{
		{name: "openai compatible", adapter: service.AdapterOpenAICompatible},
		{name: "anthropic", adapter: service.AdapterAnthropic},
		{name: "google", adapter: service.AdapterGoogle},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := modelListConfigForChannel(&model.AIChannel{
				Key:     tt.adapter,
				Adapter: tt.adapter,
				APIKey:  "sk-test",
				BaseURL: "https://api.example.com",
			})
			if err != nil {
				t.Fatalf("modelListConfigForChannel() error = %v", err)
			}
			if cfg.adapter != tt.adapter {
				t.Fatalf("adapter = %q, want %q", cfg.adapter, tt.adapter)
			}
		})
	}
}

func TestModelListConfigForChannelRequiresAPIKey(t *testing.T) {
	_, err := modelListConfigForChannel(&model.AIChannel{
		Key:     "newapi",
		Adapter: service.AdapterOpenAICompatible,
		BaseURL: "https://gateway.example.com/v1",
	})
	if !errors.Is(err, service.ErrChannelUnavailable) {
		t.Fatalf("error = %v, want channel unavailable", err)
	}
}

func TestModelListEndpointNormalizesConcreteOpenAIEndpoint(t *testing.T) {
	tests := map[string]string{
		"https://api.openai.com/v1/responses":        "https://api.openai.com/v1/models",
		"https://api.openai.com/v1/chat/completions": "https://api.openai.com/v1/models",
		"https://api.openai.com/v1/models":           "https://api.openai.com/v1/models",
		"https://gateway.example.com":                "https://gateway.example.com/v1/models",
	}
	for input, want := range tests {
		if got := modelListEndpoint(input); got != want {
			t.Fatalf("modelListEndpoint(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNativeModelListEndpointsNormalizeConcreteEndpoints(t *testing.T) {
	tests := map[string]string{
		"https://api.anthropic.com/v1":                            "https://api.anthropic.com/v1/models",
		"https://api.anthropic.com/v1/models":                     "https://api.anthropic.com/v1/models",
		"https://generativelanguage.googleapis.com/v1beta":        "https://generativelanguage.googleapis.com/v1beta/models",
		"https://generativelanguage.googleapis.com/v1beta/models": "https://generativelanguage.googleapis.com/v1beta/models",
	}
	for input, want := range tests {
		var got string
		if strings.Contains(input, "anthropic") {
			got = anthropicModelListEndpoint(input)
		} else {
			got = googleModelListEndpoint(input)
		}
		if got != want {
			t.Fatalf("native model endpoint(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestFetchGatewayModelsExplainsHTMLResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>login</body></html>"))
	}))
	defer server.Close()

	_, err := fetchGatewayModels(context.Background(), server.URL, "sk-test")
	if err == nil {
		t.Fatal("expected HTML response error")
	}
	if !strings.Contains(err.Error(), "returned HTML instead of JSON") {
		t.Fatalf("error = %q, want HTML guidance", err.Error())
	}
}

func TestFetchAnthropicModels(t *testing.T) {
	var sawAPIKey bool
	var sawVersion bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %q, want /v1/models", r.URL.Path)
		}
		sawAPIKey = r.Header.Get("x-api-key") == "sk-test"
		sawVersion = r.Header.Get("anthropic-version") == "2023-06-01"
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": [
				{"id":"claude-sonnet-4-5","display_name":"Claude Sonnet 4.5","created_at":"2026-01-01T00:00:00Z","type":"model"}
			],
			"has_more": false,
			"first_id": "claude-sonnet-4-5",
			"last_id": "claude-sonnet-4-5"
		}`))
	}))
	defer server.Close()

	models, err := fetchAnthropicModels(context.Background(), server.URL+"/v1", "sk-test")
	if err != nil {
		t.Fatalf("fetchAnthropicModels() error = %v", err)
	}
	if !sawAPIKey || !sawVersion {
		t.Fatalf("missing Anthropic auth/version headers: apiKey=%t version=%t", sawAPIKey, sawVersion)
	}
	if len(models) != 1 || models[0].ID != "claude-sonnet-4-5" || models[0].OwnedBy != "anthropic" {
		t.Fatalf("models = %#v", models)
	}
}

func TestFetchGoogleModels(t *testing.T) {
	var sawAPIKey bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models" {
			t.Fatalf("path = %q, want /v1beta/models", r.URL.Path)
		}
		sawAPIKey = r.Header.Get("x-goog-api-key") == "sk-test"
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"models": [
				{
					"name": "models/gemini-2.5-flash",
					"displayName": "Gemini 2.5 Flash",
					"inputTokenLimit": 1048576,
					"outputTokenLimit": 65536,
					"supportedActions": ["generateContent"],
					"description": "Fast Gemini model"
				}
			]
		}`))
	}))
	defer server.Close()

	models, err := fetchGoogleModels(context.Background(), server.URL+"/v1beta/models", "sk-test")
	if err != nil {
		t.Fatalf("fetchGoogleModels() error = %v", err)
	}
	if !sawAPIKey {
		t.Fatal("missing Google API key header")
	}
	if len(models) != 1 || models[0].ID != "gemini-2.5-flash" || models[0].OwnedBy != "google" {
		t.Fatalf("models = %#v", models)
	}
	if models[0].ContextWindow != 1048576 || models[0].MaxOutput != 65536 || !models[0].ToolUse || !models[0].Reasoning {
		t.Fatalf("model metadata = %#v", models[0])
	}
}

func TestWriteModelProbeFailureHidesInternalCause(t *testing.T) {
	for _, phase := range []string{"setup", "run"} {
		t.Run(phase, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/models/test", nil)
			ctx.Set("request_id", "req-probe")

			writeModelProbeFailure(ctx, "fixture-model", "fixture-provider", phase, errors.New("postgres://fixture:secret@db.example/effchat /srv/private/probe"))

			var body map[string]any
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			wantCode := "model_probe_failed"
			if phase == "setup" {
				wantCode = "model_probe_setup_failed"
			}
			if recorder.Code != http.StatusOK || body["code"] != wantCode || body["retryable"] != true || body["request_id"] != "req-probe" {
				t.Fatalf("response = %#v status=%d", body, recorder.Code)
			}
			if strings.Contains(recorder.Body.String(), "secret") || strings.Contains(recorder.Body.String(), "/srv/private") {
				t.Fatalf("response leaked internal cause: %s", recorder.Body.String())
			}
		})
	}
}

func TestWriteModelProbeFailureUsesSanitizedRuntimeClassification(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/models/test", nil)

	writeModelProbeFailure(ctx, "fixture-model", "fixture-provider", "run", &agent.RuntimeError{
		Code: "model_quota_exceeded", Message: "上游模型额度不足", Diagnostic: "HTTP 403 · 上游额度不足", Retryable: false,
	})

	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["code"] != "model_quota_exceeded" || body["error"] != "上游模型额度不足" || body["diagnostic"] != "HTTP 403 · 上游额度不足" || body["retryable"] != false {
		t.Fatalf("response = %#v", body)
	}
}

func TestInferModelDefaultsThinkingFormatAuto(t *testing.T) {
	m := inferModel(upstreamModelMeta{ID: "deepseek-v4-flash", Reasoning: true}, 0)
	if m.ThinkingFormat != "auto" {
		t.Fatalf("thinking_format = %q, want auto", m.ThinkingFormat)
	}
}

func TestMatchExistingGatewayModelFuzzyIdentityFields(t *testing.T) {
	existing := []*model.Model{
		{ID: "deepseek-ai/DeepSeek-V4-Flash", DisplayName: "DeepSeek V4 Flash", Provider: "deepseek"},
	}
	meta := upstreamModelMeta{
		ID:          "deepseek-v4-flash",
		Name:        "DeepSeek V4 Flash",
		OwnedBy:     "deepseek",
		MatchFields: []string{"deepseek-v4"},
	}

	got := matchExistingGatewayModel(meta, existing, "deepseek", map[string]bool{"deepseek": true})
	if got == nil || got.ID != existing[0].ID {
		t.Fatalf("match = %#v, want existing model", got)
	}
}

func TestMatchExistingGatewayModelUsesNestedAliasFields(t *testing.T) {
	existing := []*model.Model{
		{ID: "Qwen/Qwen3-VL-32B-Instruct", DisplayName: "Qwen3 VL 32B", Provider: "qwen"},
	}
	meta := parseUpstreamModelMap("gateway-qwen-vl", map[string]interface{}{
		"id":       "gateway-qwen-vl",
		"provider": "qwen",
		"metadata": map[string]interface{}{
			"id":             "gateway-internal-qwen-vl",
			"canonical_slug": "Qwen/Qwen3-VL-32B-Instruct",
		},
	})

	got := matchExistingGatewayModel(meta, existing, "qwen", map[string]bool{"qwen": true})
	if got == nil || got.ID != existing[0].ID {
		t.Fatalf("match = %#v, want existing model", got)
	}
}

func TestMatchExistingGatewayModelRoutesByInferredProviderForNativeChannel(t *testing.T) {
	existing := []*model.Model{
		{ID: "gateway-deepseek-v4-flash", DisplayName: "DeepSeek V4 Flash", Provider: "deepseek"},
		{ID: "gateway-gpt-4o", DisplayName: "GPT 4o", Provider: "openai"},
	}
	meta := upstreamModelMeta{
		ID:          "gateway-internal-001",
		Name:        "V4 Flash",
		MatchFields: []string{"deepseek-v4-flash"},
	}

	got := matchExistingGatewayModel(meta, existing, "deepseek", map[string]bool{"openai": true, "deepseek": true})
	if got == nil || got.Provider != "deepseek" {
		t.Fatalf("match = %#v, want deepseek model when fetched through deepseek channel", got)
	}
}

func TestOpenAICompatibleGatewayKeepsFetchedModelsInOpenAIProvider(t *testing.T) {
	fields := []string{"DeepSeek V4 Flash"}
	enabled := map[string]bool{"openai": true, "deepseek": true}

	if got := inferProviderFromFieldsWithEnabled(fields, "openai", enabled); got != "openai" {
		t.Fatalf("provider = %q, want openai for OpenAI-compatible import bucket", got)
	}
}

func TestMatchExistingGatewayModelKeepsInferredProviderBoundary(t *testing.T) {
	existing := []*model.Model{
		{ID: "gpt-4o", DisplayName: "GPT 4o", Provider: "deepseek"},
	}
	meta := upstreamModelMeta{ID: "gpt-4o", Name: "GPT 4o"}

	if got := matchExistingGatewayModel(meta, existing, "deepseek", map[string]bool{"openai": true, "deepseek": true}); got != nil {
		t.Fatalf("match = %#v, want nil when fields infer openai but existing model is deepseek", got)
	}
}

func TestMatchExistingGatewayModelUsesOpenAICompatibleImportBucket(t *testing.T) {
	existing := []*model.Model{
		{ID: "gateway-deepseek-v4-flash", DisplayName: "DeepSeek V4 Flash", Provider: "openai"},
		{ID: "deepseek-ai/DeepSeek-V4-Flash", DisplayName: "DeepSeek V4 Flash", Provider: "deepseek"},
	}
	meta := upstreamModelMeta{
		ID:          "gateway-deepseek-v4-flash",
		Name:        "DeepSeek V4 Flash",
		MatchFields: []string{"deepseek-v4-flash"},
	}

	got := matchExistingGatewayModel(meta, existing, "openai", map[string]bool{"openai": true, "deepseek": true})
	if got == nil || got.Provider != "openai" {
		t.Fatalf("match = %#v, want openai model when fetched through openai-compatible gateway", got)
	}
}

func TestInferProviderFallsBackWhenInferredProviderDisabled(t *testing.T) {
	fields := []string{"DeepSeek V4 Flash"}
	enabled := map[string]bool{"openai": true}

	if got := inferProviderFromFieldsWithEnabled(fields, "openai", enabled); got != "openai" {
		t.Fatalf("provider = %q, want openai when deepseek channel is disabled", got)
	}
}

func TestInferProviderKeepsCustomChannelFallback(t *testing.T) {
	fields := []string{"DeepSeek V4 Flash"}
	enabled := map[string]bool{"newapi": true}

	if got := inferProviderFromFieldsWithEnabled(fields, "newapi", enabled); got != "newapi" {
		t.Fatalf("provider = %q, want custom channel fallback when inferred vendor channel is disabled", got)
	}
}

func TestMatchExistingGatewayModelUsesEnabledFallbackProvider(t *testing.T) {
	existing := []*model.Model{
		{ID: "gateway-deepseek-v4-flash", DisplayName: "DeepSeek V4 Flash", Provider: "openai"},
		{ID: "deepseek-ai/DeepSeek-V4-Flash", DisplayName: "DeepSeek V4 Flash", Provider: "deepseek"},
	}
	meta := upstreamModelMeta{
		ID:          "gateway-deepseek-v4-flash",
		Name:        "DeepSeek V4 Flash",
		MatchFields: []string{"deepseek-v4-flash"},
	}

	got := matchExistingGatewayModel(meta, existing, "openai", map[string]bool{"openai": true})
	if got == nil || got.Provider != "openai" {
		t.Fatalf("match = %#v, want openai model when only openai channel is enabled", got)
	}
}

func TestInferProviderFromModelIdentityFields(t *testing.T) {
	tests := []struct {
		name   string
		fields []string
		want   string
	}{
		{name: "gpt", fields: []string{"provider-internal", "GPT-4o"}, want: "openai"},
		{name: "claude", fields: []string{"router-42", "anthropic/claude-sonnet"}, want: "anthropic"},
		{name: "deepseek", fields: []string{"vendor-name", "DeepSeek V4 Flash"}, want: "deepseek"},
		{name: "qwen", fields: []string{"gateway-qwen3", "Qwen/Qwen3-VL-32B"}, want: "qwen"},
		{name: "glm", fields: []string{"zhipu", "GLM-4.5"}, want: "zhipu"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inferProviderFromFields(tt.fields, "openai"); got != tt.want {
				t.Fatalf("provider = %q, want %q", got, tt.want)
			}
		})
	}
}
