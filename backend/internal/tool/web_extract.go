package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	modelusage "github.com/huoguojun123/EffChat/internal/usage"
)

// Summarizer 把抓取到的网页正文按 goal 提炼成要点。由 agent 层注入（独立小模型）。
// 工具不依赖 agent 包，仅依赖此最小接口，保持分层解耦。
type Summarizer interface {
	Summarize(ctx context.Context, goal, title, content, detail string) (string, error)
}

const (
	extractDetailSummary  = "summary"
	extractDetailDetailed = "detailed"
	extractDetailSource   = "source"
	detailedContentLimit  = 8000
	sourceContentLimit    = 16000

	RefinementDisabled        = "refinement_disabled"
	RefinementUnavailable     = "refinement_unavailable"
	RefinementCooldown        = "refinement_cooldown"
	RefinementFailed          = "refinement_failed"
	RefinementSourceTruncated = "source_truncated"
)

type RefinementError struct {
	Reason string
}

func NewRefinementError(reason string) error {
	return &RefinementError{Reason: normalizeRefinementReason(reason)}
}

func (e *RefinementError) Error() string {
	return e.Reason
}

func IsRefinementReason(err error, reason string) bool {
	var refinementErr *RefinementError
	return errors.As(err, &refinementErr) && refinementErr.Reason == reason
}

type WebExtractTool struct {
	crawlerImpl      string
	crawlerProviders []string
	firecrawlAPIKey  string
	firecrawlBaseURL string
	jinaAPIKey       string
	jinaBaseURL      string
	tavilyAPIKey     string
	tavilyBaseURL    string
	exaAPIKey        string
	exaBaseURL       string
	client           *http.Client
	basicClient      *http.Client // SSRF 加固客户端，仅 basic 爬虫直连任意 URL 时使用
	basicResolver    ipResolver
	ipBlocked        func(net.IP) bool // IP 拦截策略；生产用 isBlockedIP，测试可放宽以访问本地 mock
	maxContent       int               // 最终返回给模型的正文上限（提炼失败/关闭提炼时的截断阈值）
	rawContentLimit  int               // 抓取阶段保留的原文上限（喂给提炼小模型），远大于 maxContent
	summarizer       Summarizer        // 非 nil 且 summaryEnabled 时，用小模型按 goal 提炼正文
	summaryEnabled   bool
	timeout          time.Duration
}

type WebExtractConfig struct {
	CrawlerImpl      string
	CrawlerProviders []string
	FirecrawlAPIKey  string
	FirecrawlBaseURL string
	JinaAPIKey       string
	JinaBaseURL      string
	TavilyAPIKey     string
	TavilyBaseURL    string
	ExaAPIKey        string
	ExaBaseURL       string
	MaxContent       int
	Timeout          time.Duration
	Summarizer       Summarizer
	SummaryEnabled   bool
}

type WebExtractInput struct {
	URL    string `json:"url"`
	Goal   string `json:"goal,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type WebExtractOutput struct {
	OK                  bool     `json:"ok"`
	URL                 string   `json:"url"`
	Title               string   `json:"title,omitempty"`
	Content             string   `json:"content,omitempty"`
	Source              string   `json:"source,omitempty"`
	AttemptedSources    []string `json:"attempted_sources,omitempty"`
	Summarized          bool     `json:"summarized,omitempty"`
	Detail              string   `json:"detail,omitempty"`
	Truncated           bool     `json:"truncated,omitempty"`
	RefinementAttempted bool     `json:"refinement_attempted,omitempty"`
	Degraded            bool     `json:"degraded,omitempty"`
	DegradationReason   string   `json:"degradation_reason,omitempty"`
	StatusCode          int      `json:"status_code,omitempty"`
	ErrorCode           string   `json:"error_code,omitempty"`
	Error               string   `json:"error,omitempty"`
	Retryable           bool     `json:"retryable,omitempty"`
}

func NewWebExtractTool(cfg WebExtractConfig) *WebExtractTool {
	if cfg.CrawlerImpl == "" {
		cfg.CrawlerImpl = "basic"
	}
	crawlerProviders := normalizeCrawlerProviders(cfg.CrawlerProviders, cfg.CrawlerImpl)
	if cfg.MaxContent <= 0 {
		cfg.MaxContent = 4000
	}
	// 原文上限：抓取阶段保留更多正文供小模型提炼；远大于最终输出上限。
	rawContentLimit := cfg.MaxContent * 4
	if rawContentLimit < 16000 {
		rawContentLimit = 16000
	}
	if cfg.FirecrawlBaseURL == "" {
		cfg.FirecrawlBaseURL = "https://api.firecrawl.dev/v2"
	}
	if cfg.JinaBaseURL == "" {
		cfg.JinaBaseURL = "https://r.jina.ai"
	}
	if cfg.TavilyBaseURL == "" {
		cfg.TavilyBaseURL = "https://api.tavily.com/extract"
	}
	if cfg.ExaBaseURL == "" {
		cfg.ExaBaseURL = "https://api.exa.ai/contents"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 20 * time.Second
	}
	resolver := net.DefaultResolver
	return &WebExtractTool{
		crawlerImpl:      cfg.CrawlerImpl,
		crawlerProviders: crawlerProviders,
		firecrawlAPIKey:  strings.TrimSpace(cfg.FirecrawlAPIKey),
		firecrawlBaseURL: strings.TrimRight(cfg.FirecrawlBaseURL, "/"),
		jinaAPIKey:       strings.TrimSpace(cfg.JinaAPIKey),
		jinaBaseURL:      strings.TrimRight(cfg.JinaBaseURL, "/"),
		tavilyAPIKey:     strings.TrimSpace(cfg.TavilyAPIKey),
		tavilyBaseURL:    strings.TrimRight(cfg.TavilyBaseURL, "/"),
		exaAPIKey:        strings.TrimSpace(cfg.ExaAPIKey),
		exaBaseURL:       strings.TrimRight(cfg.ExaBaseURL, "/"),
		client: &http.Client{
			Timeout: cfg.Timeout,
		},
		basicClient:     newGuardedHTTPClient(cfg.Timeout, resolver, isBlockedIP),
		basicResolver:   resolver,
		ipBlocked:       isBlockedIP,
		maxContent:      cfg.MaxContent,
		rawContentLimit: rawContentLimit,
		summarizer:      cfg.Summarizer,
		summaryEnabled:  cfg.SummaryEnabled,
		timeout:         cfg.Timeout,
	}
}

func (t *WebExtractTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "web_extract",
		Desc: "Extract focused readable text from one exact web page URL. Use this when the user provided a URL, when a URL came from web_search or Session Web Evidence, when snippets are insufficient, or when a specific claim needs source-level verification from the page itself. Only extract URLs that appeared in the conversation, Session Web Evidence, or web_search results. Do not invent URLs, build URLs from memory, or guess documentation paths; search first if you do not have the exact URL. Set a concrete goal so the extractor keeps the facts that matter. Use detail=source for original wording, exhaustive reading, or exact quotation; detail=detailed for broad evidence; otherwise use summary.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"url": {
				Type:     schema.String,
				Desc:     "The exact HTTP or HTTPS URL to read. It should come from the user, web_search, or Session Web Evidence.",
				Required: true,
			},
			"goal": {
				Type:     schema.String,
				Desc:     "The exact question, claim, fields, or facts to verify on this page. Use a concrete goal so the extraction summary keeps relevant evidence.",
				Required: false,
			},
			"detail": {
				Type:     schema.String,
				Desc:     "summary (default), detailed, or source. source returns cleaned source text without model rewriting and marks truncation when needed.",
				Required: false,
			},
		}),
	}, nil
}

func (t *WebExtractTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	started := time.Now()
	if cause := toolParentCause(ctx); cause != nil {
		return "", cause
	}
	var input WebExtractInput
	if err := json.Unmarshal([]byte(argumentsInJSON), &input); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	pageURL := strings.TrimSpace(input.URL)
	if pageURL == "" {
		return "", fmt.Errorf("url is required")
	}
	detail, ok := normalizeExtractDetail(input.Detail)
	if !ok {
		return "", fmt.Errorf("invalid detail")
	}
	parsed, err := url.Parse(pageURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("invalid url")
	}
	if err := validateURLLiteralHostPolicy(parsed, t.ipBlocked); err != nil {
		log.Printf("[web_extract] blocked_by_url_policy url_chars=%d crawler=preflight error_type=%s", toolLogRuneCount(pageURL), WebErrorCodeURLBlocked)
		return marshalExtractOutput(WebExtractOutput{
			OK:        false,
			URL:       pageURL,
			ErrorCode: WebErrorCodeURLBlocked,
			Error:     "该地址被安全策略拦截",
		})
	}
	log.Printf("[web_extract] run_start url_chars=%d goal_chars=%d crawlers=%s summary_enabled=%t", toolLogRuneCount(pageURL), toolLogRuneCount(input.Goal), strings.Join(t.crawlerProviders, ","), t.summaryEnabled && t.summarizer != nil)

	// Crawling is a finite external-service phase and keeps an absolute budget.
	// Refinement starts only after source text exists and receives the semantic
	// parent context; its own streaming collector then applies a fresh
	// first-output timeout without inheriting crawler time already spent.
	crawlCtx, cancelCrawl := context.WithTimeout(ctx, t.timeout)
	defer cancelCrawl()

	failures := make([]string, 0, len(t.crawlerProviders))
	attempted := make([]string, 0, len(t.crawlerProviders))
	var lastStatus int
	for index, crawler := range t.crawlerProviders {
		if cause := toolParentCause(ctx); cause != nil {
			return "", cause
		}
		attempted = append(attempted, crawler)
		crawlerStarted := time.Now()
		budget := fallbackServiceBudget(crawlCtx, t.timeout, index, len(t.crawlerProviders))
		log.Printf("[web_extract] crawler_start url_chars=%d crawler=%s attempt=%d/%d budget_ms=%d", toolLogRuneCount(pageURL), crawler, len(attempted), len(t.crawlerProviders), budget.Milliseconds())
		crawlerCtx, crawlerCancel := context.WithTimeout(crawlCtx, budget)
		output := t.extractWithCrawler(crawlerCtx, crawler, pageURL)
		crawlerErr := context.Cause(crawlerCtx)
		crawlerCancel()
		if cause := toolParentCause(ctx); cause != nil {
			return "", cause
		}
		if output.StatusCode != 0 {
			lastStatus = output.StatusCode
		}
		if output.OK && strings.TrimSpace(output.Content) != "" {
			output.Source = crawler
			output.AttemptedSources = attempted
			log.Printf("[web_extract] crawler_success url_chars=%d crawler=%s status=%d content_chars=%d duration_ms=%d", toolLogRuneCount(pageURL), crawler, output.StatusCode, toolLogRuneCount(output.Content), toolLogDurationMS(crawlerStarted))
			if len(failures) > 0 {
				log.Printf("[web_extract] fallback_succeeded url_chars=%d source=%s attempted=%s previous_failures=%s", toolLogRuneCount(pageURL), crawler, strings.Join(attempted, ","), strings.Join(failures, ","))
			}
			cancelCrawl()
			finalized, err := t.finalizeContent(ctx, output, input.Goal, detail)
			if err != nil {
				log.Printf("[web_extract] run_canceled url_chars=%d source=%s attempted=%s duration_ms=%d error_type=canceled", toolLogRuneCount(pageURL), output.Source, strings.Join(attempted, ","), toolLogDurationMS(started))
				return "", err
			}
			log.Printf("[web_extract] run_success url_chars=%d source=%s attempted=%s summarized=%t content_chars=%d duration_ms=%d", toolLogRuneCount(pageURL), finalized.Source, strings.Join(attempted, ","), finalized.Summarized, toolLogRuneCount(finalized.Content), toolLogDurationMS(started))
			return marshalExtractOutput(finalized)
		}
		log.Printf("[web_extract] crawler_failed url_chars=%d crawler=%s status=%d duration_ms=%d error_type=%s", toolLogRuneCount(pageURL), crawler, output.StatusCode, toolLogDurationMS(crawlerStarted), webErrorCode(crawlerCtx, crawlerErr))
		failures = append(failures, crawler)
		if crawlCtx.Err() != nil {
			break
		}
	}
	if cause := toolParentCause(ctx); cause != nil {
		return "", cause
	}
	log.Printf("[web_extract] run_failed url_chars=%d crawlers=%s duration_ms=%d error_type=%s failures=%s", toolLogRuneCount(pageURL), strings.Join(attempted, ","), toolLogDurationMS(started), webErrorCode(crawlCtx, crawlCtx.Err()), strings.Join(failures, ","))
	return marshalExtractOutput(WebExtractOutput{
		OK:               false,
		URL:              pageURL,
		AttemptedSources: attempted,
		StatusCode:       lastStatus,
		ErrorCode:        webErrorCode(crawlCtx, crawlCtx.Err()),
		Error:            webErrorMessage(crawlCtx, crawlCtx.Err()),
		Retryable:        true,
	})
}

// finalizeContent 把抓取到的原文收口为最终返回正文：
// 开启提炼且有提炼器时，用小模型按 goal 提炼成要点；提炼失败或关闭提炼时，
// 降级到按 maxContent 截断。两条路径都保证返回体量可控，不再单页塞满上下文。
//
// 只有 refinement 自己的 provider/transport/首包失败允许降级。父 context
// 表示用户停止、RunHub service drain 或运行失效，必须原样作为 Go error 向上传播，
// 不能伪装成 source 成功或 degraded Tool JSON。
func (t *WebExtractTool) finalizeContent(ctx context.Context, output WebExtractOutput, goal, detail string) (WebExtractOutput, error) {
	if cause := toolParentCause(ctx); cause != nil {
		return output, cause
	}
	output.Detail = detail
	limit := t.maxContent
	if detail == extractDetailDetailed {
		limit = detailedContentLimit
	}
	if detail == extractDetailSource {
		var truncated bool
		output.Content, truncated = truncateRunesWithStatus(output.Content, sourceContentLimit)
		output.Truncated = output.Truncated || truncated
		if cause := toolParentCause(ctx); cause != nil {
			return output, cause
		}
		return output, nil
	}
	if t.summaryEnabled && t.summarizer != nil {
		sourceTruncated := output.Truncated
		output.RefinementAttempted = true
		summarizeStarted := time.Now()
		log.Printf("[web_extract] summarize_start url_chars=%d source=%s detail=%s goal_chars=%d input_chars=%d", toolLogRuneCount(output.URL), output.Source, detail, toolLogRuneCount(goal), toolLogRuneCount(output.Content))
		summary, err := t.summarizer.Summarize(ctx, goal, output.Title, output.Content, detail)
		if cause := toolParentCause(ctx); cause != nil {
			log.Printf("[web_extract] summarize_canceled url_chars=%d source=%s duration_ms=%d error_type=canceled", toolLogRuneCount(output.URL), output.Source, toolLogDurationMS(summarizeStarted))
			return output, cause
		}
		if err == nil && strings.TrimSpace(summary) != "" {
			var summaryTruncated bool
			output.Content, summaryTruncated = truncateRunesWithStatus(strings.TrimSpace(summary), limit)
			output.Truncated = sourceTruncated || summaryTruncated
			output.Summarized = true
			if sourceTruncated {
				output.Degraded = true
				output.DegradationReason = RefinementSourceTruncated
			}
			log.Printf("[web_extract] summarize_success url_chars=%d source=%s output_chars=%d duration_ms=%d", toolLogRuneCount(output.URL), output.Source, toolLogRuneCount(output.Content), toolLogDurationMS(summarizeStarted))
			return output, nil
		}
		output.Degraded = true
		output.DegradationReason = refinementReason(err)
		errorType := modelusage.ErrorType(err)
		if errorType == "" {
			errorType = "empty_result"
		}
		log.Printf("[web_extract] summarize_failed url_chars=%d source=%s duration_ms=%d error_type=%s fallback=truncate", toolLogRuneCount(output.URL), output.Source, toolLogDurationMS(summarizeStarted), errorType)
	} else {
		output.Degraded = true
		if t.summaryEnabled {
			output.DegradationReason = RefinementUnavailable
		} else {
			output.DegradationReason = RefinementDisabled
		}
	}
	before := toolLogRuneCount(output.Content)
	var truncated bool
	output.Content, truncated = truncateRunesWithStatus(output.Content, limit)
	output.Truncated = output.Truncated || truncated
	if output.Truncated {
		log.Printf("[web_extract] content_truncated url_chars=%d source=%s before_chars=%d after_chars=%d", toolLogRuneCount(output.URL), output.Source, before, toolLogRuneCount(output.Content))
	}
	if cause := toolParentCause(ctx); cause != nil {
		return output, cause
	}
	return output, nil
}

func toolParentCause(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return context.Cause(ctx)
}

func refinementReason(err error) string {
	var refinementErr *RefinementError
	if errors.As(err, &refinementErr) {
		return normalizeRefinementReason(refinementErr.Reason)
	}
	return RefinementFailed
}

func normalizeRefinementReason(reason string) string {
	switch strings.TrimSpace(reason) {
	case RefinementDisabled, RefinementUnavailable, RefinementCooldown, RefinementFailed, RefinementSourceTruncated:
		return strings.TrimSpace(reason)
	default:
		return RefinementFailed
	}
}

func (t *WebExtractTool) extractWithCrawler(ctx context.Context, crawler, pageURL string) WebExtractOutput {
	switch crawler {
	case "firecrawl":
		return t.extractWithFirecrawl(ctx, pageURL)
	case "jina":
		return t.extractWithJina(ctx, pageURL)
	case "tavily":
		return t.extractWithTavily(ctx, pageURL)
	case "exa":
		return t.extractWithExa(ctx, pageURL)
	case "basic":
		return t.extractWithBasic(ctx, pageURL)
	default:
		return WebExtractOutput{
			OK:    false,
			URL:   pageURL,
			Error: fmt.Sprintf("unsupported crawler impl: %s", crawler),
		}
	}
}

func ProbeWebExtractService(ctx context.Context, cfg WebExtractConfig) error {
	t := NewWebExtractTool(cfg)
	for _, crawler := range t.crawlerProviders {
		if crawler == "basic" {
			continue
		}
		output := t.extractWithCrawler(ctx, crawler, "https://example.com")
		if output.OK && strings.TrimSpace(output.Content) != "" {
			return nil
		}
		return fmt.Errorf("%s: %s", crawler, output.failureSummary())
	}
	return fmt.Errorf("no configured extraction provider")
}

func (t *WebExtractTool) extractWithBasic(ctx context.Context, pageURL string) WebExtractOutput {
	// SSRF 前置校验：解析所有 IP 拒私网/环回/元数据；拨号时由 basicClient 复核防 rebind。
	if err := validatePublicURL(ctx, t.basicResolver, t.ipBlocked, pageURL); err != nil {
		log.Printf("[web_extract] blocked_by_url_policy url_chars=%d crawler=basic error_type=%s", toolLogRuneCount(pageURL), WebErrorCodeURLBlocked)
		return WebExtractOutput{OK: false, URL: pageURL, Error: "该地址被安全策略拦截"}
	}
	req, err := http.NewRequestWithContext(ctx, "GET", pageURL, nil)
	if err != nil {
		return WebExtractOutput{OK: false, URL: pageURL, Error: err.Error()}
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; EffChat/1.0)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.9,*/*;q=0.8")

	resp, err := t.basicClient.Do(req)
	if err != nil {
		return WebExtractOutput{
			OK:    false,
			URL:   pageURL,
			Error: err.Error(),
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return WebExtractOutput{
			OK:         false,
			URL:        pageURL,
			StatusCode: resp.StatusCode,
			Error:      fmt.Sprintf("page returned status %d", resp.StatusCode),
		}
	}

	limited := io.LimitReader(resp.Body, int64(t.rawContentLimit*8))
	body, err := io.ReadAll(limited)
	if err != nil {
		return WebExtractOutput{OK: false, URL: pageURL, Error: err.Error()}
	}

	html := string(body)
	content, truncated := extractReadableTextWithStatus(html, t.rawContentLimit)
	return WebExtractOutput{
		OK:        true,
		URL:       pageURL,
		Title:     extractTitle(html),
		Content:   content,
		Truncated: truncated,
	}
}

func (t *WebExtractTool) extractWithJina(ctx context.Context, pageURL string) WebExtractOutput {
	readerURL := t.jinaBaseURL + "/" + pageURL
	req, err := http.NewRequestWithContext(ctx, "GET", readerURL, nil)
	if err != nil {
		return WebExtractOutput{OK: false, URL: pageURL, Error: err.Error()}
	}
	req.Header.Set("Accept", "text/plain, text/markdown;q=0.9, */*;q=0.8")
	req.Header.Set("User-Agent", "EffChat/1.0")
	if t.jinaAPIKey != "" {
		req.Header.Set("Authorization", "Bearer "+t.jinaAPIKey)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return WebExtractOutput{
			OK:    false,
			URL:   pageURL,
			Error: err.Error(),
		}
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, int64(t.rawContentLimit*8)))
	if readErr != nil {
		return WebExtractOutput{OK: false, URL: pageURL, Error: readErr.Error()}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return WebExtractOutput{
			OK:         false,
			URL:        pageURL,
			StatusCode: resp.StatusCode,
			Error:      fmt.Sprintf("jina reader returned status %d", resp.StatusCode),
		}
	}

	content := normalizeSpace(string(body))
	content, truncated := truncateRunesWithStatus(content, t.rawContentLimit)
	return WebExtractOutput{
		OK:        true,
		URL:       pageURL,
		Title:     extractJinaTitle(content),
		Content:   content,
		Truncated: truncated,
	}
}

type firecrawlScrapeResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Data    struct {
		Markdown string `json:"markdown"`
		Metadata struct {
			Title string `json:"title"`
		} `json:"metadata"`
	} `json:"data"`
	Markdown string `json:"markdown"`
	Metadata struct {
		Title string `json:"title"`
	} `json:"metadata"`
}

func (t *WebExtractTool) extractWithFirecrawl(ctx context.Context, pageURL string) WebExtractOutput {
	payload := map[string]interface{}{
		"url":                pageURL,
		"formats":            []string{"markdown"},
		"onlyMainContent":    true,
		"removeBase64Images": true,
		"blockAds":           true,
		"timeout":            20000,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return WebExtractOutput{OK: false, URL: pageURL, Error: err.Error()}
	}

	req, err := http.NewRequestWithContext(ctx, "POST", t.firecrawlBaseURL+"/scrape", bytes.NewReader(body))
	if err != nil {
		return WebExtractOutput{OK: false, URL: pageURL, Error: err.Error()}
	}
	if t.firecrawlAPIKey != "" {
		req.Header.Set("Authorization", "Bearer "+t.firecrawlAPIKey)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "EffChat/1.0")

	resp, err := t.client.Do(req)
	if err != nil {
		return WebExtractOutput{OK: false, URL: pageURL, Error: err.Error()}
	}
	defer resp.Body.Close()

	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, int64(t.rawContentLimit*16)))
	if readErr != nil {
		return WebExtractOutput{OK: false, URL: pageURL, Error: readErr.Error()}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return WebExtractOutput{
			OK:         false,
			URL:        pageURL,
			StatusCode: resp.StatusCode,
			Error:      fmt.Sprintf("firecrawl returned status %d", resp.StatusCode),
		}
	}

	var firecrawlResp firecrawlScrapeResponse
	if err := json.Unmarshal(respBody, &firecrawlResp); err != nil {
		return WebExtractOutput{OK: false, URL: pageURL, Error: fmt.Sprintf("failed to decode firecrawl response: %v", err)}
	}
	if !firecrawlResp.Success && firecrawlResp.Error != "" {
		return WebExtractOutput{OK: false, URL: pageURL, Error: firecrawlResp.Error}
	}

	content := strings.TrimSpace(firecrawlResp.Data.Markdown)
	title := strings.TrimSpace(firecrawlResp.Data.Metadata.Title)
	if content == "" {
		content = strings.TrimSpace(firecrawlResp.Markdown)
		title = strings.TrimSpace(firecrawlResp.Metadata.Title)
	}
	content = normalizeSpace(content)
	if content == "" {
		return WebExtractOutput{OK: false, URL: pageURL, Error: "firecrawl returned empty content"}
	}

	content, truncated := truncateRunesWithStatus(content, t.rawContentLimit)
	return WebExtractOutput{
		OK:        true,
		URL:       pageURL,
		Title:     title,
		Content:   content,
		Truncated: truncated,
	}
}

func (t *WebExtractTool) extractWithTavily(ctx context.Context, pageURL string) WebExtractOutput {
	if t.tavilyAPIKey == "" {
		return WebExtractOutput{URL: pageURL, Error: "tavily API key is not configured"}
	}
	body, err := json.Marshal(map[string]interface{}{"urls": []string{pageURL}, "extract_depth": "basic", "format": "markdown"})
	if err != nil {
		return WebExtractOutput{URL: pageURL, Error: err.Error()}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.tavilyBaseURL, bytes.NewReader(body))
	if err != nil {
		return WebExtractOutput{URL: pageURL, Error: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.tavilyAPIKey)
	resp, err := t.client.Do(req)
	if err != nil {
		return WebExtractOutput{URL: pageURL, Error: err.Error()}
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, int64(t.rawContentLimit*16)))
	if err != nil {
		return WebExtractOutput{URL: pageURL, Error: err.Error()}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return WebExtractOutput{URL: pageURL, StatusCode: resp.StatusCode, Error: fmt.Sprintf("tavily returned status %d", resp.StatusCode)}
	}
	var payload struct {
		Results []struct {
			URL        string `json:"url"`
			RawContent string `json:"raw_content"`
			Content    string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(responseBody, &payload); err != nil {
		return WebExtractOutput{URL: pageURL, Error: fmt.Sprintf("decode tavily response: %v", err)}
	}
	if len(payload.Results) == 0 {
		return WebExtractOutput{URL: pageURL, Error: "tavily returned empty content"}
	}
	content := strings.TrimSpace(payload.Results[0].RawContent)
	if content == "" {
		content = strings.TrimSpace(payload.Results[0].Content)
	}
	if content == "" {
		return WebExtractOutput{URL: pageURL, Error: "tavily returned empty content"}
	}
	content, truncated := truncateRunesWithStatus(normalizeSpace(content), t.rawContentLimit)
	return WebExtractOutput{OK: true, URL: pageURL, Content: content, Truncated: truncated}
}

func (t *WebExtractTool) extractWithExa(ctx context.Context, pageURL string) WebExtractOutput {
	if t.exaAPIKey == "" {
		return WebExtractOutput{URL: pageURL, Error: "exa API key is not configured"}
	}
	body, err := json.Marshal(map[string]interface{}{"urls": []string{pageURL}, "text": map[string]interface{}{"maxCharacters": t.rawContentLimit}})
	if err != nil {
		return WebExtractOutput{URL: pageURL, Error: err.Error()}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.exaBaseURL, bytes.NewReader(body))
	if err != nil {
		return WebExtractOutput{URL: pageURL, Error: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-api-key", t.exaAPIKey)
	resp, err := t.client.Do(req)
	if err != nil {
		return WebExtractOutput{URL: pageURL, Error: err.Error()}
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, int64(t.rawContentLimit*16)))
	if err != nil {
		return WebExtractOutput{URL: pageURL, Error: err.Error()}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return WebExtractOutput{URL: pageURL, StatusCode: resp.StatusCode, Error: fmt.Sprintf("exa returned status %d", resp.StatusCode)}
	}
	var payload struct {
		Results []struct {
			Title string `json:"title"`
			Text  string `json:"text"`
		} `json:"results"`
		Content struct {
			Title string `json:"title"`
			Text  string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(responseBody, &payload); err != nil {
		return WebExtractOutput{URL: pageURL, Error: fmt.Sprintf("decode exa response: %v", err)}
	}
	if len(payload.Results) > 0 && strings.TrimSpace(payload.Results[0].Text) != "" {
		content, truncated := truncateRunesWithStatus(normalizeSpace(payload.Results[0].Text), t.rawContentLimit)
		return WebExtractOutput{OK: true, URL: pageURL, Title: strings.TrimSpace(payload.Results[0].Title), Content: content, Truncated: truncated}
	}
	if strings.TrimSpace(payload.Content.Text) == "" {
		return WebExtractOutput{URL: pageURL, Error: "exa returned empty content"}
	}
	content, truncated := truncateRunesWithStatus(normalizeSpace(payload.Content.Text), t.rawContentLimit)
	return WebExtractOutput{OK: true, URL: pageURL, Title: strings.TrimSpace(payload.Content.Title), Content: content, Truncated: truncated}
}

func extractTitle(html string) string {
	re := regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	match := re.FindStringSubmatch(html)
	if len(match) < 2 {
		return ""
	}
	return normalizeSpace(stripTags(match[1]))
}

func extractReadableText(html string, limit int) string {
	text, _ := extractReadableTextWithStatus(html, limit)
	return text
}

func extractReadableTextWithStatus(html string, limit int) (string, bool) {
	text := regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`).ReplaceAllString(html, " ")
	text = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`).ReplaceAllString(text, " ")
	text = regexp.MustCompile(`(?is)<noscript[^>]*>.*?</noscript>`).ReplaceAllString(text, " ")
	text = regexp.MustCompile(`(?is)<(br|p|div|section|article|li|h[1-6])[^>]*>`).ReplaceAllString(text, "\n")
	text = stripTags(text)
	text = htmlEntityFix(text)
	text = normalizeSpace(text)
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit]), true
	}
	return text, false
}

func stripTags(input string) string {
	return regexp.MustCompile(`(?is)<[^>]+>`).ReplaceAllString(input, " ")
}

func htmlEntityFix(input string) string {
	return html.UnescapeString(input)
}

func normalizeSpace(input string) string {
	lines := strings.Split(input, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

func extractJinaTitle(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Title:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Title:"))
		}
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

func truncateRunes(text string, limit int) string {
	truncated, _ := truncateRunesWithStatus(text, limit)
	return truncated
}

func truncateRunesWithStatus(text string, limit int) (string, bool) {
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit]), true
	}
	return text, false
}

func normalizeExtractDetail(detail string) (string, bool) {
	switch normalized := strings.ToLower(strings.TrimSpace(detail)); normalized {
	case "", extractDetailSummary:
		return extractDetailSummary, true
	case extractDetailDetailed, extractDetailSource:
		return normalized, true
	default:
		return "", false
	}
}

func marshalExtractOutput(output WebExtractOutput) (string, error) {
	raw, err := json.Marshal(output)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func normalizeCrawlerProviders(providers []string, fallback string) []string {
	if len(providers) == 0 {
		providers = []string{fallback}
	}
	seen := make(map[string]bool, len(providers))
	normalized := make([]string, 0, len(providers))
	for _, item := range providers {
		item = strings.ToLower(strings.TrimSpace(item))
		if item == "" || item == "basic" || seen[item] {
			continue
		}
		seen[item] = true
		normalized = append(normalized, item)
	}
	return append(normalized, "basic")
}

func (o WebExtractOutput) failureSummary() string {
	if o.Error != "" {
		return o.Error
	}
	if o.StatusCode != 0 {
		return fmt.Sprintf("status %d", o.StatusCode)
	}
	if o.OK {
		return "empty content"
	}
	return "unknown error"
}

var _ tool.InvokableTool = (*WebExtractTool)(nil)
