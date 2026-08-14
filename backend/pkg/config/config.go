package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// AppVersion 是前后端展示和健康检查使用的当前版本标识。
//
// 现在项目处在预发布测试阶段，不承诺稳定 API；把版本集中放在 config 包里，
// 避免 /health、管理后台标题、后续日志分别写死不同字符串。
const AppVersion = "0.3.4-beta.3"

// BuildRef is set by the release image build and remains "unknown" for local Go runs.
var BuildRef = "unknown"

type Config struct {
	Server    ServerConfig
	Database  DatabaseConfig
	JWT       JWTConfig
	AI        AIConfig
	Run       RunConfig
	Security  SecurityConfig
	Extractor ExtractorConfig
}

type ServerConfig struct {
	Host string
	Port string
	Mode string // debug, release
}

type DatabaseConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
	SSLMode  string
}

type JWTConfig struct {
	Secret string
}

// DefaultJWTSecret 是未配置 JWT_SECRET 时的占位默认值。
// main 用它做启动门禁：release 模式下若仍是该值则拒绝启动（防 token 伪造）。
const DefaultJWTSecret = "your-secret-key-change-this"

type RunConfig struct {
	FirstOutputTimeout time.Duration
	HeartbeatInterval  time.Duration
}

type SecurityConfig struct {
	TrustProxyHeaders bool
	AuthRateLimit     AuthRateLimitConfig
}

type AuthRateLimitConfig struct {
	MaxAttempts int
	Window      time.Duration
	Block       time.Duration
}

type ExtractorConfig struct {
	Enabled        bool
	URL            string
	Timeout        time.Duration
	MaxUploadBytes int64
}

const defaultExtractorMaxUploadBytes int64 = 25 * 1024 * 1024

// AIConfig 只保留基础运行参数。模型渠道、API key、搜索与网页提取服务
// 由管理员后台持久化配置，运行时不再读取 .env 作为业务配置 fallback。
type AIConfig struct {
	// Compression 对话压缩阈值（由 eino summarization 中间件在运行时消费）。
	// 只按上下文 token 触发压缩，不再提供消息数/轮数阈值。
	CompressionMaxTokens int // 上下文 token 超过此值触发压缩
}

func Load() *Config {
	return &Config{
		Server: ServerConfig{
			Host: getEnv("SERVER_HOST", "0.0.0.0"),
			Port: getEnv("SERVER_PORT", "8080"),
			Mode: getEnv("SERVER_MODE", "debug"),
		},
		Database: DatabaseConfig{
			Host:     getEnv("DB_HOST", "localhost"),
			Port:     getEnvInt("DB_PORT", 5432),
			User:     getEnv("DB_USER", "effchat"),
			Password: getEnv("DB_PASSWORD", ""),
			DBName:   getEnv("DB_NAME", "effchat"),
			SSLMode:  getEnv("DB_SSLMODE", "disable"),
		},
		JWT: JWTConfig{
			Secret: getEnv("JWT_SECRET", DefaultJWTSecret),
		},
		AI: loadAIConfig(),
		Run: RunConfig{
			FirstOutputTimeout: getEnvDuration("RUN_FIRST_OUTPUT_TIMEOUT", 0),
			HeartbeatInterval:  getEnvDuration("SSE_HEARTBEAT_INTERVAL", 12*time.Second),
		},
		Security: SecurityConfig{
			TrustProxyHeaders: getEnvBool("TRUST_PROXY_HEADERS", false),
			AuthRateLimit: AuthRateLimitConfig{
				MaxAttempts: getEnvInt("AUTH_RATE_LIMIT_MAX_ATTEMPTS", 10),
				Window:      getEnvDuration("AUTH_RATE_LIMIT_WINDOW_SECONDS", 10*time.Minute),
				Block:       getEnvDuration("AUTH_RATE_LIMIT_BLOCK_SECONDS", 15*time.Minute),
			},
		},
		Extractor: ExtractorConfig{
			Enabled:        getEnvBool("PY_EXTRACTOR_ENABLED", true),
			URL:            getEnv("PY_EXTRACTOR_URL", "http://py-extractor:8090"),
			Timeout:        getEnvDuration("PY_EXTRACTOR_TIMEOUT_SECONDS", 60*time.Second),
			MaxUploadBytes: getEnvPositiveInt64("PY_EXTRACTOR_MAX_UPLOAD_BYTES", defaultExtractorMaxUploadBytes),
		},
	}
}

func loadAIConfig() AIConfig {
	return AIConfig{
		CompressionMaxTokens: getEnvInt("COMPRESSION_MAX_TOKENS", 32000),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getEnvPositiveInt64(key string, defaultValue int64) int64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return defaultValue
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return defaultValue
	}
	return parsed
}

// getEnvBool 解析布尔环境变量；未设置或无法解析时返回 defaultValue。
// 接受 1/t/T/true/TRUE/0/f/false 等 strconv.ParseBool 支持的写法。
func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if b, err := strconv.ParseBool(value); err == nil {
			return b
		}
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return defaultValue
	}
	if duration, err := time.ParseDuration(value); err == nil {
		return duration
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		return time.Duration(seconds) * time.Second
	}
	return defaultValue
}
