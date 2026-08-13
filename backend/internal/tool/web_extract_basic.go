package tool

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	readability "codeberg.org/readeck/go-readability/v2"
	"golang.org/x/net/html/charset"
)

const (
	basicWireLimitBytes     = 2 << 20
	basicDecodedLimitBytes  = 2 << 20
	basicParsedContentLimit = 512_000
)

var (
	errUnsupportedBasicContent  = errors.New("unsupported basic content type")
	errUnsupportedBasicEncoding = errors.New("unsupported basic content encoding")
	errBasicWireLimit           = errors.New("basic compressed response exceeds limit")
	errBasicHTMLParse           = errors.New("basic html parse failed")
)

// extractWithBasic owns the local fetch boundary. URL validation and the guarded
// transport stay in Go so redirects and dial-time re-resolution cannot bypass SSRF;
// the readability parser only receives already-bounded response bytes.
func (t *WebExtractTool) extractWithBasic(ctx context.Context, pageURL string) WebExtractOutput {
	started := time.Now()
	if err := validatePublicURL(ctx, t.basicResolver, t.ipBlocked, pageURL); err != nil {
		log.Printf("[web_extract] blocked_by_url_policy url_chars=%d crawler=basic error_type=%s", toolLogRuneCount(pageURL), WebErrorCodeURLBlocked)
		return WebExtractOutput{OK: false, URL: pageURL, Error: "该地址被安全策略拦截"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return WebExtractOutput{OK: false, URL: pageURL, Error: err.Error()}
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; EffChat/1.0)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.9,*/*;q=0.1")
	req.Header.Set("Accept-Encoding", "gzip")

	resp, err := t.basicClient.Do(req)
	if err != nil {
		logBasicOutcome("fetch_error", pageURL, 0, "", 0, 0, false, started)
		return WebExtractOutput{OK: false, URL: pageURL, Error: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		logBasicOutcome("http_status", pageURL, resp.StatusCode, "", 0, 0, false, started)
		return WebExtractOutput{
			OK:         false,
			URL:        pageURL,
			StatusCode: resp.StatusCode,
			Error:      fmt.Sprintf("page returned status %d", resp.StatusCode),
		}
	}

	body, fetchTruncated, err := readBasicResponse(resp, basicWireLimitBytes, basicDecodedLimitBytes)
	if err != nil {
		outcome := "fetch_error"
		if errors.Is(err, errUnsupportedBasicEncoding) || errors.Is(err, gzip.ErrHeader) || errors.Is(err, io.ErrUnexpectedEOF) {
			outcome = "decode_error"
		}
		logBasicOutcome(outcome, pageURL, resp.StatusCode, "", 0, 0, false, started)
		return WebExtractOutput{OK: false, URL: pageURL, Error: err.Error()}
	}
	finalURL := pageURL
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	mediaType := basicMediaType(resp.Header.Get("Content-Type"), body)
	if (mediaType == "text/html" || mediaType == "application/xhtml+xml") && isBasicChallengePage(finalURL, body) {
		logBasicOutcome("challenge", pageURL, resp.StatusCode, "", len(body), 0, fetchTruncated, started)
		return WebExtractOutput{OK: false, URL: pageURL, StatusCode: resp.StatusCode, Error: "challenge page detected"}
	}
	title, content, parsedTruncated, parser, err := extractBasicDocument(finalURL, resp.Header.Get("Content-Type"), body, basicParsedContentLimit)
	if err != nil {
		switch {
		case errors.Is(err, errBasicHTMLParse):
			// Readability targets article-shaped documents. Keep the existing local
			// stripper as a last-resort Basic parser for short or malformed HTML;
			// binary response types never enter this fallback.
			htmlText := string(body)
			content, parsedTruncated = extractReadableTextWithStatus(htmlText, basicParsedContentLimit)
			title = extractTitle(htmlText)
			parser = "basic-strip"
		case errors.Is(err, errUnsupportedBasicContent):
			logBasicOutcome("unsupported_content", pageURL, resp.StatusCode, "", len(body), 0, fetchTruncated, started)
			return WebExtractOutput{OK: false, URL: pageURL, StatusCode: resp.StatusCode, Error: err.Error()}
		default:
			logBasicOutcome("decode_error", pageURL, resp.StatusCode, "", len(body), 0, fetchTruncated, started)
			return WebExtractOutput{OK: false, URL: pageURL, StatusCode: resp.StatusCode, Error: err.Error()}
		}
	}
	content = strings.TrimSpace(content)
	if content == "" {
		outcome := "empty"
		if errors.Is(err, errBasicHTMLParse) {
			outcome = "parse_error"
		}
		logBasicOutcome(outcome, pageURL, resp.StatusCode, parser, len(body), 0, fetchTruncated || parsedTruncated, started)
		return WebExtractOutput{OK: false, URL: pageURL, StatusCode: resp.StatusCode, Error: "no readable text extracted"}
	}
	logBasicOutcome("success", pageURL, resp.StatusCode, parser, len(body), toolLogRuneCount(content), fetchTruncated || parsedTruncated, started)
	return WebExtractOutput{
		OK:         true,
		URL:        pageURL,
		Title:      title,
		Content:    content,
		Truncated:  fetchTruncated || parsedTruncated,
		StatusCode: resp.StatusCode,
	}
}

func readBasicResponse(resp *http.Response, wireLimit, decodedLimit int64) ([]byte, bool, error) {
	if resp == nil || resp.Body == nil {
		return nil, false, fmt.Errorf("empty basic response")
	}
	if wireLimit <= 0 {
		wireLimit = basicWireLimitBytes
	}
	if decodedLimit <= 0 {
		decodedLimit = basicDecodedLimitBytes
	}
	wire := &io.LimitedReader{R: resp.Body, N: wireLimit + 1}
	var decoded io.Reader = wire
	encoding := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))
	switch encoding {
	case "", "identity":
	case "gzip":
		reader, err := gzip.NewReader(wire)
		if err != nil {
			if wire.N == 0 {
				return nil, false, errBasicWireLimit
			}
			return nil, false, err
		}
		defer reader.Close()
		decoded = reader
	default:
		return nil, false, fmt.Errorf("%w: %s", errUnsupportedBasicEncoding, encoding)
	}
	body, err := io.ReadAll(io.LimitReader(decoded, decodedLimit+1))
	if err != nil {
		if encoding == "gzip" && wire.N == 0 {
			return nil, false, errBasicWireLimit
		}
		return nil, false, err
	}
	if encoding == "gzip" && wire.N == 0 {
		return nil, false, errBasicWireLimit
	}
	if int64(len(body)) <= decodedLimit {
		return body, false, nil
	}
	return body[:decodedLimit], true, nil
}

func isBasicChallengePage(pageURL string, body []byte) bool {
	if parsed, err := url.Parse(pageURL); err == nil && strings.Contains(strings.ToLower(parsed.EscapedPath()), "/cdn-cgi/challenge-platform/") {
		return true
	}
	sample := body
	if len(sample) > 256<<10 {
		sample = sample[:256<<10]
	}
	lower := strings.ToLower(string(sample))
	cloudflare := strings.Contains(lower, "just a moment") && (strings.Contains(lower, "__cf_chl") || strings.Contains(lower, "cf-chl-"))
	datadome := strings.Contains(lower, "datadome") && (strings.Contains(lower, "captcha-delivery") || strings.Contains(lower, "verify you are human") || strings.Contains(lower, "human verification"))
	perimeterX := strings.Contains(lower, "perimeterx") && (strings.Contains(lower, "verify you are human") || strings.Contains(lower, "human verification") || strings.Contains(lower, "captcha"))
	return cloudflare || datadome || perimeterX
}

func logBasicOutcome(outcome, pageURL string, status int, parser string, bodyBytes, contentChars int, truncated bool, started time.Time) {
	log.Printf(
		"[web_extract] basic_outcome outcome=%s url_chars=%d status=%d parser=%s body_bytes=%d content_chars=%d truncated=%t duration_ms=%d",
		outcome, toolLogRuneCount(pageURL), status, parser, bodyBytes, contentChars, truncated, toolLogDurationMS(started),
	)
}

func extractBasicDocument(pageURL, contentType string, body []byte, limit int) (title, content string, truncated bool, parser string, err error) {
	mediaType := basicMediaType(contentType, body)
	switch mediaType {
	case "text/plain":
		reader, decodeErr := charset.NewReader(bytes.NewReader(body), contentType)
		if decodeErr != nil {
			return "", "", false, "", decodeErr
		}
		decoded, decodeErr := io.ReadAll(reader)
		if decodeErr != nil {
			return "", "", false, "", decodeErr
		}
		content = normalizeBasicText(string(decoded))
		content, truncated = truncateRunesWithStatus(content, limit)
		return "", content, truncated, "plain-text", nil
	case "text/html", "application/xhtml+xml":
		parsedURL, parseErr := url.Parse(pageURL)
		if parseErr != nil {
			return "", "", false, "", parseErr
		}
		article, parseErr := readability.FromReader(bytes.NewReader(body), parsedURL)
		if parseErr != nil {
			return "", "", false, "", fmt.Errorf("%w: %v", errBasicHTMLParse, parseErr)
		}
		var rendered strings.Builder
		if renderErr := article.RenderText(&rendered); renderErr != nil {
			return "", "", false, "", fmt.Errorf("%w: %v", errBasicHTMLParse, renderErr)
		}
		content = normalizeBasicText(rendered.String())
		if content == "" {
			return "", "", false, "", fmt.Errorf("%w: readability returned empty content", errBasicHTMLParse)
		}
		content, truncated = truncateRunesWithStatus(content, limit)
		return strings.TrimSpace(article.Title()), content, truncated, "go-readability", nil
	default:
		return "", "", false, "", errUnsupportedBasicContent
	}
}

func basicMediaType(declared string, body []byte) string {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(declared))
	if err == nil && mediaType != "" && mediaType != "application/octet-stream" {
		return strings.ToLower(mediaType)
	}
	detected, _, err := mime.ParseMediaType(http.DetectContentType(body))
	if err != nil {
		return ""
	}
	return strings.ToLower(detected)
}

// normalizeBasicText preserves the meaningful line structure emitted by
// readability but drops blank-only separator lines. Tool results are plain text,
// so repeated visual paragraph spacing only consumes context without adding
// information; code and table rows remain separate non-empty lines.
func normalizeBasicText(input string) string {
	input = strings.ReplaceAll(input, "\r\n", "\n")
	input = strings.ReplaceAll(input, "\r", "\n")
	lines := strings.Split(input, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}
