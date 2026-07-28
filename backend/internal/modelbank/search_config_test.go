package modelbank

import "testing"

func TestResolveSearchConfig(t *testing.T) {
	tests := []struct {
		name         string
		modelID      string
		provider     string
		mode         SearchMode
		preferNative bool
		wantEnabled  bool
		wantNative   bool
		wantAppTool  bool
	}{
		{
			name:    "搜索关闭",
			modelID: "gpt-4o-mini", provider: "openai",
			mode: SearchModeOff, preferNative: true,
			wantEnabled: false, wantNative: false, wantAppTool: false,
		},
		{
			name:    "无原生搜索模型 auto → 挂应用工具",
			modelID: "gpt-4o-mini", provider: "openai",
			mode: SearchModeAuto, preferNative: true,
			wantEnabled: true, wantNative: false, wantAppTool: true,
		},
		{
			name:    "Perplexity internal → 透明原生，但当前无工具兜底",
			modelID: "sonar", provider: "perplexity",
			mode: SearchModeAuto, preferNative: false,
			wantEnabled: true, wantNative: true, wantAppTool: false,
		},
		{
			name:    "Gemini params + 偏好原生 → 原生优先且保留工具兜底",
			modelID: "gemini-2.5-flash", provider: "google",
			mode: SearchModeAuto, preferNative: true,
			wantEnabled: true, wantNative: true, wantAppTool: true,
		},
		{
			name:    "Gemini params + 不偏好原生 → 只走应用工具",
			modelID: "gemini-2.5-flash", provider: "google",
			mode: SearchModeOn, preferNative: false,
			wantEnabled: true, wantNative: false, wantAppTool: true,
		},
		{
			name:    "on 强制纯工具：params 型即使偏好原生也关原生",
			modelID: "gemini-2.5-flash", provider: "google",
			mode: SearchModeOn, preferNative: true,
			wantEnabled: true, wantNative: false, wantAppTool: true,
		},
		{
			name:    "on 强制纯工具：Claude tool 型即使偏好原生也关原生",
			modelID: "claude-sonnet-4-6", provider: "anthropic",
			mode: SearchModeOn, preferNative: true,
			wantEnabled: true, wantNative: false, wantAppTool: true,
		},
		{
			name:    "on internal 型例外：原生透明不可关，仍保持原生",
			modelID: "sonar", provider: "perplexity",
			mode: SearchModeOn, preferNative: false,
			wantEnabled: true, wantNative: true, wantAppTool: false,
		},
		{
			name:    "Claude tool + 偏好原生 → 保留工具兜底",
			modelID: "claude-sonnet-4-6", provider: "anthropic",
			mode: SearchModeAuto, preferNative: true,
			wantEnabled: true, wantNative: true, wantAppTool: true,
		},
		{
			name:    "未知模型 → 当作无原生搜索，挂应用工具",
			modelID: "some-unknown-model", provider: "openai",
			mode: SearchModeAuto, preferNative: true,
			wantEnabled: true, wantNative: false, wantAppTool: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveSearchConfig(tt.modelID, tt.provider, tt.mode, tt.preferNative)
			if got.EnabledSearch != tt.wantEnabled {
				t.Errorf("EnabledSearch = %v, want %v", got.EnabledSearch, tt.wantEnabled)
			}
			if got.UseModelNativeSearch != tt.wantNative {
				t.Errorf("UseModelNativeSearch = %v, want %v", got.UseModelNativeSearch, tt.wantNative)
			}
			if got.UseApplicationTool != tt.wantAppTool {
				t.Errorf("UseApplicationTool = %v, want %v", got.UseApplicationTool, tt.wantAppTool)
			}
		})
	}
}
