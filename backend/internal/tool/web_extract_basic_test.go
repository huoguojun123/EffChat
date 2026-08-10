package tool

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExtractBasicDocumentKeepsArticleStructureAndDropsPageChrome(t *testing.T) {
	html := `<!doctype html><html><head><title>Fixture article</title></head><body>
<header><nav>Home Pricing Sign in</nav></header><aside>Advertisement</aside>
<main><article><h1>Reliable extraction</h1><p>Primary paragraph.</p>
<pre><code>func main() { println("ok") }</code></pre>
<table><tr><th>Mode</th><th>Result</th></tr><tr><td>Basic</td><td>Ready</td></tr></table>
</article></main><footer>Privacy Cookies</footer></body></html>`
	title, content, truncated, parser, err := extractBasicDocument("https://fixture.example/article", "text/html; charset=utf-8", []byte(html), 4000)
	if err != nil {
		t.Fatalf("extractBasicDocument() error = %v", err)
	}
	if title != "Fixture article" || parser != "go-readability" || truncated {
		t.Fatalf("title=%q parser=%q truncated=%t", title, parser, truncated)
	}
	for _, want := range []string{"Reliable extraction", "Primary paragraph", "func main", "Mode", "Basic", "Ready"} {
		if !strings.Contains(content, want) {
			t.Fatalf("content missing %q:\n%s", want, content)
		}
	}
	for _, noise := range []string{"Home Pricing", "Advertisement", "Privacy Cookies"} {
		if strings.Contains(content, noise) {
			t.Fatalf("content retained page chrome %q:\n%s", noise, content)
		}
	}
}

func TestExtractBasicDocumentDecodesPlainTextAndRejectsBinary(t *testing.T) {
	_, content, truncated, parser, err := extractBasicDocument(
		"https://fixture.example/readme.txt",
		"text/plain; charset=iso-8859-1",
		[]byte{'c', 'a', 'f', 0xe9},
		100,
	)
	if err != nil || content != "café" || truncated || parser != "plain-text" {
		t.Fatalf("content=%q truncated=%t parser=%q err=%v", content, truncated, parser, err)
	}
	_, content, truncated, parser, err = extractBasicDocument(
		"https://fixture.example/readme.txt",
		"text/plain; charset=gbk",
		[]byte{0xd6, 0xd0, 0xce, 0xc4},
		100,
	)
	if err != nil || content != "中文" || truncated || parser != "plain-text" {
		t.Fatalf("gbk content=%q truncated=%t parser=%q err=%v", content, truncated, parser, err)
	}
	if _, _, _, _, err := extractBasicDocument(
		"https://fixture.example/file.pdf",
		"application/pdf",
		[]byte("%PDF-1.7"),
		100,
	); !errors.Is(err, errUnsupportedBasicContent) {
		t.Fatalf("binary error = %v, want unsupported", err)
	}
}

func TestReadBasicResponseEnforcesIdentityAndDecodedLimits(t *testing.T) {
	resp := &http.Response{Header: make(http.Header), Body: io.NopCloser(strings.NewReader("123456"))}
	body, truncated, err := readBasicResponse(resp, 5, 5)
	if err != nil || string(body) != "12345" || !truncated {
		t.Fatalf("body=%q truncated=%t err=%v", body, truncated, err)
	}
}

func TestReadBasicResponseDecodesGzipAndRejectsUnsupportedEncoding(t *testing.T) {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, _ = writer.Write([]byte("decoded body"))
	_ = writer.Close()
	resp := &http.Response{Header: http.Header{"Content-Encoding": []string{"gzip"}}, Body: io.NopCloser(bytes.NewReader(compressed.Bytes()))}
	body, truncated, err := readBasicResponse(resp, 1024, 1024)
	if err != nil || truncated || string(body) != "decoded body" {
		t.Fatalf("body=%q truncated=%t err=%v", body, truncated, err)
	}

	resp = &http.Response{Header: http.Header{"Content-Encoding": []string{"br"}}, Body: io.NopCloser(strings.NewReader("encoded"))}
	if _, _, err := readBasicResponse(resp, 1024, 1024); !errors.Is(err, errUnsupportedBasicEncoding) {
		t.Fatalf("unsupported encoding error = %v", err)
	}
}

func TestReadBasicResponseBoundsDecodedAndCompressedStreams(t *testing.T) {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, _ = writer.Write([]byte(strings.Repeat("a", 256)))
	_ = writer.Close()
	resp := &http.Response{Header: http.Header{"Content-Encoding": []string{"gzip"}}, Body: io.NopCloser(bytes.NewReader(compressed.Bytes()))}
	body, truncated, err := readBasicResponse(resp, 1024, 32)
	if err != nil || !truncated || len(body) != 32 {
		t.Fatalf("decoded limit body=%d truncated=%t err=%v", len(body), truncated, err)
	}

	resp = &http.Response{Header: http.Header{"Content-Encoding": []string{"gzip"}}, Body: io.NopCloser(bytes.NewReader(compressed.Bytes()))}
	if _, _, err := readBasicResponse(resp, 8, 1024); err == nil {
		t.Fatal("compressed stream exceeding its wire limit must fail")
	}
}

func TestBasicChallengeDetectionRequiresStrongSignals(t *testing.T) {
	if !isBasicChallengePage("https://fixture.example/cdn-cgi/challenge-platform/h/g", []byte("ordinary")) {
		t.Fatal("explicit Cloudflare challenge path was not detected")
	}
	if !isBasicChallengePage("https://fixture.example/article", []byte(`<title>Just a moment</title><script>window.__cf_chl_opt={}</script>`)) {
		t.Fatal("strong Cloudflare marker pair was not detected")
	}
	if isBasicChallengePage("https://fixture.example/article", []byte(`<article>Captcha accessibility guidance</article><script src="hcaptcha.js"></script>`)) {
		t.Fatal("a normal article with one captcha marker must not be classified as a challenge")
	}
}

func TestWebExtractToolChallengePageDoesNotReachRefinement(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><title>Just a moment</title><script>window.__cf_chl_opt={}</script></html>`))
	}))
	defer server.Close()
	mock := &mockSummarizer{summary: "must not run"}
	tool := allowLoopback(NewWebExtractTool(WebExtractConfig{Summarizer: mock, SummaryEnabled: true}))
	output := runExtract(t, tool, WebExtractInput{URL: server.URL})
	if output.OK || mock.called || strings.Join(output.AttemptedSources, ",") != "basic" {
		t.Fatalf("output=%#v summarizer_called=%t", output, mock.called)
	}
}

func TestWebExtractToolExternalProviderSuccessSkipsBasic(t *testing.T) {
	pageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.7 fixture"))
	}))
	defer pageServer.Close()
	jinaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("Title: Fallback\nMarkdown Content:\nusable fallback body"))
	}))
	defer jinaServer.Close()

	tool := allowLoopback(NewWebExtractTool(WebExtractConfig{
		CrawlerProviders: []string{"basic", "jina", "basic"},
		JinaBaseURL:      jinaServer.URL,
	}))
	output := runExtract(t, tool, WebExtractInput{URL: pageServer.URL})
	if !output.OK || output.Source != "jina" || !strings.Contains(output.Content, "usable fallback body") {
		t.Fatalf("output = %#v", output)
	}
	if strings.Join(output.AttemptedSources, ",") != "jina" {
		t.Fatalf("attempted_sources = %#v", output.AttemptedSources)
	}
}
