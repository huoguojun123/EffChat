package modelbank

import (
	"testing"

	"github.com/huoguojun123/EffChat/internal/model"
)

// TestLoadModels_OverridesBuiltins 验证 DB 记录能覆盖内置默认，且能力字段正确映射。
func TestLoadModels_OverridesBuiltins(t *testing.T) {
	// 用一个与内置不同的自定义模型加载
	LoadModels([]*model.Model{
		{
			ID: "custom-model", DisplayName: "Custom", Provider: "openai",
			Vision: true, ToolUse: true, Reasoning: true, SearchImpl: "tool",
			ContextWindow: 12345, MaxOutput: 678, Enabled: true,
		},
	})

	// 覆盖后内置的 gpt-4o 应不再存在
	if Get("gpt-4o") != nil {
		t.Error("LoadModels 应替换整个注册表，gpt-4o 不该残留")
	}

	m := Get("custom-model")
	if m == nil {
		t.Fatal("custom-model 未加载")
	}
	if !m.Capabilities.Vision || !m.Capabilities.ToolUse || !m.Capabilities.Reasoning {
		t.Error("能力布尔字段映射错误")
	}
	if m.Capabilities.SearchImpl != SearchImplTool {
		t.Errorf("SearchImpl = %q, want tool", m.Capabilities.SearchImpl)
	}
	if m.Capabilities.ContextWindow != 12345 || m.Capabilities.MaxOutput != 678 {
		t.Errorf("token 数字映射错误: ctx=%d out=%d", m.Capabilities.ContextWindow, m.Capabilities.MaxOutput)
	}
	// 还原内置默认，避免污染同包其它测试
	resetRegistryToBuiltins()
}

// TestLoadModels_EmptyClearsRegistry 验证空数据库也会清空运行时模型表。
func TestLoadModels_EmptyClearsRegistry(t *testing.T) {
	resetRegistryToBuiltins() // 隔离前序测试的覆盖影响
	before := Get("gpt-4o")
	if before == nil {
		t.Fatal("前置条件：内置 gpt-4o 应存在")
	}
	LoadModels(nil)
	if Get("gpt-4o") != nil {
		t.Error("nil 模型列表应清空运行时模型表")
	}
	resetRegistryToBuiltins()
	LoadModels([]*model.Model{})
	if Get("gpt-4o") != nil {
		t.Error("空切片应清空运行时模型表")
	}
	resetRegistryToBuiltins() // 还原，避免影响 search_config_test 等同包测试
}

func TestBuiltinsIncludeGPT56Family(t *testing.T) {
	resetRegistryToBuiltins()
	for _, id := range []string{"gpt-5.6", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"} {
		info := Get(id)
		if info == nil {
			t.Fatalf("missing builtin %s", id)
		}
		if info.Capabilities.ContextWindow != 1050000 || info.Capabilities.MaxOutput != 128000 {
			t.Fatalf("%s limits = %d/%d", id, info.Capabilities.ContextWindow, info.Capabilities.MaxOutput)
		}
	}
}

func TestBuiltinsIncludeCandidateDayModelFamilies(t *testing.T) {
	resetRegistryToBuiltins()
	cases := []struct {
		id       string
		provider string
		context  int
		output   int
	}{
		{id: "claude-opus-5", provider: "anthropic", context: 1000000, output: 128000},
		{id: "claude-sonnet-5", provider: "anthropic", context: 1000000, output: 128000},
		{id: "claude-opus-4-8", provider: "anthropic", context: 1000000, output: 128000},
		{id: "claude-sonnet-4-7", provider: "anthropic", context: 1000000, output: 128000},
		{id: "gemini-3.7-flash", provider: "google", context: 1048576, output: 65536},
		{id: "gemini-3.6-flash", provider: "google", context: 1048576, output: 65536},
	}
	for _, tc := range cases {
		info := Get(tc.id)
		if info == nil {
			t.Fatalf("missing candidate-day builtin %s", tc.id)
		}
		if info.Provider != tc.provider || info.Capabilities.ContextWindow != tc.context || info.Capabilities.MaxOutput != tc.output {
			t.Fatalf("%s profile = provider:%q limits:%d/%d", tc.id, info.Provider, info.Capabilities.ContextWindow, info.Capabilities.MaxOutput)
		}
		if !info.Capabilities.Vision || !info.Capabilities.ToolUse || !info.Capabilities.Reasoning {
			t.Fatalf("%s lost required capability evidence: %+v", tc.id, info.Capabilities)
		}
	}
}
