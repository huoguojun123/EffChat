package main

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestLongSSESurvivesWhileSlowHeadersAreBounded(t *testing.T) {
	if os.Getenv("EFFCHAT_LONG_STREAM_TEST") != "1" {
		t.Skip("set EFFCHAT_LONG_STREAM_TEST=1 to run the 15-minute SSE acceptance test")
	}
	duration := 15*time.Minute + 5*time.Second
	if raw := os.Getenv("EFFCHAT_LONG_SSE_DURATION"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 {
			t.Fatalf("invalid EFFCHAT_LONG_SSE_DURATION %q", raw)
		}
		duration = parsed
	}
	heartbeatEvery := 5 * time.Second
	if duration < heartbeatEvery {
		heartbeatEvery = duration / 3
		if heartbeatEvery < 10*time.Millisecond {
			heartbeatEvery = 10 * time.Millisecond
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/events", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unavailable", http.StatusInternalServerError)
			return
		}
		_, _ = fmt.Fprint(w, "event: start\ndata: {}\n\n")
		flusher.Flush()
		timer := time.NewTimer(duration)
		defer timer.Stop()
		ticker := time.NewTicker(heartbeatEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_, _ = fmt.Fprint(w, ": heartbeat\n\n")
				flusher.Flush()
			case <-timer.C:
				_, _ = fmt.Fprint(w, "event: complete\ndata: {}\n\n")
				flusher.Flush()
				return
			}
		}
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := newHTTPServer(listener.Addr().String(), mux)
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Close()
		if serveErr := <-serveDone; serveErr != nil && serveErr != http.ErrServerClosed {
			t.Errorf("serve: %v", serveErr)
		}
	})

	var slowHeaderDone <-chan time.Duration
	if duration > serverReadHeaderTimeout+2*time.Second {
		done := make(chan time.Duration, 1)
		slowHeaderDone = done
		go func() {
			started := time.Now()
			conn, dialErr := net.Dial("tcp", listener.Addr().String())
			if dialErr != nil {
				done <- 0
				return
			}
			defer conn.Close()
			_, _ = fmt.Fprintf(conn, "GET /health HTTP/1.1\r\nHost: %s\r\n", listener.Addr().String())
			_ = conn.SetReadDeadline(time.Now().Add(2 * serverReadHeaderTimeout))
			buffer := make([]byte, 1)
			_, _ = conn.Read(buffer)
			done <- time.Since(started)
		}()
	}

	started := time.Now()
	response, err := http.Get("http://" + listener.Addr().String() + "/events")
	if err != nil {
		t.Fatalf("open SSE: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("SSE status = %d", response.StatusCode)
	}
	scanner := bufio.NewScanner(response.Body)
	completed := false
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "event: complete" {
			completed = true
			break
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read SSE: %v", err)
	}
	if !completed {
		t.Fatal("SSE closed before the terminal event")
	}
	if elapsed := time.Since(started); elapsed+25*time.Millisecond < duration {
		t.Fatalf("SSE completed after %s, want at least %s", elapsed, duration)
	}

	if slowHeaderDone != nil {
		elapsed := <-slowHeaderDone
		if elapsed < serverReadHeaderTimeout-2*time.Second || elapsed > 2*serverReadHeaderTimeout {
			t.Fatalf("slow header connection bounded after %s, want near %s", elapsed, serverReadHeaderTimeout)
		}
	}
}

func TestNewHTTPServerKeepsLongSSEWritesUnbounded(t *testing.T) {
	server := newHTTPServer(":8080", http.NewServeMux())
	if serverReadHeaderTimeout != 10*time.Second {
		t.Fatalf("serverReadHeaderTimeout = %s", serverReadHeaderTimeout)
	}
	if server.ReadHeaderTimeout != serverReadHeaderTimeout {
		t.Fatalf("ReadHeaderTimeout = %s", server.ReadHeaderTimeout)
	}
	if serverIdleTimeout != 75*time.Second {
		t.Fatalf("serverIdleTimeout = %s", serverIdleTimeout)
	}
	if server.IdleTimeout != serverIdleTimeout {
		t.Fatalf("IdleTimeout = %s", server.IdleTimeout)
	}
	if serverMaxHeaderBytes != 1<<20 {
		t.Fatalf("serverMaxHeaderBytes = %d", serverMaxHeaderBytes)
	}
	if server.MaxHeaderBytes != serverMaxHeaderBytes {
		t.Fatalf("MaxHeaderBytes = %d", server.MaxHeaderBytes)
	}
	if server.WriteTimeout != 0 {
		t.Fatalf("WriteTimeout = %s, want 0 for long-lived SSE", server.WriteTimeout)
	}
}

func TestIsAllowedOrigin(t *testing.T) {
	t.Setenv("CORS_EXTRA_ORIGINS", "https://chat.example.com, http://my.lan:9000")
	cases := []struct {
		origin string
		want   bool
	}{
		{"http://localhost:5173", true},
		{"http://127.0.0.1:3000", true},
		{"http://192.168.101.4:5173", true},  // LAN IP on dev port
		{"http://10.0.0.5:4173", true},       // private 10/8
		{"http://172.20.1.2:5174", true},     // private 172.16-31
		{"http://192.168.101.4:8080", false}, // private IP but non-dev port
		{"http://8.8.8.8:5173", false},       // public IP rejected
		{"https://evil.com", false},          // arbitrary host
		{"https://chat.example.com", true},   // env override exact match
		{"http://my.lan:9000", true},         // env override exact match
		{"http://localhost:9999", false},     // localhost but non-dev port
		{"garbage", false},
	}
	for _, c := range cases {
		if got := isAllowedOrigin(c.origin); got != c.want {
			t.Errorf("isAllowedOrigin(%q) = %v, want %v", c.origin, got, c.want)
		}
	}
}

func TestIsAllowedOriginForRequest_RejectsForwardedOriginByDefault(t *testing.T) {
	t.Setenv("CORS_EXTRA_ORIGINS", "")
	req := httptest.NewRequest("POST", "http://backend:8080/api/v1/auth/login", nil)
	req.Host = "backend:8080"
	req.Header.Set("X-Forwarded-Host", "chat.example.com")
	req.Header.Set("X-Forwarded-Proto", "https")

	if isAllowedOriginForRequest(req, "https://chat.example.com", false) {
		t.Fatal("same forwarded origin should be rejected when proxy trust is disabled")
	}
}

func TestIsAllowedOriginForRequest_AllowsSameForwardedOriginWhenTrusted(t *testing.T) {
	t.Setenv("CORS_EXTRA_ORIGINS", "")
	req := httptest.NewRequest("POST", "http://backend:8080/api/v1/auth/login", nil)
	req.Host = "backend:8080"
	req.Header.Set("X-Forwarded-Host", "chat.example.com")
	req.Header.Set("X-Forwarded-Proto", "https")

	if !isAllowedOriginForRequest(req, "https://chat.example.com", true) {
		t.Fatal("same forwarded origin should be allowed when proxy trust is enabled")
	}
}

func TestIsAllowedOriginForRequest_RejectsDifferentForwardedOrigin(t *testing.T) {
	t.Setenv("CORS_EXTRA_ORIGINS", "")
	req := httptest.NewRequest("POST", "http://backend:8080/api/v1/auth/login", nil)
	req.Host = "backend:8080"
	req.Header.Set("X-Forwarded-Host", "chat.example.com")
	req.Header.Set("X-Forwarded-Proto", "https")

	if isAllowedOriginForRequest(req, "https://evil.example.com", true) {
		t.Fatal("different forwarded origin should be rejected")
	}
}

func TestSecurityHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(securityHeaders(false))
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	r.ServeHTTP(rec, req)

	headers := rec.Header()
	if got := headers.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := headers.Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options = %q, want DENY", got)
	}
	if got := headers.Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q, want no-referrer", got)
	}
	if got := headers.Get("Permissions-Policy"); got == "" {
		t.Fatal("Permissions-Policy should be set")
	}
	if got := headers.Get("Content-Security-Policy"); got == "" {
		t.Fatal("Content-Security-Policy should be set")
	}
	if got := headers.Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("Strict-Transport-Security over plain HTTP = %q, want empty", got)
	}
}

func TestSecurityHeaders_HSTSRequiresHTTPSOrTrustedProxy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(securityHeaders(true))
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	r.ServeHTTP(rec, req)

	if got := rec.Header().Get("Strict-Transport-Security"); got != "max-age=31536000; includeSubDomains" {
		t.Fatalf("Strict-Transport-Security = %q, want release policy", got)
	}
}

func TestAPICacheControl(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(apiCacheControl())
	r.GET("/api/v1/auth/login", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	r.GET("/health", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	apiRecorder := httptest.NewRecorder()
	r.ServeHTTP(apiRecorder, httptest.NewRequest(http.MethodGet, "/api/v1/auth/login", nil))
	if got := apiRecorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("api Cache-Control = %q, want no-store", got)
	}

	healthRecorder := httptest.NewRecorder()
	r.ServeHTTP(healthRecorder, httptest.NewRequest(http.MethodGet, "/health", nil))
	if got := healthRecorder.Header().Get("Cache-Control"); got != "" {
		t.Fatalf("health Cache-Control = %q, want empty", got)
	}
}

func TestClientIPMiddlewareOnlyUsesProxyHeaderWhenTrusted(t *testing.T) {
	for _, trusted := range []bool{false, true} {
		t.Run(map[bool]string{false: "untrusted", true: "trusted"}[trusted], func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := gin.New()
			r.Use(clientIPMiddleware(trusted))
			r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, c.GetString("client_ip")) })
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = "192.0.2.1:9000"
			req.Header.Set("X-Real-IP", "198.51.100.10")
			r.ServeHTTP(recorder, req)
			want := "192.0.2.1"
			if trusted {
				want = "198.51.100.10"
			}
			if got := recorder.Body.String(); got != want {
				t.Fatalf("client ip = %q, want %q", got, want)
			}
		})
	}
}

func TestRequestIDMiddleware_GeneratesAndExposesRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(requestIDMiddleware())
	r.GET("/health", func(c *gin.Context) {
		id, ok := c.Get("request_id")
		if !ok || id == "" {
			t.Fatal("request_id should be stored in gin context")
		}
		c.JSON(http.StatusOK, gin.H{"request_id": id})
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	r.ServeHTTP(rec, req)

	if got := rec.Header().Get(requestIDHeader); got == "" {
		t.Fatal("generated request id response header should be set")
	}
}

func TestRequestIDMiddleware_PreservesCleanClientRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(requestIDMiddleware())
	r.GET("/health", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set(requestIDHeader, "  req-123\r\n  ")
	r.ServeHTTP(rec, req)

	if got := rec.Header().Get(requestIDHeader); got != "req-123" {
		t.Fatalf("request id = %q, want req-123", got)
	}
}
