package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/huoguojun123/EffChat/internal/repository"
)

func TestToolConfigService_RuntimeConfigDefaults(t *testing.T) {
	runtime := NewToolConfigService(nil).RuntimeConfigSet()

	if !runtime.IsEnabled("web_search") {
		t.Fatal("web_search should be enabled by default")
	}
	if got := runtime.Timeout("web_search").Seconds(); got != 20 {
		t.Fatalf("web_search timeout = %.0f, want 20", got)
	}
	if got := runtime.Timeout("web_extract").Seconds(); got != 30 {
		t.Fatalf("web_extract timeout = %.0f, want 30", got)
	}
	if runtime.IsEnabled("unregistered_tool") {
		t.Fatal("unknown tools must fail closed")
	}
	if runtime.IsKnown("unregistered_tool") {
		t.Fatal("unknown tool reported as governed")
	}
}

func TestToolConfigService_RejectsUnknownTool(t *testing.T) {
	enabled := true
	_, err := toolConfigFromInput(&ToolConfigInput{
		Key:            "shell",
		DisplayName:    "Shell",
		Enabled:        &enabled,
		TimeoutSeconds: 20,
	})
	if !errors.Is(err, ErrToolConfigInvalid) {
		t.Fatalf("unknown tool error = %v, want invalid tool configuration", err)
	}
}

func TestToolConfigService_ClampsTimeout(t *testing.T) {
	enabled := true
	item, err := toolConfigFromInput(&ToolConfigInput{
		Key:            "web_extract",
		DisplayName:    "Web extract",
		Enabled:        &enabled,
		TimeoutSeconds: 999,
	})
	if err != nil {
		t.Fatalf("toolConfigFromInput returned error: %v", err)
	}
	if item.TimeoutSeconds != 120 {
		t.Fatalf("timeout = %d, want 120", item.TimeoutSeconds)
	}
}

func TestToolConfigService_RuntimeConfigFailsClosedWhenRepositoryReadFails(t *testing.T) {
	runtime := NewToolConfigService(&repository.ToolConfigRepository{}).RuntimeConfigSet()
	for _, key := range []string{"web_search", "web_extract", "memory"} {
		if runtime.IsEnabled(key) {
			t.Fatalf("%s should be disabled when tool configuration cannot be read", key)
		}
	}
}

func TestNormalizeGovernanceReason(t *testing.T) {
	if got := normalizeGovernanceReason("  ", "fallback"); got != "fallback" {
		t.Fatalf("empty reason = %q", got)
	}
	long := strings.Repeat("治", 501)
	if got := []rune(normalizeGovernanceReason(long, "fallback")); len(got) != 500 || got[499] != '治' {
		t.Fatalf("unicode reason length=%d", len(got))
	}
}
