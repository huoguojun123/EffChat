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

func TestLoadRunConfigFirstOutputTimeout(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{name: "Go duration", value: "25m", want: 25 * time.Minute},
		{name: "plain seconds", value: "90", want: 90 * time.Second},
		{name: "explicit zero", value: "0"},
		{name: "invalid value uses handler defaults", value: "invalid"},
		{name: "unset uses handler defaults"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("RUN_FIRST_OUTPUT_TIMEOUT", testCase.value)

			cfg := Load()
			if cfg.Run.FirstOutputTimeout != testCase.want {
				t.Errorf("FirstOutputTimeout = %v, want %v", cfg.Run.FirstOutputTimeout, testCase.want)
			}
		})
	}
}

func TestLoadRunConfigHeartbeat(t *testing.T) {
	t.Setenv("SSE_HEARTBEAT_INTERVAL", "8s")
	if got := Load().Run.HeartbeatInterval; got != 8*time.Second {
		t.Errorf("HeartbeatInterval = %v, want 8s", got)
	}
}
