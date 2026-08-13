package tool

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/huoguojun123/EffChat/internal/modelstream"
)

// allowLoopback 放宽 basic 爬虫的 SSRF 策略，使其可访问 httptest 起在 127.0.0.1 的
// 本地 mock server。生产默认 isBlockedIP 仍拒环回——仅测试需要此放宽。
func allowLoopback(t *WebExtractTool) *WebExtractTool {
	t.ipBlocked = func(ip net.IP) bool {
		if ip != nil && ip.IsLoopback() {
			return false
		}
		return isBlockedIP(ip)
	}
	t.basicClient = newGuardedHTTPClient(20*time.Second, t.basicResolver, t.ipBlocked)
	return t
}

func allowMockPageTarget(t *WebExtractTool) *WebExtractTool {
	t.ipBlocked = func(ip net.IP) bool { return ip == nil }
	t.basicClient = newGuardedHTTPClient(20*time.Second, t.basicResolver, t.ipBlocked)
	return t
}

func TestWebExtractConfiguresHTTPTimeout(t *testing.T) {
	tool := NewWebExtractTool(WebExtractConfig{Timeout: 4 * time.Second})
	if tool.client.Timeout != 4*time.Second {
		t.Fatalf("client timeout = %s, want 4s", tool.client.Timeout)
	}
	if tool.basicClient.Timeout != 4*time.Second {
		t.Fatalf("basic client timeout = %s, want 4s", tool.basicClient.Timeout)
	}
}

func TestNormalizeCrawlerProvidersKeepsExternalOrderAndBasicLast(t *testing.T) {
	tests := []struct {
		name      string
		providers []string
		fallback  string
		want      []string
	}{
		{name: "configured order", providers: []string{"jina", "firecrawl"}, fallback: "jina", want: []string{"jina", "firecrawl", "basic"}},
		{name: "early and duplicate basic", providers: []string{"basic", "jina", "basic", "firecrawl", "jina"}, fallback: "jina", want: []string{"jina", "firecrawl", "basic"}},
		{name: "no external provider", fallback: "basic", want: []string{"basic"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeCrawlerProviders(tt.providers, tt.fallback); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("normalizeCrawlerProviders() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestWebExtractToolInfoGuidesExactURLWorkflow(t *testing.T) {
	tool := NewWebExtractTool(WebExtractConfig{})
	info, err := tool.Info(context.Background())
	if err != nil {
		t.Fatalf("Info() error: %v", err)
	}
	for _, want := range []string{"one exact web page URL", "Do not invent URLs", "Set a concrete goal"} {
		if !strings.Contains(info.Desc, want) {
			t.Fatalf("web_extract description missing %q:\n%s", want, info.Desc)
		}
	}
}

func TestWebExtractTool_ReturnsStructuredErrorOnUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	tool := allowLoopback(NewWebExtractTool(WebExtractConfig{}))
	input, _ := json.Marshal(WebExtractInput{URL: server.URL})

	result, err := tool.InvokableRun(context.Background(), string(input))
	if err != nil {
		t.Fatalf("InvokableRun() error = %v, want nil", err)
	}

	var output WebExtractOutput
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	if output.OK {
		t.Fatal("output.OK = true, want false")
	}
	if output.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status_code = %d, want %d", output.StatusCode, http.StatusUnauthorized)
	}
	if output.Error == "" {
		t.Fatal("error should not be empty")
	}
}

func TestWebExtractTool_AllowsThirdPartyCrawlerWhenLocalDNSMapsPrivate(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte("Title: 代理解析\n正文"))
	}))
	defer server.Close()

	tool := NewWebExtractTool(WebExtractConfig{CrawlerProviders: []string{"jina"}, JinaBaseURL: server.URL})
	tool.basicResolver = staticResolver{ips: []net.IPAddr{{IP: net.ParseIP("10.0.0.8")}}}
	output := runExtract(t, tool, WebExtractInput{URL: "https://private-dns.example/article"})

	if !output.OK || output.Source != "jina" {
		t.Fatalf("output = %#v", output)
	}
	if !called {
		t.Fatal("third-party crawler should run without local DNS preflight")
	}
}

func TestWebExtractTool_BlocksLocalhostBeforeThirdPartyCrawler(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	tool := NewWebExtractTool(WebExtractConfig{
		CrawlerProviders: []string{"jina", "basic"},
		JinaBaseURL:      server.URL,
	})
	input, _ := json.Marshal(WebExtractInput{URL: "http://localhost/private"})

	result, err := tool.InvokableRun(context.Background(), string(input))
	if err != nil {
		t.Fatalf("InvokableRun() error = %v, want nil", err)
	}

	var output WebExtractOutput
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.OK {
		t.Fatal("output.OK = true, want false")
	}
	if output.ErrorCode != WebErrorCodeURLBlocked {
		t.Fatalf("error_code = %q", output.ErrorCode)
	}
	if called {
		t.Fatal("third-party crawler should not receive private targets")
	}
}

func TestWebExtractTool_BlocksPrivateLiteralBeforeCrawlers(t *testing.T) {
	logs := captureToolLog(t)
	tool := NewWebExtractTool(WebExtractConfig{
		CrawlerProviders: []string{"jina", "basic"},
		JinaBaseURL:      "https://jina.invalid",
	})
	input, _ := json.Marshal(WebExtractInput{URL: "http://127.0.0.1/private"})

	result, err := tool.InvokableRun(context.Background(), string(input))
	if err != nil {
		t.Fatalf("InvokableRun() error = %v, want nil", err)
	}

	var output WebExtractOutput
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.OK {
		t.Fatal("output.OK = true, want false")
	}
	if !strings.Contains(output.Error, "安全策略") {
		t.Fatalf("error = %q, want safety policy message", output.Error)
	}
	if len(output.AttemptedSources) != 0 {
		t.Fatalf("attempted_sources = %#v, want none before crawlers", output.AttemptedSources)
	}
	requireToolLogContains(t, logs.String(), "blocked_by_url_policy")
}

func TestCleanHTMLTextUnescapesStandardEntities(t *testing.T) {
	got := extractReadableText(`<p>Tom &amp; Jerry&apos;s &#x4e2d;&#25991; &#20013;</p>`, 200)
	want := "Tom & Jerry's 中文 中"
	if got != want {
		t.Fatalf("extractReadableText() = %q, want %q", got, want)
	}
}

func TestWebExtractTool_JinaReader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer jina-key" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if r.URL.Path != "/https://example.com/article" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("Title: 示例文章\nURL Source: https://example.com/article\nMarkdown Content:\n正文内容"))
	}))
	defer server.Close()

	tool := allowMockPageTarget(NewWebExtractTool(WebExtractConfig{
		CrawlerImpl:      "jina",
		CrawlerProviders: []string{"jina"},
		JinaAPIKey:       "jina-key",
		JinaBaseURL:      server.URL,
	}))
	input, _ := json.Marshal(WebExtractInput{URL: "https://example.com/article"})
	result, err := tool.InvokableRun(context.Background(), string(input))
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}

	var output WebExtractOutput
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if !output.OK {
		t.Fatalf("output.OK = false, error = %s", output.Error)
	}
	if output.Title != "示例文章" {
		t.Fatalf("title = %q", output.Title)
	}
	if output.Content == "" {
		t.Fatal("content should not be empty")
	}
	if output.Source != "jina" {
		t.Fatalf("source = %q, want jina", output.Source)
	}
}

func TestWebExtractTool_JinaIntegration(t *testing.T) {
	if os.Getenv("RUN_NET_TESTS") != "1" {
		t.Skip("skipping network test; set RUN_NET_TESTS=1 to run")
	}
	apiKey := os.Getenv("JINA_API_KEY")
	if apiKey == "" {
		t.Skip("skipping jina integration; JINA_API_KEY is empty")
	}

	tool := NewWebExtractTool(WebExtractConfig{
		CrawlerImpl:      "jina",
		CrawlerProviders: []string{"jina"},
		JinaAPIKey:       apiKey,
		MaxContent:       4000,
	})
	input, _ := json.Marshal(WebExtractInput{URL: "https://example.com"})
	result, err := tool.InvokableRun(context.Background(), string(input))
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}

	var output WebExtractOutput
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if !output.OK {
		t.Fatalf("output.OK = false, status = %d, error = %s", output.StatusCode, output.Error)
	}
	if output.Content == "" {
		t.Fatal("content should not be empty")
	}
	t.Logf("Jina returned %d chars", len([]rune(output.Content)))
}

func TestWebExtractTool_FirecrawlPreferred(t *testing.T) {
	firecrawlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer firecrawl-key" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if r.URL.Path != "/v2/scrape" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"markdown":"# 主标题\n正文内容","metadata":{"title":"主标题"}}}`))
	}))
	defer firecrawlServer.Close()

	tool := allowMockPageTarget(NewWebExtractTool(WebExtractConfig{
		CrawlerProviders: []string{"firecrawl", "jina", "basic"},
		FirecrawlAPIKey:  "firecrawl-key",
		FirecrawlBaseURL: firecrawlServer.URL + "/v2",
	}))
	input, _ := json.Marshal(WebExtractInput{URL: "https://example.com/article"})
	result, err := tool.InvokableRun(context.Background(), string(input))
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}

	var output WebExtractOutput
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if !output.OK {
		t.Fatalf("output.OK = false, error = %s", output.Error)
	}
	if output.Title != "主标题" {
		t.Fatalf("title = %q", output.Title)
	}
	if !strings.Contains(output.Content, "正文内容") {
		t.Fatalf("content = %q", output.Content)
	}
	if output.Source != "firecrawl" {
		t.Fatalf("source = %q, want firecrawl", output.Source)
	}
}

func TestWebExtractTool_FirecrawlReceivesWeightedDeadlineBudget(t *testing.T) {
	firecrawlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(140 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"markdown":"weighted deadline content"}}`))
	}))
	defer firecrawlServer.Close()

	tool := allowMockPageTarget(NewWebExtractTool(WebExtractConfig{
		CrawlerProviders: []string{"firecrawl", "jina", "basic"},
		FirecrawlAPIKey:  "firecrawl-key",
		FirecrawlBaseURL: firecrawlServer.URL,
		Timeout:          300 * time.Millisecond,
	}))
	output := runExtract(t, tool, WebExtractInput{URL: "https://example.com/article"})
	if !output.OK || output.Source != "firecrawl" {
		t.Fatalf("output = %#v, want weighted Firecrawl success", output)
	}
}

func TestWebExtractTool_FirecrawlKeyless(t *testing.T) {
	firecrawlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("Authorization = %q, want empty for keyless firecrawl", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"markdown":"# Keyless\n正文","metadata":{"title":"Keyless"}}}`))
	}))
	defer firecrawlServer.Close()

	tool := allowMockPageTarget(NewWebExtractTool(WebExtractConfig{
		CrawlerProviders: []string{"firecrawl"},
		FirecrawlBaseURL: firecrawlServer.URL,
	}))
	input, _ := json.Marshal(WebExtractInput{URL: "https://example.com/keyless"})
	result, err := tool.InvokableRun(context.Background(), string(input))
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}

	var output WebExtractOutput
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if !output.OK {
		t.Fatalf("output.OK = false, error = %s", output.Error)
	}
	if output.Source != "firecrawl" {
		t.Fatalf("source = %q, want firecrawl", output.Source)
	}
}

func TestWebExtractTool_FallbackToJina(t *testing.T) {
	logs := captureToolLog(t)
	firecrawlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":false,"error":"firecrawl failed"}`))
	}))
	defer firecrawlServer.Close()

	jinaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("Title: 备用标题\nMarkdown Content:\nJina 正文"))
	}))
	defer jinaServer.Close()

	tool := allowMockPageTarget(NewWebExtractTool(WebExtractConfig{
		CrawlerProviders: []string{"firecrawl", "jina", "basic"},
		FirecrawlAPIKey:  "firecrawl-key",
		FirecrawlBaseURL: firecrawlServer.URL,
		JinaBaseURL:      jinaServer.URL,
	}))
	input, _ := json.Marshal(WebExtractInput{URL: "https://example.com/article"})
	result, err := tool.InvokableRun(context.Background(), string(input))
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}

	var output WebExtractOutput
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if !output.OK {
		t.Fatalf("output.OK = false, error = %s", output.Error)
	}
	if output.Title != "备用标题" {
		t.Fatalf("title = %q", output.Title)
	}
	if output.Source != "jina" {
		t.Fatalf("source = %q, want jina", output.Source)
	}
	if strings.Join(output.AttemptedSources, ",") != "firecrawl,jina" {
		t.Fatalf("attempted_sources = %#v", output.AttemptedSources)
	}
	requireToolLogContains(t, logs.String(),
		"[web_extract] run_start",
		"crawler_start",
		"crawler_failed",
		"crawler_success",
		"fallback_succeeded",
		"run_success",
		"source=jina",
	)
}

func TestWebExtractTool_FallbackToBasic(t *testing.T) {
	firecrawlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer firecrawlServer.Close()

	jinaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer jinaServer.Close()

	pageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><head><title>基础标题</title></head><body><article><p>基础正文</p></article></body></html>"))
	}))
	defer pageServer.Close()

	tool := allowLoopback(NewWebExtractTool(WebExtractConfig{
		CrawlerProviders: []string{"firecrawl", "jina", "basic"},
		FirecrawlAPIKey:  "firecrawl-key",
		FirecrawlBaseURL: firecrawlServer.URL,
		JinaBaseURL:      jinaServer.URL,
	}))
	input, _ := json.Marshal(WebExtractInput{URL: pageServer.URL})
	result, err := tool.InvokableRun(context.Background(), string(input))
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}

	var output WebExtractOutput
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if !output.OK {
		t.Fatalf("output.OK = false, error = %s", output.Error)
	}
	if output.Title != "基础标题" {
		t.Fatalf("title = %q", output.Title)
	}
	if !strings.Contains(output.Content, "基础正文") {
		t.Fatalf("content = %q", output.Content)
	}
	if output.Source != "basic" {
		t.Fatalf("source = %q, want basic", output.Source)
	}
}

func TestWebExtractTool_BasicFastPathSkipsRefinement(t *testing.T) {
	pageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><head><title>Local</title></head><body><article><p>locally readable content</p></article></body></html>"))
	}))
	defer pageServer.Close()

	mock := &mockSummarizer{summary: "should not run"}
	tool := allowLoopback(NewWebExtractTool(WebExtractConfig{
		CrawlerProviders: []string{"basic", "jina"},
		Summarizer:       mock,
		SummaryEnabled:   true,
	}))
	output := runExtract(t, tool, WebExtractInput{URL: pageServer.URL, Goal: "find the content"})

	if !output.OK || output.Source != "basic" || output.Title != "Local" || output.Content != "locally readable content" {
		t.Fatalf("output = %#v, want direct basic result", output)
	}
	if mock.called || output.RefinementAttempted || output.Summarized || output.Degraded {
		t.Fatalf("basic fast path invoked refinement: output=%#v called=%t", output, mock.called)
	}
}

func TestWebExtractTool_TavilyAndExaContracts(t *testing.T) {
	tests := []struct {
		name      string
		crawler   string
		configure func(WebExtractConfig, string) WebExtractConfig
		assert    func(*testing.T, *http.Request)
		response  string
	}{
		{
			name:    "tavily",
			crawler: "tavily",
			configure: func(cfg WebExtractConfig, endpoint string) WebExtractConfig {
				cfg.TavilyAPIKey, cfg.TavilyBaseURL = "tavily-key", endpoint
				return cfg
			},
			assert: func(t *testing.T, r *http.Request) {
				t.Helper()
				var body map[string]any
				if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer tavily-key" || json.NewDecoder(r.Body).Decode(&body) != nil || len(body["urls"].([]any)) != 1 {
					t.Fatalf("unexpected Tavily request")
				}
			},
			response: `{"results":[{"url":"https://example.com/article","raw_content":"Tavily content"}]}`,
		},
		{
			name:    "exa top-level content",
			crawler: "exa",
			configure: func(cfg WebExtractConfig, endpoint string) WebExtractConfig {
				cfg.ExaAPIKey, cfg.ExaBaseURL = "exa-key", endpoint
				return cfg
			},
			assert: func(t *testing.T, r *http.Request) {
				t.Helper()
				var body map[string]any
				if r.Method != http.MethodPost || r.Header.Get("x-api-key") != "exa-key" || json.NewDecoder(r.Body).Decode(&body) != nil || len(body["urls"].([]any)) != 1 {
					t.Fatalf("unexpected Exa request")
				}
			},
			response: `{"url":"https://example.com/article","content":{"title":"Exa title","text":"Exa content"}}`,
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
			cfg := tt.configure(WebExtractConfig{CrawlerImpl: tt.crawler, CrawlerProviders: []string{tt.crawler}}, server.URL)
			output := runExtract(t, allowMockPageTarget(NewWebExtractTool(cfg)), WebExtractInput{URL: "https://example.com/article"})
			if !output.OK || output.Source != tt.crawler || output.Content == "" {
				t.Fatalf("output = %#v", output)
			}
		})
	}
}

func TestWebExtractTool_AllCrawlersFail(t *testing.T) {
	firecrawlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer firecrawlServer.Close()

	jinaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer jinaServer.Close()

	pageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer pageServer.Close()

	tool := allowLoopback(NewWebExtractTool(WebExtractConfig{
		CrawlerProviders: []string{"firecrawl", "jina", "basic"},
		FirecrawlAPIKey:  "firecrawl-key",
		FirecrawlBaseURL: firecrawlServer.URL,
		JinaBaseURL:      jinaServer.URL,
	}))
	input, _ := json.Marshal(WebExtractInput{URL: pageServer.URL})
	result, err := tool.InvokableRun(context.Background(), string(input))
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}

	var output WebExtractOutput
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.OK {
		t.Fatal("output.OK = true, want false")
	}
	if output.ErrorCode != WebErrorCodeUnavailable || output.Error == "" {
		t.Fatalf("output = %#v", output)
	}
	if strings.Contains(output.Error, "firecrawl") || strings.Contains(output.Error, "jina") || strings.Contains(output.Error, "basic") {
		t.Fatalf("error must not expose provider internals: %q", output.Error)
	}
}

func TestWebExtractTool_FirecrawlIntegration(t *testing.T) {
	if os.Getenv("RUN_NET_TESTS") != "1" {
		t.Skip("skipping network test; set RUN_NET_TESTS=1 to run")
	}
	apiKey := os.Getenv("FIRECRAWL_API_KEY")
	if apiKey == "" {
		t.Skip("skipping firecrawl integration; FIRECRAWL_API_KEY is empty")
	}

	tool := NewWebExtractTool(WebExtractConfig{
		CrawlerProviders: []string{"firecrawl"},
		FirecrawlAPIKey:  apiKey,
		MaxContent:       4000,
	})
	input, _ := json.Marshal(WebExtractInput{URL: "https://example.com"})
	result, err := tool.InvokableRun(context.Background(), string(input))
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}

	var output WebExtractOutput
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if !output.OK {
		t.Fatalf("output.OK = false, status = %d, error = %s", output.StatusCode, output.Error)
	}
	if output.Content == "" {
		t.Fatal("content should not be empty")
	}
	t.Logf("Firecrawl returned %d chars", len([]rune(output.Content)))
}

// mockSummarizer 记录入参并按预设返回值/错误响应，覆盖提炼成功与失败两条路径。
type mockSummarizer struct {
	summary    string
	err        error
	delay      time.Duration
	gotGoal    string
	gotTitle   string
	gotContent string
	gotDetail  string
	called     bool
}

func (m *mockSummarizer) Summarize(ctx context.Context, goal, title, content, detail string) (string, error) {
	m.called = true
	m.gotGoal, m.gotTitle, m.gotContent, m.gotDetail = goal, title, content, detail
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return "", context.Cause(ctx)
		}
	}
	return m.summary, m.err
}

type cancellationStageSummarizer struct {
	started         chan struct{}
	firstOutput     chan struct{}
	emitFirstOutput bool
}

func (m *cancellationStageSummarizer) Summarize(ctx context.Context, _, _, _, _ string) (string, error) {
	close(m.started)
	if m.emitFirstOutput {
		close(m.firstOutput)
	}
	<-ctx.Done()
	return "", context.Cause(ctx)
}

func newJinaPageTool(t *testing.T, cfg WebExtractConfig) (*WebExtractTool, string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("Title: 原始标题\nMarkdown Content:\n这是一段很长的网页正文，包含许多细节内容需要被提炼或截断处理。"))
	}))
	t.Cleanup(server.Close)
	cfg.CrawlerImpl = "jina"
	cfg.CrawlerProviders = []string{"jina"}
	cfg.JinaBaseURL = server.URL
	return allowMockPageTarget(NewWebExtractTool(cfg)), "https://example.com/article"
}

func runExtract(t *testing.T, tool *WebExtractTool, input WebExtractInput) WebExtractOutput {
	t.Helper()
	raw, _ := json.Marshal(input)
	result, err := tool.InvokableRun(context.Background(), string(raw))
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	var output WebExtractOutput
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	return output
}

// 提炼开启且成功：返回提炼结果、标记 summarized，并把 goal/title 透传给提炼器。
func TestWebExtractTool_SummarizeSuccess(t *testing.T) {
	mock := &mockSummarizer{summary: "提炼后的要点"}
	tool, url := newJinaPageTool(t, WebExtractConfig{Summarizer: mock, SummaryEnabled: true})

	output := runExtract(t, tool, WebExtractInput{URL: url, Goal: "查找正文细节"})
	if !output.OK {
		t.Fatalf("output.OK = false, error = %s", output.Error)
	}
	if !output.Summarized {
		t.Fatal("summarized = false, want true")
	}
	if output.Content != "提炼后的要点" {
		t.Fatalf("content = %q, want 提炼后的要点", output.Content)
	}
	if !mock.called {
		t.Fatal("summarizer not called")
	}
	if mock.gotGoal != "查找正文细节" {
		t.Fatalf("goal = %q", mock.gotGoal)
	}
	if mock.gotDetail != "summary" {
		t.Fatalf("detail = %q, want summary", mock.gotDetail)
	}
	if mock.gotTitle != "原始标题" {
		t.Fatalf("title = %q", mock.gotTitle)
	}
}

func TestWebExtractTool_OwnCrawlerTimeoutRemainsBusinessJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	tool := allowMockPageTarget(NewWebExtractTool(WebExtractConfig{
		CrawlerProviders: []string{"jina"},
		JinaBaseURL:      server.URL,
		Timeout:          20 * time.Millisecond,
	}))
	input, _ := json.Marshal(WebExtractInput{URL: "https://example.com/article"})
	result, err := tool.InvokableRun(t.Context(), string(input))
	if err != nil {
		t.Fatalf("owned crawler timeout must remain a business result: %v", err)
	}
	var output WebExtractOutput
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.OK || output.ErrorCode != WebErrorCodeTimeout || !output.Retryable {
		t.Fatalf("output = %#v, want retryable upstream timeout", output)
	}
}

func TestWebExtractTool_ParentCancellationDuringCrawlerPropagatesCause(t *testing.T) {
	requestStarted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		<-r.Context().Done()
	}))
	defer server.Close()

	tool := allowMockPageTarget(NewWebExtractTool(WebExtractConfig{
		CrawlerProviders: []string{"jina"},
		JinaBaseURL:      server.URL,
		Timeout:          time.Second,
	}))
	input, _ := json.Marshal(WebExtractInput{URL: "https://example.com/article"})
	stopCause := errors.New("server draining")
	ctx, cancel := context.WithCancelCause(t.Context())
	result := make(chan struct {
		output string
		err    error
	}, 1)
	go func() {
		output, err := tool.InvokableRun(ctx, string(input))
		result <- struct {
			output string
			err    error
		}{output: output, err: err}
	}()

	<-requestStarted
	cancel(stopCause)
	select {
	case got := <-result:
		if got.err != stopCause {
			t.Fatalf("InvokableRun() error = %v, want exact parent cause %v", got.err, stopCause)
		}
		if got.output != "" {
			t.Fatalf("canceled crawler returned Tool JSON: %q", got.output)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled crawler did not return")
	}
}

func TestWebExtractTool_RefinementDoesNotInheritCrawlerDeadline(t *testing.T) {
	const crawlerTimeout = 20 * time.Millisecond
	mock := &mockSummarizer{
		summary: "慢速但完整的提炼结果",
		delay:   4 * crawlerTimeout,
	}
	tool, url := newJinaPageTool(t, WebExtractConfig{
		Summarizer:     mock,
		SummaryEnabled: true,
		Timeout:        crawlerTimeout,
	})

	started := time.Now()
	output := runExtract(t, tool, WebExtractInput{URL: url, Goal: "验证分阶段超时"})
	if !output.OK || !output.Summarized || output.Content != mock.summary {
		t.Fatalf("output = %#v, want completed refinement", output)
	}
	if elapsed := time.Since(started); elapsed <= crawlerTimeout {
		t.Fatalf("test did not run beyond crawler timeout: %s", elapsed)
	}
}

func TestWebExtractTool_ParentCancellationDuringRefinementPropagatesBeforeAndAfterFirstOutput(t *testing.T) {
	tests := []struct {
		name            string
		emitFirstOutput bool
	}{
		{name: "before first output"},
		{name: "after first output", emitFirstOutput: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summarizer := &cancellationStageSummarizer{
				started:         make(chan struct{}),
				firstOutput:     make(chan struct{}),
				emitFirstOutput: tt.emitFirstOutput,
			}
			tool, url := newJinaPageTool(t, WebExtractConfig{
				Summarizer:     summarizer,
				SummaryEnabled: true,
			})
			input, _ := json.Marshal(WebExtractInput{URL: url, Goal: "verify cancellation"})
			stopCause := errors.New("run canceled")
			ctx, cancel := context.WithCancelCause(t.Context())
			result := make(chan struct {
				output string
				err    error
			}, 1)
			go func() {
				output, err := tool.InvokableRun(ctx, string(input))
				result <- struct {
					output string
					err    error
				}{output: output, err: err}
			}()

			<-summarizer.started
			if tt.emitFirstOutput {
				<-summarizer.firstOutput
			}
			cancel(stopCause)
			select {
			case got := <-result:
				if got.err != stopCause {
					t.Fatalf("InvokableRun() error = %v, want exact parent cause %v", got.err, stopCause)
				}
				if got.output != "" {
					t.Fatalf("canceled refinement returned degraded Tool JSON: %q", got.output)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("canceled refinement did not return")
			}
		})
	}
}

func TestWebExtractTool_SourceSkipsSummarizerAndMarksItsMode(t *testing.T) {
	mock := &mockSummarizer{summary: "不应出现"}
	tool, url := newJinaPageTool(t, WebExtractConfig{Summarizer: mock, SummaryEnabled: true, MaxContent: 8})

	output := runExtract(t, tool, WebExtractInput{URL: url, Detail: "source"})
	if mock.called {
		t.Fatal("source mode must not call the summarizer")
	}
	if output.Summarized || output.Detail != "source" {
		t.Fatalf("output = %#v", output)
	}
	if !strings.Contains(output.Content, "很长的网页正文") || len([]rune(output.Content)) <= 8 {
		t.Fatalf("source content was unexpectedly summarized or truncated: %q", output.Content)
	}
}

func TestWebExtractTool_SourceModePropagatesParentCancellation(t *testing.T) {
	stopCause := errors.New("user stop")
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(stopCause)
	tool := NewWebExtractTool(WebExtractConfig{})

	_, err := tool.finalizeContent(ctx, WebExtractOutput{
		OK:      true,
		URL:     "https://example.com/article",
		Content: "source text",
	}, "", extractDetailSource)
	if err != stopCause {
		t.Fatalf("finalizeContent() error = %v, want exact parent cause %v", err, stopCause)
	}
}

func TestWebExtractTool_SourceMarksCrawlerTruncation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("长", sourceContentLimit+1)))
	}))
	defer server.Close()

	tool := allowLoopback(NewWebExtractTool(WebExtractConfig{CrawlerProviders: []string{"jina"}, JinaBaseURL: server.URL, MaxContent: 4000}))
	output := runExtract(t, tool, WebExtractInput{URL: "https://example.com/article", Detail: "source"})

	if !output.OK || !output.Truncated {
		t.Fatalf("output = %#v, want successful source output marked truncated", output)
	}
	if len([]rune(output.Content)) != sourceContentLimit {
		t.Fatalf("content len = %d, want %d", len([]rune(output.Content)), sourceContentLimit)
	}
}

func TestWebExtractTool_DetailedPassesModeToSummarizer(t *testing.T) {
	mock := &mockSummarizer{summary: "保留完整限定条件的详细结果"}
	tool, url := newJinaPageTool(t, WebExtractConfig{Summarizer: mock, SummaryEnabled: true})

	output := runExtract(t, tool, WebExtractInput{URL: url, Detail: "detailed"})
	if !output.Summarized || output.Detail != "detailed" || mock.gotDetail != "detailed" {
		t.Fatalf("output = %#v, summarizer detail = %q", output, mock.gotDetail)
	}
}

// 提炼开启但失败：降级到按 maxContent 截断，content 取自原文且不标 summarized。
func TestWebExtractTool_SummarizeFailureFallsBackToTruncate(t *testing.T) {
	mock := &mockSummarizer{err: errSummarize}
	tool, url := newJinaPageTool(t, WebExtractConfig{Summarizer: mock, SummaryEnabled: true, MaxContent: 10})

	output := runExtract(t, tool, WebExtractInput{URL: url})
	if !output.OK {
		t.Fatalf("output.OK = false, error = %s", output.Error)
	}
	if output.Summarized {
		t.Fatal("summarized = true, want false on fallback")
	}
	if !output.RefinementAttempted || !output.Degraded || output.DegradationReason != RefinementFailed {
		t.Fatalf("output = %#v, want explicit refinement failure", output)
	}
	if len([]rune(output.Content)) != 10 {
		t.Fatalf("content len = %d, want 10 (truncated)", len([]rune(output.Content)))
	}
	if !output.Truncated {
		t.Fatal("truncated = false, want true for bounded fallback")
	}
}

func TestWebExtractTool_RefinementFailureLogClassifiesFirstOutputTimeout(t *testing.T) {
	logs := captureToolLog(t)
	mock := &mockSummarizer{err: modelstream.ErrFirstOutputTimeout}
	tool, url := newJinaPageTool(t, WebExtractConfig{Summarizer: mock, SummaryEnabled: true})

	output := runExtract(t, tool, WebExtractInput{URL: url})
	if !output.OK || !output.Degraded || output.DegradationReason != RefinementFailed {
		t.Fatalf("output = %#v, want degraded source fallback", output)
	}
	requireToolLogContains(t, logs.String(), "summarize_failed", "error_type=first_output_timeout")
}

func TestWebExtractTool_DetailedFailureDoesNotClaimCompleteRefinement(t *testing.T) {
	mock := &mockSummarizer{err: NewRefinementError(RefinementCooldown)}
	tool, url := newJinaPageTool(t, WebExtractConfig{Summarizer: mock, SummaryEnabled: true})

	output := runExtract(t, tool, WebExtractInput{URL: url, Detail: "detailed"})
	if !output.OK || !output.Degraded || output.DegradationReason != RefinementCooldown {
		t.Fatalf("output = %#v, want explicit detailed degradation", output)
	}
	if output.Summarized {
		t.Fatal("summarized = true, want false for degraded detailed output")
	}
}

// 提炼关闭：不调用提炼器，直接按 maxContent 截断。
func TestWebExtractTool_SummaryDisabledTruncates(t *testing.T) {
	mock := &mockSummarizer{summary: "不应出现"}
	tool, url := newJinaPageTool(t, WebExtractConfig{Summarizer: mock, SummaryEnabled: false, MaxContent: 8})

	output := runExtract(t, tool, WebExtractInput{URL: url})
	if mock.called {
		t.Fatal("summarizer called while disabled")
	}
	if output.Summarized {
		t.Fatal("summarized = true, want false")
	}
	if output.RefinementAttempted || !output.Degraded || output.DegradationReason != RefinementDisabled {
		t.Fatalf("output = %#v, want disabled refinement degradation", output)
	}
	if len([]rune(output.Content)) != 8 {
		t.Fatalf("content len = %d, want 8 (truncated)", len([]rune(output.Content)))
	}
}

var errSummarize = errSummarizeFailed{}

type staticResolver struct {
	ips []net.IPAddr
}

func (r staticResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return r.ips, nil
}

type errSummarizeFailed struct{}

func (errSummarizeFailed) Error() string { return "summarize failed" }
