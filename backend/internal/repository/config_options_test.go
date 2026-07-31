package repository

import (
	"strings"
	"testing"
)

// clampToOptions 应把不在档位内的值夹紧到最近的合法档位，档位内的值原样返回，
// 无档位定义的 key 不受影响。这是防止压缩阈值被误设成过小值（如 1000）的兜底。
func TestClampToOptions(t *testing.T) {
	const key = "compression_context_threshold"

	cases := []struct {
		name string
		in   int
		want int
	}{
		{"低于最小档夹到 8000", 1000, 8000},
		{"恰好命中档位", 32000, 32000},
		{"两档之间取最近", 20000, 16000},
		{"高于最大档夹到 128000", 999999, 128000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := clampToOptions(key, c.in); got != c.want {
				t.Errorf("clampToOptions(%d) = %d, want %d", c.in, got, c.want)
			}
		})
	}

	// 无档位定义的 key 原样返回。
	if got := clampToOptions("title_generation_trigger", 7); got != 7 {
		t.Errorf("non-select key should pass through, got %d", got)
	}
}

func TestAdminEditableConfig_ExcludesRemovedConfigKeys(t *testing.T) {
	removed := []string{
		"compression_threshold",
		"compression_message_threshold",
		"attachment_max_tokens",
		"file_upload_max_session_total_mb",
	}
	for _, key := range removed {
		if _, ok := AdminEditableConfig[key]; ok {
			t.Fatalf("removed config key %q must not be admin editable", key)
		}
	}
}

func TestAdminEditableConfig_SeparatesUtilityMemorySettings(t *testing.T) {
	modelKeys := []string{"title_generation_model", "extract_summary_model"}
	for _, key := range modelKeys {
		modelMeta, ok := AdminEditableConfig[key]
		if !ok {
			t.Fatalf("%s missing from admin config", key)
		}
		if modelMeta.Category != "系统小模型" || modelMeta.ConfigType != "string" {
			t.Fatalf("%s meta = %+v", key, modelMeta)
		}
	}
	for _, key := range []string{"compression_model", "memory_maintenance_model"} {
		if _, ok := AdminEditableConfig[key]; ok {
			t.Fatalf("%s should not be admin editable because compaction and memory maintenance reuse the active chat model", key)
		}
	}
	limitMeta, ok := AdminEditableConfig["memory_max_chars"]
	if !ok {
		t.Fatal("memory_max_chars missing from admin config")
	}
	if limitMeta.Category != "会话记忆" || limitMeta.ConfigType != "select" || len(limitMeta.Options) == 0 {
		t.Fatalf("memory_max_chars meta = %+v", limitMeta)
	}
	for _, option := range limitMeta.Options {
		if !strings.Contains(option.Label, "输出 token") {
			t.Fatalf("memory_max_chars option does not disclose output requirement: %+v", option)
		}
	}
}
