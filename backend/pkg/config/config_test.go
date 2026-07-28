package config

import (
	"testing"
	"time"
)

func TestLoadAIConfigIgnoresBusinessRuntimeEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-openai")
	t.Setenv("OPENAI_BASE_URL", "https://gw.example.com/v1")
	t.Setenv("TAVILY_API_KEY", "tvly-test")
	t.Setenv("SEARXNG_URL", "https://search.example.com")
	cfg := loadAIConfig()

	if cfg.CompressionMaxTokens != 32000 {
		t.Errorf("CompressionMaxTokens = %d, want default 32000", cfg.CompressionMaxTokens)
	}
}

func TestLoadAIConfigCompressionThreshold(t *testing.T) {
	t.Setenv("COMPRESSION_MAX_TOKENS", "64000")
	cfg := loadAIConfig()

	if cfg.CompressionMaxTokens != 64000 {
		t.Errorf("CompressionMaxTokens = %d, want 64000", cfg.CompressionMaxTokens)
	}
}

func TestLoadRunConfig(t *testing.T) {
	t.Setenv("RUN_MAX_TOTAL_DURATION", "25m")
	t.Setenv("SSE_HEARTBEAT_INTERVAL", "8s")

	cfg := Load()

	if cfg.Run.MaxTotalDuration != 25*time.Minute {
		t.Errorf("MaxTotalDuration = %v", cfg.Run.MaxTotalDuration)
	}
	if cfg.Run.HeartbeatInterval != 8*time.Second {
		t.Errorf("HeartbeatInterval = %v", cfg.Run.HeartbeatInterval)
	}
}
