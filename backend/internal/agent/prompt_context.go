package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/huoguojun123/effchat/internal/modelbank"
)

func formatUserBlock(req *ChatRequest, userDisplayName string) string {
	lines := make([]string, 0, 4)
	if userDisplayName != "" {
		lines = append(lines, fmt.Sprintf("- Display name: %s", userDisplayName))
	}
	if strings.TrimSpace(req.UserName) != "" {
		lines = append(lines, fmt.Sprintf("- Username: %s", strings.TrimSpace(req.UserName)))
	}
	if strings.TrimSpace(req.UserNickname) != "" && strings.TrimSpace(req.UserNickname) != userDisplayName {
		lines = append(lines, fmt.Sprintf("- Nickname: %s", strings.TrimSpace(req.UserNickname)))
	}
	if strings.TrimSpace(req.UserRole) != "" {
		lines = append(lines, fmt.Sprintf("- Role: %s", strings.TrimSpace(req.UserRole)))
	}
	return strings.Join(lines, "\n")
}

func formatSessionBlock(req *ChatRequest) string {
	lines := make([]string, 0, 7)
	if strings.TrimSpace(req.SessionTitle) != "" {
		lines = append(lines, fmt.Sprintf("- Session title: %s", strings.TrimSpace(req.SessionTitle)))
	}
	if strings.TrimSpace(req.ModelID) != "" {
		lines = append(lines, fmt.Sprintf("- Current model: %s", strings.TrimSpace(req.ModelID)))
	}
	if strings.TrimSpace(req.Provider) != "" {
		lines = append(lines, fmt.Sprintf("- Model channel: %s", strings.TrimSpace(req.Provider)))
	}
	if strings.TrimSpace(req.MessageFormat) != "" {
		lines = append(lines, fmt.Sprintf("- Message format: %s", strings.TrimSpace(req.MessageFormat)))
	}
	if value := formatFloat(req.Temperature); value != "" {
		lines = append(lines, fmt.Sprintf("- Temperature: %s", value))
	}
	if value := formatInt(req.MaxTokens); value != "" {
		lines = append(lines, fmt.Sprintf("- Max output tokens: %s", value))
	}
	if value := formatSearchMode(req.SearchMode); value != "" {
		lines = append(lines, fmt.Sprintf("- Web search: %s", value))
	}
	return strings.Join(lines, "\n")
}

// formatCapabilityBlock 根据当前模型的真实能力生成自我认知说明，
// 让模型清楚自己能否看图 / 调用工具 / 多步推理，避免越权承诺或漏用能力。
func formatCapabilityBlock(modelID, provider string) string {
	info := modelbank.GetOrDefault(strings.TrimSpace(modelID), strings.TrimSpace(provider))
	if info == nil {
		return ""
	}
	caps := info.Capabilities
	lines := make([]string, 0, 4)

	if caps.Vision {
		lines = append(lines, "- Vision: can inspect and reason over user-provided images.")
	} else {
		lines = append(lines, "- Vision: cannot inspect images. If an image is required, explain this and ask for a text description.")
	}

	if caps.ToolUse {
		lines = append(lines, "- Tools: can call mounted tools when needed to retrieve live, external, file, memory, or skill information.")
	} else {
		lines = append(lines, "- Tools: cannot call tools. Answer only from existing knowledge and context; never pretend a tool was called.")
	}

	if caps.Reasoning {
		lines = append(lines, "- Reasoning: supports multi-step reasoning for complex tasks before giving a concise final answer.")
	}

	switch caps.SearchImpl {
	case modelbank.SearchImplInternal:
		lines = append(lines, "- Search: has transparent built-in web retrieval and may use current information directly.")
	case modelbank.SearchImplParams, modelbank.SearchImplTool:
		lines = append(lines, "- Search: can retrieve web information when current or external evidence is needed.")
	}

	return strings.Join(lines, "\n")
}

func parsePreferenceMap(raw []byte) map[string]interface{} {
	if len(raw) == 0 {
		return nil
	}
	var data map[string]interface{}
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil
	}
	return data
}

func parseSessionPreferenceMap(raw []byte) map[string]interface{} {
	if len(raw) == 0 {
		return nil
	}
	var data map[string]interface{}
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil
	}
	if nested, ok := data["preferences"].(map[string]interface{}); ok {
		return nested
	}
	return data
}

// formatPreferenceBlock 将前端/数据库保存的偏好 JSON 转为提示词中的稳定条目。
//
// 偏好字段来自用户设置和会话 metadata，历史上存在多个命名版本，例如 language、
// locale、response_language 都表达“语言”。这里显式列出同义 key，而不是把整个
// JSON 原样塞给模型，避免模型看到内部字段名后产生误解，也减少无关 metadata 污染提示词。
func formatPreferenceBlock(data map[string]interface{}) string {
	if len(data) == 0 {
		return ""
	}
	type prefItem struct {
		keys  []string
		label string
	}
	items := []prefItem{
		{keys: []string{"language", "locale", "response_language"}, label: "Language"},
		{keys: []string{"timezone"}, label: "Timezone"},
		{keys: []string{"response_style", "answer_style"}, label: "Response style"},
		{keys: []string{"verbosity"}, label: "Verbosity"},
		{keys: []string{"tone"}, label: "Tone"},
		{keys: []string{"technical_level"}, label: "Technical level"},
		{keys: []string{"search_enabled", "default_search_enabled"}, label: "Default web search"},
		{keys: []string{"salutation", "name_style"}, label: "Preferred address"},
	}
	lines := make([]string, 0, len(items))
	for _, item := range items {
		value, ok := pickPreferenceValue(data, item.keys...)
		if !ok {
			continue
		}
		text := formatPreferenceValue(value)
		if text == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s: %s", item.label, text))
	}
	return strings.Join(lines, "\n")
}

func pickPreferenceValue(data map[string]interface{}, keys ...string) (interface{}, bool) {
	for _, key := range keys {
		value, ok := data[key]
		if ok {
			return value, true
		}
	}
	return nil, false
}

func formatPreferenceValue(value interface{}) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case bool:
		if v {
			return "enabled"
		}
		return "disabled"
	case float64:
		if float64(int(v)) == v {
			return fmt.Sprintf("%d", int(v))
		}
		return fmt.Sprintf("%.2f", v)
	case []interface{}:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			text := formatPreferenceValue(item)
			if text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, ", ")
	default:
		return ""
	}
}

func formatFloat(value *float64) string {
	if value == nil {
		return ""
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", *value), "0"), ".")
}

func formatInt(value int) string {
	if value <= 0 {
		return ""
	}
	return fmt.Sprintf("%d", value)
}

func formatSearchMode(mode modelbank.SearchMode) string {
	switch mode {
	case modelbank.SearchModeOn:
		return "on"
	case modelbank.SearchModeOff:
		return "off"
	case modelbank.SearchModeAuto:
		return "auto"
	default:
		return ""
	}
}
