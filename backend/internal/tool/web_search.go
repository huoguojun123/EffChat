package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// WebSearchTool 网页搜索工具（对接 SearXNG JSON API）
type WebSearchTool struct {
	provider   string
	providers  []string
	searxngURL string
	tavilyURL  string
	tavilyKey  string
	braveURL   string
	braveKey   string
	exaURL     string
	exaKey     string
	bochaURL   string
	bochaKey   string
	client     *http.Client
	maxResults int
	timeout    time.Duration
}

type WebSearchConfig struct {
	Provider     string
	Providers    []string
	SearXNGURL   string
	TavilyAPIKey string
	TavilyURL    string
	BraveAPIKey  string
	BraveURL     string
	ExaAPIKey    string
	ExaURL       string
	BochaAPIKey  string
	BochaURL     string
	MaxResults   int
	Timeout      time.Duration
}

// WebSearchInput 搜索输入
type WebSearchInput struct {
	Query string `json:"query"`
}

// WebSearchOutput 搜索输出
type WebSearchOutput struct {
	Summary          string     `json:"summary"`                     // 搜索结果摘要
	Citations        []Citation `json:"citations"`                   // 引用来源
	Source           string     `json:"source,omitempty"`            // 实际成功的搜索 provider
	AttemptedSources []string   `json:"attempted_sources,omitempty"` // 本次尝试过的 provider
	SearchFailed     bool       `json:"search_failed,omitempty"`     // 搜索是否失败（失败时模型应改用自身知识并向用户声明）
	ErrorCode        string     `json:"error_code,omitempty"`
	Error            string     `json:"error,omitempty"`
	Retryable        bool       `json:"retryable,omitempty"`
}

const (
	WebErrorCodeUnavailable = "upstream_unavailable"
	WebErrorCodeTimeout     = "upstream_timeout"
	WebErrorCodeURLBlocked  = "url_blocked"
)

// Citation 引用
type Citation struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// searxngResponse SearXNG JSON API 响应
type searxngResponse struct {
	Query               string     `json:"query"`
	UnresponsiveEngines [][]string `json:"unresponsive_engines"`
	Results             []struct {
		URL     string  `json:"url"`
		Title   string  `json:"title"`
		Content string  `json:"content"`
		Score   float64 `json:"score"`
	} `json:"results"`
}

type tavilyResponse struct {
	Answer  string `json:"answer"`
	Results []struct {
		URL        string  `json:"url"`
		Title      string  `json:"title"`
		Content    string  `json:"content"`
		RawContent string  `json:"raw_content"`
		Score      float64 `json:"score"`
	} `json:"results"`
}

func NewWebSearchTool(cfg WebSearchConfig) *WebSearchTool {
	if cfg.Provider == "" {
		cfg.Provider = "searxng"
	}
	if len(cfg.Providers) == 0 {
		cfg.Providers = []string{cfg.Provider}
	}
	cfg.SearXNGURL = strings.TrimRight(cfg.SearXNGURL, "/")
	if cfg.TavilyURL == "" {
		cfg.TavilyURL = "https://api.tavily.com/search"
	}
	if cfg.BraveURL == "" {
		cfg.BraveURL = "https://api.search.brave.com/res/v1/web/search"
	}
	if cfg.ExaURL == "" {
		cfg.ExaURL = "https://api.exa.ai/search"
	}
	if cfg.BochaURL == "" {
		cfg.BochaURL = "https://api.bochaai.com/v1/web-search"
	}
	if cfg.MaxResults <= 0 {
		cfg.MaxResults = 5
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}

	return &WebSearchTool{
		provider:   cfg.Provider,
		providers:  normalizeSearchProviders(cfg.Provider, cfg.Providers),
		searxngURL: cfg.SearXNGURL,
		tavilyURL:  cfg.TavilyURL,
		tavilyKey:  strings.TrimSpace(cfg.TavilyAPIKey),
		braveURL:   strings.TrimRight(cfg.BraveURL, "/"),
		braveKey:   strings.TrimSpace(cfg.BraveAPIKey),
		exaURL:     strings.TrimRight(cfg.ExaURL, "/"),
		exaKey:     strings.TrimSpace(cfg.ExaAPIKey),
		bochaURL:   strings.TrimRight(cfg.BochaURL, "/"),
		bochaKey:   strings.TrimSpace(cfg.BochaAPIKey),
		client: &http.Client{
			Timeout: cfg.Timeout,
		},
		maxResults: cfg.MaxResults,
		timeout:    cfg.Timeout,
	}
}

// Info 返回工具信息
func (t *WebSearchTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "web_search",
		Desc: "Search the web for current, local, niche, or external information. Use this for recent events, prices, laws, policies, schedules, releases, software/library versions, company or public-figure facts, local facts, niche topics, or anything that may have changed. If the cost of a small outdated fact would be high, search before answering; confidence from memory is not enough. Reuse Session Web Evidence when it already answers the follow-up, and search again only when evidence is insufficient, stale, or clearly about a different topic. Use focused queries with all important entities; if the first search misses, issue a meaningfully different query rather than repeating the same words. Do not mention a knowledge cutoff or offer to search later when the request already needs live evidence.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"query": {
				Type:     schema.String,
				Desc:     "A focused search query with the key entities and facts sought. Include the current year/date when freshness matters; change terms if a previous query missed.",
				Required: true,
			},
		}),
	}, nil
}

// InvokableRun 执行搜索
func (t *WebSearchTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	started := time.Now()
	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	// 解析输入
	var input WebSearchInput
	if err := json.Unmarshal([]byte(argumentsInJSON), &input); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	query := strings.TrimSpace(input.Query)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	log.Printf("[web_search] run_start query_chars=%d providers=%s max_results=%d", toolLogRuneCount(query), strings.Join(t.providers, ","), t.maxResults)

	// 执行搜索
	results, source, attempted, err := t.search(ctx, query)
	if err != nil {
		// 优雅降级：搜索是外部依赖，失败不应中止整个 agent run。
		// 返回结构化“工具结果”而非 Go error —— 后者会让 Eino ToolNode 触发
		// NodeRunError 并中止本轮对话。把失败信号交回模型，由它改用自身知识
		// 作答，并向用户声明实时数据未能获取。
		log.Printf("[web_search] run_degraded query_chars=%d attempted=%s duration_ms=%d error_type=%s", toolLogRuneCount(query), strings.Join(attempted, ","), toolLogDurationMS(started), webErrorCode(ctx, err))
		output := WebSearchOutput{
			Summary:          "Web search is currently unavailable. Answer the user using your own knowledge, and explicitly tell them that live or real-time data could not be retrieved and may be outdated.",
			Citations:        []Citation{},
			AttemptedSources: attempted,
			SearchFailed:     true,
			ErrorCode:        webErrorCode(ctx, err),
			Error:            webErrorMessage(ctx, err),
			Retryable:        true,
		}
		outputJSON, marshalErr := json.Marshal(output)
		if marshalErr != nil {
			return "", fmt.Errorf("failed to marshal search failure output: %w", marshalErr)
		}
		return string(outputJSON), nil
	}

	// 构造输出
	log.Printf("[web_search] run_success query_chars=%d source=%s attempted=%s results=%d duration_ms=%d", toolLogRuneCount(query), source, strings.Join(attempted, ","), len(results), toolLogDurationMS(started))
	output := WebSearchOutput{
		Summary:          t.generateSummary(query, results),
		Citations:        results,
		Source:           source,
		AttemptedSources: attempted,
	}

	// 返回 JSON
	outputJSON, err := json.Marshal(output)
	if err != nil {
		return "", fmt.Errorf("failed to marshal output: %w", err)
	}

	return string(outputJSON), nil
}

func (t *WebSearchTool) search(ctx context.Context, query string) ([]Citation, string, []string, error) {
	providers := t.providers
	if len(providers) == 0 {
		providers = []string{t.provider}
	}

	failures := make([]string, 0, len(providers))
	attempted := make([]string, 0, len(providers))
	for index, provider := range providers {
		attempted = append(attempted, provider)
		providerStarted := time.Now()
		budget := fallbackServiceBudget(ctx, t.timeout, index, len(providers))
		log.Printf("[web_search] provider_start query_chars=%d provider=%s attempt=%d/%d budget_ms=%d", toolLogRuneCount(query), provider, len(attempted), len(providers), budget.Milliseconds())
		var (
			results []Citation
			err     error
		)
		providerCtx, cancel := context.WithTimeout(ctx, budget)
		switch provider {
		case "searxng":
			results, err = t.searchSearXNG(providerCtx, query)
		case "tavily":
			results, err = t.searchTavily(providerCtx, query)
		case "brave":
			results, err = t.searchBrave(providerCtx, query)
		case "exa":
			results, err = t.searchExa(providerCtx, query)
		case "bocha":
			results, err = t.searchBocha(providerCtx, query)
		default:
			err = fmt.Errorf("unsupported search provider: %s", provider)
		}
		cancel()
		if err == nil && len(results) > 0 {
			log.Printf("[web_search] provider_success query_chars=%d provider=%s results=%d duration_ms=%d", toolLogRuneCount(query), provider, len(results), toolLogDurationMS(providerStarted))
			if len(failures) > 0 {
				log.Printf("[web_search] fallback_succeeded query_chars=%d source=%s attempted=%s previous_failures=%s", toolLogRuneCount(query), provider, strings.Join(attempted, ","), strings.Join(failures, ","))
			}
			return results, provider, attempted, nil
		}
		if err == nil {
			err = fmt.Errorf("no results found")
		}
		log.Printf("[web_search] provider_failed query_chars=%d provider=%s duration_ms=%d error_type=%s", toolLogRuneCount(query), provider, toolLogDurationMS(providerStarted), webErrorCode(providerCtx, err))
		failures = append(failures, provider)
		if ctx.Err() != nil {
			break
		}
	}
	log.Printf("[web_search] all_providers_failed query_chars=%d providers=%s error_type=%s failures=%s", toolLogRuneCount(query), strings.Join(attempted, ","), webErrorCode(ctx, ctx.Err()), strings.Join(failures, ","))
	return nil, "", attempted, fmt.Errorf("all search providers failed")
}

func ProbeWebSearchService(ctx context.Context, cfg WebSearchConfig) error {
	t := NewWebSearchTool(cfg)
	_, _, _, err := t.search(ctx, "connection test")
	return err
}

func (t *WebSearchTool) searchSearXNG(ctx context.Context, query string) ([]Citation, error) {
	if strings.TrimSpace(t.searxngURL) == "" {
		return nil, fmt.Errorf("searxng base URL is not configured")
	}
	params := url.Values{}
	params.Set("q", query)
	params.Set("format", "json")

	searchURL := t.searxngURL + "/search?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; EffChat/1.0)")
	req.Header.Set("Accept", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("searxng returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("failed to read searxng response: %w", err)
	}
	var searxResp searxngResponse
	if err := json.Unmarshal(body, &searxResp); err != nil {
		if looksLikeHTMLResponse(body) {
			return nil, fmt.Errorf("searxng returned HTML instead of JSON; check the Base URL and ensure SearXNG JSON format is enabled")
		}
		return nil, fmt.Errorf("failed to decode searxng response: %w", err)
	}

	if len(searxResp.Results) == 0 {
		if detail := formatSearXNGUnresponsiveEngines(searxResp.UnresponsiveEngines); detail != "" {
			return nil, fmt.Errorf("no results found; searxng engines unavailable: %s", detail)
		}
		return nil, fmt.Errorf("no results found for query: %s", query)
	}

	citations := make([]Citation, 0, t.maxResults)
	for _, r := range searxResp.Results {
		if len(citations) >= t.maxResults {
			break
		}
		citation := Citation{
			Title:   cleanSearchText(r.Title),
			URL:     cleanSearchURL(r.URL),
			Snippet: cleanSearchText(r.Content),
		}
		if citation.URL == "" {
			continue
		}
		if citation.Title == "" {
			citation.Title = citation.URL
		}
		citations = append(citations, citation)
	}

	return citations, nil
}

func formatSearXNGUnresponsiveEngines(items [][]string) string {
	if len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		if len(item) == 0 || strings.TrimSpace(item[0]) == "" {
			continue
		}
		if len(item) > 1 && strings.TrimSpace(item[1]) != "" {
			parts = append(parts, strings.TrimSpace(item[0])+" ("+strings.TrimSpace(item[1])+")")
			continue
		}
		parts = append(parts, strings.TrimSpace(item[0]))
	}
	return strings.Join(parts, ", ")
}

func looksLikeHTMLResponse(body []byte) bool {
	trimmed := strings.TrimSpace(string(body))
	lower := strings.ToLower(trimmed)
	return strings.HasPrefix(lower, "<!doctype html") || strings.HasPrefix(lower, "<html") || strings.HasPrefix(trimmed, "<")
}

func (t *WebSearchTool) searchTavily(ctx context.Context, query string) ([]Citation, error) {
	payload := map[string]interface{}{
		"query":          query,
		"search_depth":   "basic",
		"max_results":    t.maxResults,
		"include_answer": false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", t.tavilyURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "EffChat/1.0")
	if t.tavilyKey != "" {
		req.Header.Set("Authorization", "Bearer "+t.tavilyKey)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if readErr != nil {
		return nil, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("tavily returned status %d", resp.StatusCode)
	}

	var tavilyResp tavilyResponse
	if err := json.Unmarshal(respBody, &tavilyResp); err != nil {
		return nil, fmt.Errorf("failed to decode tavily response: %w", err)
	}
	if len(tavilyResp.Results) == 0 {
		return nil, fmt.Errorf("no results found for query: %s", query)
	}

	citations := make([]Citation, 0, t.maxResults)
	for _, r := range tavilyResp.Results {
		if len(citations) >= t.maxResults {
			break
		}
		snippet := r.Content
		if snippet == "" {
			snippet = r.RawContent
		}
		citation := Citation{
			Title:   cleanSearchText(r.Title),
			URL:     cleanSearchURL(r.URL),
			Snippet: cleanSearchText(snippet),
		}
		if citation.URL == "" {
			continue
		}
		if citation.Title == "" {
			citation.Title = citation.URL
		}
		citations = append(citations, citation)
	}
	return citations, nil
}

func (t *WebSearchTool) searchBrave(ctx context.Context, query string) ([]Citation, error) {
	if t.braveKey == "" {
		return nil, fmt.Errorf("brave API key is not configured")
	}
	params := url.Values{}
	params.Set("q", query)
	params.Set("count", fmt.Sprintf("%d", t.maxResults))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.braveURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", t.braveKey)
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("brave returned status %d", resp.StatusCode)
	}
	var payload struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode brave response: %w", err)
	}
	citations := make([]Citation, 0, len(payload.Web.Results))
	for _, item := range payload.Web.Results {
		if citation := normalizeCitation(item.Title, item.URL, item.Description); citation.URL != "" {
			citations = append(citations, citation)
		}
		if len(citations) >= t.maxResults {
			break
		}
	}
	return citations, nil
}

func (t *WebSearchTool) searchExa(ctx context.Context, query string) ([]Citation, error) {
	if t.exaKey == "" {
		return nil, fmt.Errorf("exa API key is not configured")
	}
	body, err := json.Marshal(map[string]interface{}{"query": query, "type": "auto", "numResults": t.maxResults, "contents": map[string]bool{"text": true}})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.exaURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-api-key", t.exaKey)
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("exa returned status %d", resp.StatusCode)
	}
	var payload struct {
		Results []struct {
			Title      string   `json:"title"`
			URL        string   `json:"url"`
			Text       string   `json:"text"`
			Highlights []string `json:"highlights"`
		} `json:"results"`
	}
	if err := json.Unmarshal(responseBody, &payload); err != nil {
		return nil, fmt.Errorf("decode exa response: %w", err)
	}
	citations := make([]Citation, 0, len(payload.Results))
	for _, item := range payload.Results {
		snippet := item.Text
		if len(item.Highlights) > 0 {
			snippet = strings.Join(item.Highlights, " ")
		}
		if citation := normalizeCitation(item.Title, item.URL, snippet); citation.URL != "" {
			citations = append(citations, citation)
		}
		if len(citations) >= t.maxResults {
			break
		}
	}
	return citations, nil
}

func (t *WebSearchTool) searchBocha(ctx context.Context, query string) ([]Citation, error) {
	if t.bochaKey == "" {
		return nil, fmt.Errorf("bocha API key is not configured")
	}
	body, err := json.Marshal(map[string]interface{}{"query": query, "count": t.maxResults, "freshness": "noLimit"})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.bochaURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.bochaKey)
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("bocha returned status %d", resp.StatusCode)
	}
	var payload struct {
		Code int `json:"code"`
		Data struct {
			WebPages struct {
				Value []struct {
					Name    string `json:"name"`
					URL     string `json:"url"`
					Snippet string `json:"snippet"`
				} `json:"value"`
			} `json:"webPages"`
		} `json:"data"`
	}
	if err := json.Unmarshal(responseBody, &payload); err != nil {
		return nil, fmt.Errorf("decode bocha response: %w", err)
	}
	if payload.Code != 0 {
		return nil, fmt.Errorf("bocha returned error code %d", payload.Code)
	}
	citations := make([]Citation, 0, len(payload.Data.WebPages.Value))
	for _, item := range payload.Data.WebPages.Value {
		if citation := normalizeCitation(item.Name, item.URL, item.Snippet); citation.URL != "" {
			citations = append(citations, citation)
		}
		if len(citations) >= t.maxResults {
			break
		}
	}
	return citations, nil
}

func normalizeCitation(title, rawURL, snippet string) Citation {
	citation := Citation{Title: cleanSearchText(title), URL: cleanSearchURL(rawURL), Snippet: cleanSearchText(snippet)}
	if citation.Title == "" {
		citation.Title = citation.URL
	}
	return citation
}

func fallbackServiceBudget(ctx context.Context, fallback time.Duration, index, total int) time.Duration {
	if total < 1 {
		total = 1
	}
	if index < 0 {
		index = 0
	}
	if index >= total {
		index = total - 1
	}
	budget := fallback
	if deadline, ok := ctx.Deadline(); ok {
		budget = time.Until(deadline)
	}
	if budget <= 0 {
		return time.Millisecond
	}

	remainingWeight := 0
	for position := index; position < total; position++ {
		remainingWeight += fallbackServiceWeight(position)
	}
	return budget * time.Duration(fallbackServiceWeight(index)) / time.Duration(remainingWeight)
}

func fallbackServiceWeight(position int) int {
	switch position {
	case 0:
		return 4
	case 1:
		return 2
	default:
		return 1
	}
}

func (t *WebSearchTool) generateSummary(query string, citations []Citation) string {
	if len(citations) == 0 {
		return "No results found."
	}

	var summary strings.Builder
	summary.WriteString(fmt.Sprintf("Search results for %q:\n\n", query))

	for i, citation := range citations {
		summary.WriteString(fmt.Sprintf("[%d] %s\n", i+1, citation.Title))
		summary.WriteString(fmt.Sprintf("URL: %s\n", citation.URL))
		if citation.Snippet != "" {
			summary.WriteString(fmt.Sprintf("%s\n", citation.Snippet))
		}
		summary.WriteString("\n")
	}

	return summary.String()
}

func normalizeSearchProviders(provider string, providers []string) []string {
	seen := make(map[string]bool, len(providers)+1)
	normalized := make([]string, 0, len(providers)+1)
	for _, item := range append(providers, provider) {
		item = strings.ToLower(strings.TrimSpace(item))
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		normalized = append(normalized, item)
	}
	if len(normalized) == 0 {
		return []string{"searxng"}
	}
	return normalized
}

func cleanSearchURL(raw string) string {
	candidate := strings.TrimSpace(raw)
	candidate = strings.Trim(candidate, "[]()<>\"'")
	if idx := strings.Index(candidate, `\n`); idx >= 0 {
		candidate = candidate[:idx]
	}
	if idx := strings.IndexAny(candidate, "\r\n\t "); idx >= 0 {
		candidate = candidate[:idx]
	}
	parsed, err := url.Parse(candidate)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return ""
	}
	return parsed.String()
}

func cleanSearchText(raw string) string {
	text := strings.TrimSpace(raw)
	text = markdownLinkPattern.ReplaceAllString(text, "$1")
	text = danglingMarkdownURLPattern.ReplaceAllString(text, "")
	text = strings.ReplaceAll(text, "\r", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.Join(strings.Fields(text), " ")
	text = strings.Trim(text, "[]()<>\"'")
	if len([]rune(text)) > 260 {
		runes := []rune(text)
		text = string(runes[:260]) + "..."
	}
	return text
}

var markdownLinkPattern = regexp.MustCompile(`\[(.*?)\]\(https?://[^\s)]+\)`)
var danglingMarkdownURLPattern = regexp.MustCompile(`\]\(https?://[^\s)]+\)`)

// 确保实现了 tool.InvokableTool 接口
var _ tool.InvokableTool = (*WebSearchTool)(nil)
