package tool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// TestWebSearchTool_Info 验证工具元信息正确（不需要网络）
func TestWebSearchTool_Info(t *testing.T) {
	tool := NewWebSearchTool(WebSearchConfig{})
	info, err := tool.Info(context.Background())
	if err != nil {
		t.Fatalf("Info() error: %v", err)
	}
	if info.Name != "web_search" {
		t.Errorf("Name = %q, want web_search", info.Name)
	}
	if info.Desc == "" {
		t.Error("Desc should not be empty")
	}
	for _, want := range []string{"current, local, niche", "knowledge cutoff", "meaningfully different query"} {
		if !strings.Contains(info.Desc, want) {
			t.Fatalf("web_search description missing %q:\n%s", want, info.Desc)
		}
	}
}

func TestWebSearchConfiguresHTTPTimeout(t *testing.T) {
	tool := NewWebSearchTool(WebSearchConfig{Timeout: 3 * time.Second})
	if tool.client.Timeout != 3*time.Second {
		t.Fatalf("client timeout = %s, want 3s", tool.client.Timeout)
	}
}

func TestCleanSearchURLAndText(t *testing.T) {
	rawURL := "[https://example.com/path?q=1\n一段错误拼进 URL 的摘要"
	if got := cleanSearchURL(rawURL); got != "https://example.com/path?q=1" {
		t.Fatalf("cleanSearchURL() = %q", got)
	}

	rawEscapedURL := "[https://example.com/path?q=1\\n一段错误拼进 URL 的摘要"
	if got := cleanSearchURL(rawEscapedURL); got != "https://example.com/path?q=1" {
		t.Fatalf("cleanSearchURL(escaped newline) = %q", got)
	}

	if got := cleanSearchURL("not a url"); got != "" {
		t.Fatalf("cleanSearchURL(invalid) = %q, want empty", got)
	}

	rawText := " 第一行\n\n第二行\t第三行 "
	if got := cleanSearchText(rawText); got != "第一行 第二行 第三行" {
		t.Fatalf("cleanSearchText() = %q", got)
	}

	rawMarkdownText := `AI Agent发展历程](https://example.com/path) 和 [深度学习](https://example.com/deep)`
	if got := cleanSearchText(rawMarkdownText); got != "AI Agent发展历程 和 深度学习" {
		t.Fatalf("cleanSearchText(markdown) = %q", got)
	}
}

func TestWebSearchTool_TavilyProvider(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q", got)
		}
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req["query"] != "测试查询" {
			t.Fatalf("query = %v", req["query"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"结果标题","url":"https://example.com/a","content":"结果摘要"}]}`))
	}))
	defer server.Close()

	tool := NewWebSearchTool(WebSearchConfig{
		Provider:     "tavily",
		Providers:    []string{"tavily"},
		TavilyAPIKey: "test-key",
		TavilyURL:    server.URL,
	})
	input, _ := json.Marshal(WebSearchInput{Query: "测试查询"})
	result, err := tool.InvokableRun(context.Background(), string(input))
	if err != nil {
		t.Fatalf("InvokableRun() error: %v", err)
	}

	var output WebSearchOutput
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if len(output.Citations) != 1 {
		t.Fatalf("citations = %d, want 1", len(output.Citations))
	}
	if output.Citations[0].URL != "https://example.com/a" {
		t.Fatalf("url = %q", output.Citations[0].URL)
	}
	if output.Source != "tavily" {
		t.Fatalf("source = %q, want tavily", output.Source)
	}
}

func TestWebSearchTool_AdditionalProviderContracts(t *testing.T) {
	tests := []struct {
		name      string
		provider  string
		configure func(WebSearchConfig, string) WebSearchConfig
		assert    func(*testing.T, *http.Request)
		response  string
	}{
		{
			name:     "brave",
			provider: "brave",
			configure: func(cfg WebSearchConfig, endpoint string) WebSearchConfig {
				cfg.BraveAPIKey, cfg.BraveURL = "brave-key", endpoint
				return cfg
			},
			assert: func(t *testing.T, r *http.Request) {
				t.Helper()
				if r.Method != http.MethodGet || r.Header.Get("X-Subscription-Token") != "brave-key" || r.URL.Query().Get("q") != "contract" {
					t.Fatalf("unexpected Brave request: method=%s token=%q query=%q", r.Method, r.Header.Get("X-Subscription-Token"), r.URL.Query().Get("q"))
				}
			},
			response: `{"web":{"results":[{"title":"Brave","url":"https://example.com/brave","description":"result"}]}}`,
		},
		{
			name:     "exa",
			provider: "exa",
			configure: func(cfg WebSearchConfig, endpoint string) WebSearchConfig {
				cfg.ExaAPIKey, cfg.ExaURL = "exa-key", endpoint
				return cfg
			},
			assert: func(t *testing.T, r *http.Request) {
				t.Helper()
				var body map[string]any
				if r.Method != http.MethodPost || r.Header.Get("x-api-key") != "exa-key" || json.NewDecoder(r.Body).Decode(&body) != nil || body["query"] != "contract" {
					t.Fatalf("unexpected Exa request")
				}
			},
			response: `{"results":[{"title":"Exa","url":"https://example.com/exa","text":"result"}]}`,
		},
		{
			name:     "bocha",
			provider: "bocha",
			configure: func(cfg WebSearchConfig, endpoint string) WebSearchConfig {
				cfg.BochaAPIKey, cfg.BochaURL = "bocha-key", endpoint
				return cfg
			},
			assert: func(t *testing.T, r *http.Request) {
				t.Helper()
				var body map[string]any
				if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer bocha-key" || json.NewDecoder(r.Body).Decode(&body) != nil || body["query"] != "contract" {
					t.Fatalf("unexpected Bocha request")
				}
			},
			response: `{"code":0,"data":{"webPages":{"value":[{"name":"Bocha","url":"https://example.com/bocha","snippet":"result"}]}}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				tt.assert(t, r)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.response))
			}))
			defer server.Close()
			cfg := tt.configure(WebSearchConfig{Provider: tt.provider, Providers: []string{tt.provider}}, server.URL)
			result, err := NewWebSearchTool(cfg).InvokableRun(context.Background(), `{"query":"contract"}`)
			if err != nil {
				t.Fatalf("InvokableRun() error = %v", err)
			}
			var output WebSearchOutput
			if err := json.Unmarshal([]byte(result), &output); err != nil {
				t.Fatalf("unmarshal output: %v", err)
			}
			if output.Source != tt.provider || len(output.Citations) != 1 {
				t.Fatalf("output = %#v", output)
			}
		})
	}
}

func TestWebSearchTool_SearXNGHTMLResponseUsesStableFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>not json</body></html>"))
	}))
	defer server.Close()

	tool := NewWebSearchTool(WebSearchConfig{
		Provider:   "searxng",
		Providers:  []string{"searxng"},
		SearXNGURL: server.URL,
	})
	input, _ := json.Marshal(WebSearchInput{Query: "html"})
	result, err := tool.InvokableRun(context.Background(), string(input))
	if err != nil {
		t.Fatalf("InvokableRun() should return structured failure, got error: %v", err)
	}
	if strings.Contains(result, "SearXNG JSON format") || strings.Contains(result, "returned HTML") {
		t.Fatalf("result must not expose provider diagnostics: %s", result)
	}
}

func TestWebSearchTool_SearXNGEmptyResultsUsesStableFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[],"unresponsive_engines":[["duckduckgo","CAPTCHA"],["google","Suspended: CAPTCHA"]]}`))
	}))
	defer server.Close()

	tool := NewWebSearchTool(WebSearchConfig{
		Provider:   "searxng",
		Providers:  []string{"searxng"},
		SearXNGURL: server.URL,
	})
	input, _ := json.Marshal(WebSearchInput{Query: "empty"})
	result, err := tool.InvokableRun(context.Background(), string(input))
	if err != nil {
		t.Fatalf("InvokableRun() should return structured failure, got error: %v", err)
	}
	if strings.Contains(result, "duckduckgo") || strings.Contains(result, "CAPTCHA") {
		t.Fatalf("result must not expose upstream engine diagnostics: %s", result)
	}
}

func TestWebSearchTool_FallbacksFromTavilyToSearXNG(t *testing.T) {
	logs := captureToolLog(t)
	tavilyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}))
	defer tavilyServer.Close()

	searxngServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"兜底结果","url":"https://example.com/fallback","content":"来自 SearXNG"}]}`))
	}))
	defer searxngServer.Close()

	tool := NewWebSearchTool(WebSearchConfig{
		Provider:   "tavily",
		Providers:  []string{"tavily", "searxng"},
		SearXNGURL: searxngServer.URL,
		TavilyURL:  tavilyServer.URL,
	})
	input, _ := json.Marshal(WebSearchInput{Query: "fallback"})
	result, err := tool.InvokableRun(context.Background(), string(input))
	if err != nil {
		t.Fatalf("InvokableRun() error: %v", err)
	}
	if !strings.Contains(result, "https://example.com/fallback") {
		t.Fatalf("result does not contain fallback citation: %s", result)
	}
	var output WebSearchOutput
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.Source != "searxng" {
		t.Fatalf("source = %q, want searxng", output.Source)
	}
	if strings.Join(output.AttemptedSources, ",") != "tavily,searxng" {
		t.Fatalf("attempted_sources = %#v", output.AttemptedSources)
	}
	requireToolLogContains(t, logs.String(),
		"[web_search] run_start",
		"provider_start",
		"provider_failed",
		"provider_success",
		"fallback_succeeded",
		"run_success",
		"source=searxng",
	)
}

// TestWebSearchTool_GracefulDegradation 所有 provider 失败时，应返回结构化降级结果
// （nil error + SearchFailed=true），而非 Go error —— 否则 Eino ToolNode 会触发
// NodeRunError 中止整个 agent run。
func TestWebSearchTool_GracefulDegradation(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}))
	defer failing.Close()

	tool := NewWebSearchTool(WebSearchConfig{
		Provider:   "searxng",
		Providers:  []string{"searxng", "tavily"},
		SearXNGURL: failing.URL,
		TavilyURL:  failing.URL,
	})
	input, _ := json.Marshal(WebSearchInput{Query: "anything"})
	result, err := tool.InvokableRun(context.Background(), string(input))
	if err != nil {
		t.Fatalf("InvokableRun() should not return error on provider failure, got: %v", err)
	}

	var output WebSearchOutput
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if !output.SearchFailed {
		t.Fatalf("expected SearchFailed=true, got false (result=%s)", result)
	}
	if len(output.Citations) != 0 {
		t.Fatalf("expected no citations on failure, got %d", len(output.Citations))
	}
	if output.Summary == "" {
		t.Fatal("expected non-empty degradation summary instructing model to use own knowledge")
	}
	if output.ErrorCode != WebErrorCodeUnavailable || output.Error == "" {
		t.Fatalf("failure should use a stable error code and message: %#v", output)
	}
	if strings.Contains(output.Summary, "returned status") || strings.Contains(output.Error, "returned status") {
		t.Fatalf("failure must not expose upstream details: %s", result)
	}
	if strings.Join(output.AttemptedSources, ",") != "searxng,tavily" {
		t.Fatalf("attempted_sources = %#v", output.AttemptedSources)
	}
}

func TestWebSearchTool_FallbackChainRespectsSingleDeadline(t *testing.T) {
	delayed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusGatewayTimeout)
	}))
	defer delayed.Close()

	tool := NewWebSearchTool(WebSearchConfig{
		Provider:   "searxng",
		Providers:  []string{"searxng", "tavily"},
		SearXNGURL: delayed.URL,
		TavilyURL:  delayed.URL,
		Timeout:    150 * time.Millisecond,
	})
	started := time.Now()
	result, err := tool.InvokableRun(context.Background(), `{"query":"deadline"}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 240*time.Millisecond {
		t.Fatalf("fallback chain exceeded its total deadline: %s", elapsed)
	}
	var output WebSearchOutput
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.ErrorCode != WebErrorCodeTimeout || !output.Retryable {
		t.Fatalf("output = %#v", output)
	}
}

func TestFallbackServiceBudgetFavorsEarlierServices(t *testing.T) {
	const total = 30 * time.Second
	first := fallbackServiceBudget(context.Background(), total, 0, 3)
	second := fallbackServiceBudget(context.Background(), total, 1, 3)
	third := fallbackServiceBudget(context.Background(), total, 2, 3)

	if first != 17*time.Second+142857142*time.Nanosecond {
		t.Fatalf("first budget = %s", first)
	}
	if second != 20*time.Second || third != 30*time.Second {
		t.Fatalf("later budgets = %s, %s", second, third)
	}

	ctx, cancel := context.WithTimeout(context.Background(), total)
	defer cancel()
	if got := fallbackServiceBudget(ctx, total, 0, 3); got <= 17*time.Second || got > first {
		t.Fatalf("deadline-aware first budget = %s, want about %s", got, first)
	}
}

func TestCleanSearchURLAllowsOnlyHTTPSSources(t *testing.T) {
	for _, rawURL := range []string{"file:///etc/passwd", "javascript:alert(1)", "mailto:person@example.com"} {
		if got := cleanSearchURL(rawURL); got != "" {
			t.Fatalf("cleanSearchURL(%q) = %q, want empty", rawURL, got)
		}
	}
	if got := cleanSearchURL("https://example.com/source"); got != "https://example.com/source" {
		t.Fatalf("cleanSearchURL(https) = %q", got)
	}
}

// TestWebSearchTool_RejectsEmptyQuery 输入错误（query 为空）仍应返回 Go error，
// 因为那是调用方错误，不属于外部依赖失败的降级范畴。
func TestWebSearchTool_RejectsEmptyQuery(t *testing.T) {
	tool := NewWebSearchTool(WebSearchConfig{})
	input, _ := json.Marshal(WebSearchInput{Query: "   "})
	if _, err := tool.InvokableRun(context.Background(), string(input)); err == nil {
		t.Fatal("expected error for empty query, got nil")
	}
}

// TestWebSearchTool_Integration 真实调用 SearXNG（需网络，默认跳过）
// 运行：RUN_NET_TESTS=1 go test ./internal/tool/ -run Integration -v
func TestWebSearchTool_Integration(t *testing.T) {
	if os.Getenv("RUN_NET_TESTS") != "1" {
		t.Skip("skipping network test; set RUN_NET_TESTS=1 to run")
	}

	searxngURL := os.Getenv("SEARXNG_URL")
	if searxngURL == "" {
		t.Skip("skipping searxng integration; SEARXNG_URL is empty")
	}
	tool := NewWebSearchTool(WebSearchConfig{SearXNGURL: searxngURL})

	input, _ := json.Marshal(WebSearchInput{Query: "golang 1.24 release"})
	result, err := tool.InvokableRun(context.Background(), string(input))
	if err != nil {
		t.Fatalf("InvokableRun() error: %v", err)
	}

	var output WebSearchOutput
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	if len(output.Citations) == 0 {
		t.Error("expected at least one citation")
	}
	if output.Summary == "" {
		t.Error("expected non-empty summary")
	}

	t.Logf("Got %d citations", len(output.Citations))
	for i, c := range output.Citations {
		t.Logf("[%d] %s — %s", i+1, c.Title, c.URL)
	}
}

func TestWebSearchTool_TavilyIntegration(t *testing.T) {
	if os.Getenv("RUN_NET_TESTS") != "1" {
		t.Skip("skipping network test; set RUN_NET_TESTS=1 to run")
	}
	apiKey := os.Getenv("TAVILY_API_KEY")
	if apiKey == "" {
		t.Skip("skipping tavily integration; TAVILY_API_KEY is empty")
	}

	tool := NewWebSearchTool(WebSearchConfig{
		Provider:     "tavily",
		Providers:    []string{"tavily"},
		TavilyAPIKey: apiKey,
		MaxResults:   3,
	})
	input, _ := json.Marshal(WebSearchInput{Query: "Go 1.24 release"})
	result, err := tool.InvokableRun(context.Background(), string(input))
	if err != nil {
		t.Fatalf("InvokableRun() error: %v", err)
	}

	var output WebSearchOutput
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if len(output.Citations) == 0 {
		t.Fatal("expected at least one citation")
	}
	t.Logf("Tavily returned %d citations", len(output.Citations))
}
