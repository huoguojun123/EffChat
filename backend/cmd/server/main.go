package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/huoguojun123/EffChat/internal/handler"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/pkg/config"
	"github.com/huoguojun123/EffChat/pkg/db"
	"github.com/huoguojun123/EffChat/pkg/logger"
	"github.com/joho/godotenv"
)

const (
	serverDrainTimeout       = 45 * time.Second
	serverFinalizeTimeout    = 5 * time.Second
	serverForceTerminalLimit = 5 * time.Second
	serverReadHeaderTimeout  = 10 * time.Second
	serverIdleTimeout        = 75 * time.Second
	serverMaxHeaderBytes     = 1 << 20
)

func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: serverReadHeaderTimeout,
		IdleTimeout:       serverIdleTimeout,
		MaxHeaderBytes:    serverMaxHeaderBytes,
	}
}

func main() {
	if err := godotenv.Overload(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	logger.Init()

	cfg := config.Load()

	// JWT secret 门禁：release 模式下拒绝用占位默认值启动（否则 token 可被伪造）；
	// debug 模式仅大声告警，保留本地开发便利。
	// 用前缀匹配同时覆盖 config 默认值与 .env.example 占位（…-in-production）。
	if cfg.JWT.Secret == "" || strings.HasPrefix(cfg.JWT.Secret, config.DefaultJWTSecret) {
		if cfg.Server.Mode == "release" {
			logger.Fatal("JWT_SECRET 未设置或仍为默认值，release 模式拒绝启动；请在环境变量中配置强随机 JWT_SECRET")
		}
		logger.Error("[安全告警] JWT_SECRET 仍为默认值，仅供本地开发；上线前必须设置强随机 JWT_SECRET")
	}

	database, err := db.Connect(cfg.Database)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer database.Close()

	schemaCtx, schemaCancel := context.WithTimeout(context.Background(), 5*time.Second)
	schemaVersion, err := db.VerifySchemaVersion(schemaCtx, database)
	schemaCancel()
	if err != nil {
		log.Fatalf("database schema is incompatible: %v; run the migration service before starting backend", err)
	}
	reconcileCtx, reconcileCancel := context.WithTimeout(context.Background(), 5*time.Second)
	reconciledRuns, err := repository.NewQuotaRepository(database).ReconcileRunningChatRuns(reconcileCtx)
	reconcileCancel()
	if err != nil {
		log.Fatalf("failed to reconcile interrupted chat runs: %v", err)
	}
	if reconciledRuns > 0 {
		logger.Info("Reconciled %d interrupted chat runs from the previous process", reconciledRuns)
	}

	if cfg.Server.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	if err := r.SetTrustedProxies(nil); err != nil {
		log.Fatalf("configure trusted proxies: %v", err)
	}
	r.Use(requestIDMiddleware())
	r.Use(clientIPMiddleware(cfg.Security.TrustProxyHeaders))
	r.Use(gin.LoggerWithFormatter(requestLogFormatter))
	r.Use(gin.Recovery())
	r.Use(securityHeaders(cfg.Security.TrustProxyHeaders))
	r.Use(apiCacheControl())

	// CORS
	r.Use(cors.New(cors.Config{
		AllowOriginWithContextFunc: func(c *gin.Context, origin string) bool {
			return isAllowedOriginForRequest(c.Request, origin, cfg.Security.TrustProxyHeaders)
		},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", requestIDHeader},
		ExposeHeaders:    []string{"Content-Length", requestIDHeader},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// 健康检查
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status":         "ok",
			"service":        "effchat-backend",
			"version":        config.AppVersion,
			"build_ref":      config.BuildRef,
			"schema_version": schemaVersion,
		})
	})

	runHub, stopOCRRecovery := handler.RegisterRoutes(r, database, cfg)
	defer stopOCRRecovery()

	// 优雅停机
	addr := cfg.Server.Host + ":" + cfg.Server.Port
	srv := newHTTPServer(addr, r)

	go func() {
		logger.Info("Server starting on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("Server draining active conversations...")
	runHub.BeginDrain()
	drainCtx, drainCancel := context.WithTimeout(context.Background(), serverDrainTimeout)
	if !runHub.WaitForIdle(drainCtx) {
		logger.Info("Server drain timed out; canceling %d active conversations", runHub.CancelForDrain())
		finalizeCtx, finalizeCancel := context.WithTimeout(context.Background(), serverFinalizeTimeout)
		finalized := runHub.WaitForIdle(finalizeCtx)
		finalizeCancel()
		if !finalized {
			forceCtx, forceCancel := context.WithTimeout(context.Background(), serverForceTerminalLimit)
			count, err := runHub.FinalizeForDrain(forceCtx)
			forceCancel()
			if err != nil {
				logger.Error("Force-finalize draining conversations failed after %d transitions: %v", count, err)
			} else {
				logger.Info("Force-finalized %d draining conversations", count)
			}
		}
	}
	drainCancel()
	stopOCRRecovery()
	logger.Info("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}
	logger.Info("Server exited")
}

// devOriginPorts 是允许跨域的前端开发/预览端口。
var devOriginPorts = map[string]bool{"3000": true, "4173": true, "5173": true, "5174": true}

// isAllowedOrigin 判定 CORS 来源是否放行：
//   - 回环地址（localhost / 127.0.0.1 / [::1]）任意 dev 端口
//   - 私网 IP（192.168/10/172.16-31）在 dev 端口上 —— 支持局域网用 IP 访问
//   - CORS_EXTRA_ORIGINS 环境变量里逗号分隔的精确来源（用于自定义域名/端口）
func isAllowedOrigin(origin string) bool {
	for _, extra := range strings.Split(os.Getenv("CORS_EXTRA_ORIGINS"), ",") {
		if e := strings.TrimSpace(extra); e != "" && e == origin {
			return true
		}
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	if !devOriginPorts[u.Port()] {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate()
}

func isAllowedOriginForRequest(r *http.Request, origin string, trustProxyHeaders bool) bool {
	if isAllowedOrigin(origin) {
		return true
	}
	if !trustProxyHeaders {
		return false
	}
	return isSameForwardedOrigin(r, origin)
}

func isSameForwardedOrigin(r *http.Request, origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	reqHost := firstForwardedValue(r.Header.Get("X-Forwarded-Host"))
	if reqHost == "" {
		reqHost = r.Host
	}
	reqProto := firstForwardedValue(r.Header.Get("X-Forwarded-Proto"))
	if reqProto == "" {
		if r.TLS != nil {
			reqProto = "https"
		} else {
			reqProto = "http"
		}
	}
	return strings.EqualFold(u.Scheme, reqProto) && strings.EqualFold(u.Host, reqHost)
}

func firstForwardedValue(value string) string {
	if i := strings.IndexByte(value, ','); i >= 0 {
		value = value[:i]
	}
	return strings.TrimSpace(value)
}

func securityHeaders(trustProxyHeaders bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.Writer.Header()
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("X-Frame-Options", "DENY")
		header.Set("Referrer-Policy", "no-referrer")
		header.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		header.Set("Content-Security-Policy", "default-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
		if isHTTPSRequest(c.Request, trustProxyHeaders) {
			header.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		c.Next()
	}
}

func apiCacheControl() gin.HandlerFunc {
	return func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.Header("Cache-Control", "no-store")
		}
		c.Next()
	}
}

func clientIPMiddleware(trustProxyHeaders bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.RemoteIP()
		if trustProxyHeaders {
			if forwarded := net.ParseIP(strings.TrimSpace(c.GetHeader("X-Real-IP"))); forwarded != nil {
				ip = forwarded.String()
			}
		}
		c.Set("client_ip", ip)
		c.Next()
	}
}

func isHTTPSRequest(r *http.Request, trustProxyHeaders bool) bool {
	if r.TLS != nil {
		return true
	}
	if !trustProxyHeaders {
		return false
	}
	return strings.EqualFold(firstForwardedValue(r.Header.Get("X-Forwarded-Proto")), "https")
}

const requestIDHeader = "X-Request-Id"

func requestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := cleanRequestID(c.GetHeader(requestIDHeader))
		if requestID == "" {
			requestID = uuid.NewString()
		}
		c.Set("request_id", requestID)
		c.Writer.Header().Set(requestIDHeader, requestID)
		c.Request.Header.Set(requestIDHeader, requestID)
		c.Next()
	}
}

func cleanRequestID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
		if b.Len() >= 128 {
			break
		}
	}
	return strings.TrimSpace(b.String())
}

func requestLogFormatter(p gin.LogFormatterParams) string {
	requestID := p.Request.Header.Get(requestIDHeader)
	if requestID == "" {
		requestID = "-"
	}
	return fmt.Sprintf("[GIN] %v | %3d | %13v | %15s | %s | %-7s %#v\n%s",
		p.TimeStamp.Format("2006/01/02 - 15:04:05"),
		p.StatusCode,
		p.Latency,
		p.ClientIP,
		requestID,
		p.Method,
		p.Path,
		p.ErrorMessage,
	)
}
