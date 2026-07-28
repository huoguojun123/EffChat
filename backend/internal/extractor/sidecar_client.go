package extractor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// SidecarClient 调用 Python 文档解析服务。
//
// Go 后端仍然负责鉴权、上传大小、白名单、落盘和 DB 元数据；Python sidecar 只做
// “把复杂文档转成可读 Markdown/text”这一件事。这样可以把 pdfplumber、python-docx
// 等相对重的依赖隔离到独立容器，主服务镜像仍保持小而稳定。
type SidecarClient struct {
	baseURL string
	client  *http.Client
}

type sidecarResponse struct {
	Text           string   `json:"text"`
	Parser         string   `json:"parser"`
	TokenEstimate  int      `json:"token_estimate"`
	SniffedMIME    string   `json:"sniffed_mime"`
	PageCount      int      `json:"page_count"`
	ParagraphCount int      `json:"paragraph_count"`
	TableCount     int      `json:"table_count"`
	Warnings       []string `json:"warnings"`
}

type MinerUStartResult struct {
	TaskID    string
	State     string
	PageCount int
}

type OCRPDFInfo struct {
	PageCount int
	MaxPages  int
	MaxBytes  int64
}

type MinerUPollResult struct {
	State         string
	Markdown      string
	TokenEstimate int
	Error         string
	ErrorType     string
}

type minerUStartResponse struct {
	TaskID    string `json:"task_id"`
	State     string `json:"state"`
	PageCount int    `json:"page_count"`
}

type ocrPDFInfoResponse struct {
	PageCount int   `json:"page_count"`
	MaxPages  int   `json:"max_pages"`
	MaxBytes  int64 `json:"max_bytes"`
}

type minerUPollResponse struct {
	State         string `json:"state"`
	Markdown      string `json:"markdown"`
	TokenEstimate int    `json:"token_estimate"`
	Error         string `json:"error"`
	ErrorType     string `json:"error_type"`
	MinerUState   string `json:"mineru_state"`
}

func NewSidecarClient(baseURL string, timeout time.Duration) *SidecarClient {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if timeout < 5*time.Minute {
		timeout = 5 * time.Minute
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Python extractor 是 docker-compose 内网 sidecar，只应该走 Docker DNS 直连。
	//
	// Go 默认 HTTP client 会读取 HTTP_PROXY/HTTPS_PROXY 环境变量。OrbStack、Docker
	// Desktop 或用户 shell 可能把这些变量注入容器；如果 NO_PROXY 漏掉 py-extractor，
	// 请求会被转发到外部代理，代理无法解析 docker service name，最终表现为 EOF，且
	// py-extractor 容器日志里完全看不到 /extract 请求。这里在专用 client 层禁用代理，
	// 让内部服务调用不依赖部署环境变量是否写对。
	transport.Proxy = nil
	return &SidecarClient{
		baseURL: baseURL,
		client: &http.Client{
			Timeout:   timeout,
			Transport: transport,
		},
	}
}

func (c *SidecarClient) Enabled() bool {
	return c != nil && strings.TrimSpace(c.baseURL) != ""
}

func (c *SidecarClient) Health(ctx context.Context) error {
	if !c.Enabled() {
		return fmt.Errorf("python extractor is not configured")
	}
	endpoint, err := url.JoinPath(c.baseURL, "health")
	if err != nil {
		endpoint = strings.TrimRight(c.baseURL, "/") + "/health"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create extractor health request: %w", err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("python extractor health request failed: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("python extractor health returned %d", resp.StatusCode)
	}
	return nil
}

func (c *SidecarClient) Extract(ctx context.Context, content []byte, contentType, filename string) (*Result, error) {
	if !c.Enabled() {
		return nil, fmt.Errorf("python extractor is not configured")
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return nil, fmt.Errorf("create multipart file: %w", err)
	}
	if _, err := part.Write(content); err != nil {
		return nil, fmt.Errorf("write multipart file: %w", err)
	}
	if err := writer.WriteField("filename", filename); err != nil {
		return nil, fmt.Errorf("write filename field: %w", err)
	}
	if err := writer.WriteField("content_type", contentType); err != nil {
		return nil, fmt.Errorf("write content_type field: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close multipart body: %w", err)
	}

	endpoint, err := url.JoinPath(c.baseURL, "extract")
	if err != nil {
		endpoint = strings.TrimRight(c.baseURL, "/") + "/" + path.Clean("extract")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return nil, fmt.Errorf("create extractor request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("python extractor request failed: %w", err)
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, int64(len(content))+8<<20)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read extractor response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("python extractor returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	var parsed sidecarResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("decode extractor response: %w", err)
	}
	if strings.TrimSpace(parsed.Text) == "" {
		return nil, fmt.Errorf("python extractor returned empty text")
	}
	return &Result{
		Text:           parsed.Text,
		TokenEstimate:  parsed.TokenEstimate,
		SniffedMIME:    parsed.SniffedMIME,
		Parser:         parsed.Parser,
		PageCount:      parsed.PageCount,
		ParagraphCount: parsed.ParagraphCount,
		TableCount:     parsed.TableCount,
		Warnings:       parsed.Warnings,
	}, nil
}

func (c *SidecarClient) StartMinerUOCR(ctx context.Context, content []byte, filename, baseURL, apiKey string) (*MinerUStartResult, error) {
	if !c.Enabled() {
		return nil, fmt.Errorf("python extractor is not configured")
	}
	fields := map[string]string{
		"filename": filename,
		"base_url": baseURL,
		"api_key":  apiKey,
	}
	data, err := c.postMultipart(ctx, "ocr/mineru/start", content, filename, fields)
	if err != nil {
		return nil, err
	}
	var parsed minerUStartResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("decode mineru start response: %w", err)
	}
	if strings.TrimSpace(parsed.TaskID) == "" {
		return nil, fmt.Errorf("mineru start returned no task id")
	}
	return &MinerUStartResult{TaskID: parsed.TaskID, State: parsed.State, PageCount: parsed.PageCount}, nil
}

func (c *SidecarClient) InspectOCRPDF(ctx context.Context, content []byte, filename string) (*OCRPDFInfo, error) {
	if !c.Enabled() {
		return nil, fmt.Errorf("python extractor is not configured")
	}
	data, err := c.postMultipart(ctx, "ocr/pdf-info", content, filename, map[string]string{"filename": filename})
	if err != nil {
		return nil, err
	}
	var parsed ocrPDFInfoResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("decode OCR PDF info response: %w", err)
	}
	return &OCRPDFInfo{PageCount: parsed.PageCount, MaxPages: parsed.MaxPages, MaxBytes: parsed.MaxBytes}, nil
}

func (c *SidecarClient) PollMinerUOCR(ctx context.Context, taskID, baseURL, apiKey string) (*MinerUPollResult, error) {
	if !c.Enabled() {
		return nil, fmt.Errorf("python extractor is not configured")
	}
	endpoint, err := url.JoinPath(c.baseURL, "ocr/mineru/tasks", taskID)
	if err != nil {
		endpoint = strings.TrimRight(c.baseURL, "/") + "/" + path.Clean("ocr/mineru/tasks/"+taskID)
	}
	q := url.Values{}
	if strings.TrimSpace(baseURL) != "" {
		q.Set("base_url", baseURL)
	}
	if encoded := q.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create mineru poll request: %w", err)
	}
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("X-MinerU-Token", strings.TrimSpace(apiKey))
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("python extractor mineru poll failed: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read mineru poll response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("python extractor mineru poll returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var parsed minerUPollResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("decode mineru poll response: %w", err)
	}
	if parsed.State == "" {
		parsed.State = parsed.MinerUState
	}
	return &MinerUPollResult{
		State:         parsed.State,
		Markdown:      parsed.Markdown,
		TokenEstimate: parsed.TokenEstimate,
		Error:         parsed.Error,
		ErrorType:     parsed.ErrorType,
	}, nil
}

func (c *SidecarClient) postMultipart(ctx context.Context, endpointPath string, content []byte, filename string, fields map[string]string) ([]byte, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return nil, fmt.Errorf("create multipart file: %w", err)
	}
	if _, err := part.Write(content); err != nil {
		return nil, fmt.Errorf("write multipart file: %w", err)
	}
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return nil, fmt.Errorf("write %s field: %w", key, err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close multipart body: %w", err)
	}

	endpoint, err := url.JoinPath(c.baseURL, endpointPath)
	if err != nil {
		endpoint = strings.TrimRight(c.baseURL, "/") + "/" + path.Clean(endpointPath)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return nil, fmt.Errorf("create extractor request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("python extractor request failed: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(len(content))+8<<20))
	if err != nil {
		return nil, fmt.Errorf("read extractor response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("python extractor returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}
