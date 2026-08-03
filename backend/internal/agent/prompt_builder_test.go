package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	sessionmemory "github.com/huoguojun123/EffChat/internal/memory"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/repository"
)

func TestUserLocationUsesPreferenceAndStableFallback(t *testing.T) {
	now := time.Date(2026, time.July, 12, 16, 30, 0, 0, time.UTC)

	shanghai := userLocation([]byte(`{"timezone":"Asia/Shanghai"}`))
	if got := now.In(shanghai).Format("2006-01-02"); got != "2026-07-13" {
		t.Fatalf("Shanghai date = %s, want 2026-07-13", got)
	}

	newYork := userLocation([]byte(`{"timezone":"America/New_York"}`))
	if got := now.In(newYork).Format("2006-01-02 15:04"); got != "2026-07-12 12:30" {
		t.Fatalf("New York time = %s, want 2026-07-12 12:30", got)
	}

	for _, raw := range [][]byte{nil, []byte(`{"timezone":"Invalid/Zone"}`), []byte(`not json`)} {
		if got := userLocation(raw).String(); got != "Asia/Shanghai" {
			t.Fatalf("fallback location = %q, want Asia/Shanghai", got)
		}
	}
}

func TestBuildInstructionInjectsRuntimeContext(t *testing.T) {
	req := &ChatRequest{
		SystemName:      "effchat",
		ModelID:         "gpt-4o",
		Provider:        "openai",
		SystemPrompt:    "本会话用简洁风格。",
		MessageFormat:   "v1",
		SessionTitle:    "测试会话",
		UserName:        "mock_user",
		UserNickname:    "Mock User",
		UserRole:        "admin",
		UserPreferences: []byte(`{"language":"中文","verbosity":"简洁","timezone":"Asia/Shanghai","default_search_enabled":true}`),
		SearchMode:      modelbank.SearchModeAuto,
	}

	got, err := buildInstruction(nil, req, modelbank.SearchDecision{
		EnabledSearch:      true,
		UseApplicationTool: true,
	}, map[string]bool{"web_search": true, "web_extract": true})
	if err != nil {
		t.Fatalf("buildInstruction: %v", err)
	}

	for _, want := range []string{
		"You are effchat",
		"Date:",
		"Display name: Mock User",
		"Language: 中文",
		"Verbosity: 简洁",
		"Default web search: enabled",
		"Session title: 测试会话",
		"Current model: gpt-4o",
		"本会话用简洁风格。",
		"Use web_search",
		"web_extract",
		"Session Web Evidence",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("instruction missing %q:\n%s", want, got)
		}
	}
}

func TestRenderPromptTemplateRejectsInvalidTemplate(t *testing.T) {
	if _, err := renderPromptTemplate("{{ .SystemName ", PromptTemplateData{SystemName: "effchat"}); err == nil {
		t.Fatal("expected invalid template to be rejected")
	}
}

func TestFormatCapabilityBlockReflectsModelCapabilities(t *testing.T) {
	// 已知具备视觉/工具/推理的模型
	rich := formatCapabilityBlock("gpt-4o", "openai")
	for _, want := range []string{"Vision: can inspect", "Tools: can call mounted tools"} {
		if !strings.Contains(rich, want) {
			t.Fatalf("rich capability block missing %q:\n%s", want, rich)
		}
	}

	// 透明内置搜索的模型应给出对应说明
	internal := formatCapabilityBlock("sonar-reasoning-pro", "perplexity")
	if !strings.Contains(internal, "built-in web retrieval") {
		t.Fatalf("internal-search capability block missing search line:\n%s", internal)
	}
}

func TestBuildInstructionInjectsCapabilityBlock(t *testing.T) {
	req := &ChatRequest{
		SystemName: "effchat",
		ModelID:    "gpt-4o",
		Provider:   "openai",
	}
	got, err := buildInstruction(nil, req, modelbank.SearchDecision{}, nil)
	if err != nil {
		t.Fatalf("buildInstruction: %v", err)
	}
	if !strings.Contains(got, "Vision: can inspect") {
		t.Fatalf("instruction missing vision capability line:\n%s", got)
	}
}

func TestAppendMemoryInstruction(t *testing.T) {
	base := "BASE INSTRUCTION"

	withMem := appendMemoryInstruction(base, "  用户喜欢简洁回答  ")
	if !strings.Contains(withMem, base) {
		t.Error("base instruction lost")
	}
	if !strings.Contains(withMem, "Conversation Memory") {
		t.Error("missing memory section header")
	}
	if !strings.Contains(withMem, "用户喜欢简洁回答") {
		t.Error("memory body not injected")
	}
	if strings.Contains(withMem, `action="add"`) || strings.Contains(withMem, `action="replace"`) {
		t.Error("dynamic memory context should not duplicate the memory tool contract")
	}
	if strings.Contains(withMem, "  用户喜欢简洁回答  ") {
		t.Error("memory should be trimmed")
	}

	empty := appendMemoryInstruction(base, "   ")
	if !strings.Contains(empty, "Current saved memory is empty") {
		t.Errorf("empty memory should show placeholder, got: %s", empty)
	}
}

func TestAppendMemoryInstructionRedactsLegacySecrets(t *testing.T) {
	secret := "fixture-password-42"
	got := appendMemoryInstruction("BASE", "## Decisions\n- password="+secret+"\n- Keep ticket EC-2026-041.")
	if strings.Contains(got, secret) {
		t.Fatalf("legacy memory secret reached model instruction: %s", got)
	}
	if !strings.Contains(got, "EC-2026-041") || !strings.Contains(got, sessionmemory.SensitiveValuePlaceholder) {
		t.Fatalf("prompt redaction lost ordinary memory context: %s", got)
	}
}

func TestBuildInstructionDeclaresOnlyMountedCapabilities(t *testing.T) {
	req := &ChatRequest{
		SystemName: "effchat",
		ModelID:    "gpt-4o",
		Provider:   "openai",
		EnabledSkills: []SkillInstruction{{
			ID:          "mock-skill",
			Name:        "Mock Skill",
			Description: "A fictional test skill.",
		}},
	}

	got, err := buildInstruction(nil, req, modelbank.SearchDecision{}, map[string]bool{
		"file_read":  true,
		"skill_read": true,
	})
	if err != nil {
		t.Fatalf("buildInstruction: %v", err)
	}

	for _, want := range []string{
		"## Available Capabilities This Turn",
		"Mounted tools: file_read, skill_read.",
		"## Enabled Structured Skills",
		"Mock Skill",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("instruction missing %q:\n%s", want, got)
		}
	}
	for _, absent := range []string{
		"## Memory",
		"### Web Tools",
	} {
		if strings.Contains(got, absent) {
			t.Fatalf("instruction declares unavailable capability %q:\n%s", absent, got)
		}
	}
	for _, want := range []string{"### File Tools", "### Skill Tools", "file_read", "skill_read"} {
		if !strings.Contains(got, want) {
			t.Fatalf("instruction removed mounted capability guidance %q:\n%s", want, got)
		}
	}
}

func TestAppendSkillWorkspaceInstructionBoundsMetadata(t *testing.T) {
	skills := make([]SkillInstruction, 0, 40)
	for i := 0; i < 40; i++ {
		files := make([]model.SkillFile, 0, 30)
		for j := 0; j < 30; j++ {
			files = append(files, model.SkillFile{RelativePath: strings.Repeat("reference/", 8) + intString(j) + ".md"})
		}
		skills = append(skills, SkillInstruction{
			ID:          "skill-" + intString(i),
			Name:        "Skill " + intString(i),
			Description: strings.Repeat("bounded description ", 80),
			Files:       files,
		})
	}
	got := appendSkillWorkspaceInstruction("base", skills)
	if !strings.Contains(got, "additional skill(s) omitted") {
		t.Fatalf("bounded skill metadata should disclose omissions:\n%s", got)
	}
	if len([]rune(got))-len([]rune("base")) > maxPromptSkillChars+300 {
		t.Fatalf("skill instruction exceeded budget: %d", len([]rune(got)))
	}
}

func TestBuildInstructionAlwaysDeclaresPlainListMindMaps(t *testing.T) {
	req := &ChatRequest{SystemName: "effchat", ModelID: "gpt-4o", Provider: "openai"}

	got, err := buildInstruction(nil, req, modelbank.SearchDecision{}, nil)
	if err != nil {
		t.Fatalf("buildInstruction: %v", err)
	}
	if count := strings.Count(strings.ToLower(got), "mindmap"); count != 2 {
		t.Fatalf("default instruction should contain one mindmap rule with two syntax mentions, got %d:\n%s", count, got)
	}

	custom := "You are concise."
	got = appendWorkspaceOutputInstruction(custom)
	if !strings.Contains(got, repository.MindMapOutputInstruction) {
		t.Fatalf("custom prompt missing runtime mindmap capability:\n%s", got)
	}
}

func TestFilterCapabilitySectionsPreservesAdministratorContent(t *testing.T) {
	template := `## Knowledge and Current Information
Keep this administrator rule.

## Tools
Keep this custom tool policy.

### File Tools
File guidance.

### Skill Tools
Skill guidance.

### Web Tools
Web guidance.

## Output Format
Keep output rules.`

	got := filterCapabilitySections(template, map[string]bool{"file_read": true})
	for _, want := range []string{
		"Keep this administrator rule.",
		"Keep this custom tool policy.",
		"### File Tools",
		"Keep output rules.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("filtered prompt lost %q:\n%s", want, got)
		}
	}
	for _, absent := range []string{"### Skill Tools", "### Web Tools"} {
		if strings.Contains(got, absent) {
			t.Fatalf("filtered prompt kept unavailable section %q:\n%s", absent, got)
		}
	}
}

func TestBuildInstructionKeepsMemoryPolicyOnlyWhenMounted(t *testing.T) {
	req := &ChatRequest{SystemName: "effchat", ModelID: "gpt-4o", Provider: "openai"}

	withoutMemory, err := buildInstruction(nil, req, modelbank.SearchDecision{}, nil)
	if err != nil {
		t.Fatalf("buildInstruction: %v", err)
	}
	if strings.Contains(withoutMemory, "## Memory") {
		t.Fatalf("instruction should omit memory policy when the tool is unavailable:\n%s", withoutMemory)
	}

	withMemory, err := buildInstruction(nil, req, modelbank.SearchDecision{}, map[string]bool{"memory": true})
	if err != nil {
		t.Fatalf("buildInstruction: %v", err)
	}
	if !strings.Contains(withMemory, "## Memory") || !strings.Contains(withMemory, "Mounted tools: memory.") {
		t.Fatalf("instruction should keep memory policy when mounted:\n%s", withMemory)
	}
}

func TestDefaultSystemPromptTemplateGuidesMemoryWrites(t *testing.T) {
	got := defaultSystemPromptTemplate()
	for _, want := range []string{"## Memory", `action="add"`, `action="replace"`, `action="remove"`, "you are lying", "Never say", "Transient conversation details", "zero for generic technical questions"} {
		if !strings.Contains(got, want) {
			t.Fatalf("default prompt missing memory guidance %q:\n%s", want, got)
		}
	}
}

func TestDefaultSystemPromptTemplateBansMemoryAttributionPhrases(t *testing.T) {
	got := defaultSystemPromptTemplate()
	for _, forbidden := range []string{"I remember", "According to my memories", "Based on what I know about you", "I recall"} {
		if !strings.Contains(got, forbidden) {
			t.Fatalf("default prompt missing forbidden memory phrase %q:\n%s", forbidden, got)
		}
	}
}

func TestDefaultSystemPromptTemplateTreatsAvailableReadToolsAsReady(t *testing.T) {
	got := defaultSystemPromptTemplate()
	for _, want := range []string{"Read-only and information-gathering tools are ready to use without asking", "do not suggest the user enable a tool that is already available"} {
		if !strings.Contains(got, want) {
			t.Fatalf("default prompt missing available-tool proactivity rule %q:\n%s", want, got)
		}
	}
}

func TestDefaultSystemPromptTemplateKeepsChineseDefault(t *testing.T) {
	got := defaultSystemPromptTemplate()
	for _, want := range []string{"Default to Chinese", "Never switch languages mid-conversation", "## Output Format"} {
		if !strings.Contains(got, want) {
			t.Fatalf("default prompt missing language/output rule %q:\n%s", want, got)
		}
	}
}

// TestDefaultSystemPromptTemplate_MatchesConfigDefault 守护提示词模板的单一权威来源：
// runtime 默认值（defaultSystemPromptTemplate）必须与 Admin 可编辑配置默认值逐字符相等。
// 任一处改动而忘了另一处，此测试即失败。
func TestDefaultSystemPromptTemplate_MatchesConfigDefault(t *testing.T) {
	var configDefault string
	raw := repository.AdminEditableConfig["system_prompt_template"].Default
	if err := json.Unmarshal(raw, &configDefault); err != nil {
		t.Fatalf("config 默认值不是合法 JSON 字符串: %v", err)
	}
	if got := defaultSystemPromptTemplate(); got != configDefault {
		t.Errorf("runtime 默认模板与 config 默认值不一致\nruntime=%q\nconfig =%q", got, configDefault)
	}
}

func TestDefaultSystemPromptTemplateAvoidsSecondLevelTime(t *testing.T) {
	got := defaultSystemPromptTemplate()
	if strings.Contains(got, "{{time}}") || strings.Contains(got, "{{datetime}}") {
		t.Fatalf("default prompt should not include second-level time placeholders because they break provider prefix cache:\n%s", got)
	}
	if !strings.Contains(got, "{{date}}") {
		t.Fatalf("default prompt should still include the current date:\n%s", got)
	}
}

func TestDefaultSystemPromptTemplateGuidesWebContextReuse(t *testing.T) {
	got := defaultSystemPromptTemplate()
	for _, want := range []string{"Session Web Evidence", "Search again only", "web_extract", "goal", "Do not invent URLs", "meaningfully different queries"} {
		if !strings.Contains(got, want) {
			t.Fatalf("default prompt missing web context reuse guidance %q:\n%s", want, got)
		}
	}
}

func TestDefaultSystemPromptTemplateKeepsCurrentInfoOverviewBrief(t *testing.T) {
	got := defaultSystemPromptTemplate()
	for _, want := range []string{"When in doubt, search", "Detailed search triggers and hygiene live in the web_search and web_extract tool descriptions"} {
		if !strings.Contains(got, want) {
			t.Fatalf("default prompt missing concise current-info guidance %q:\n%s", want, got)
		}
	}
}

func TestDefaultSystemPromptTemplateGuidesFileAndSkillTools(t *testing.T) {
	got := defaultSystemPromptTemplate()
	for _, want := range []string{
		"Do not infer document contents from filenames alone",
		"Reading the relevant SKILL.md is a required first step",
		"hard-won trial-and-error",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("default prompt missing file/skill guidance %q:\n%s", want, got)
		}
	}
}
