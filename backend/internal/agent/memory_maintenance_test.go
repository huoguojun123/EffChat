package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	sessionmemory "github.com/huoguojun123/effchat/internal/memory"
)

type memoryMaintenanceStreamModel struct {
	chunks         []*schema.Message
	streamErr      error
	generateCalled bool
}

func (m *memoryMaintenanceStreamModel) Generate(_ context.Context, _ []*schema.Message, _ ...einoModel.Option) (*schema.Message, error) {
	m.generateCalled = true
	return schema.AssistantMessage("generate should not be used", nil), nil
}

func (m *memoryMaintenanceStreamModel) Stream(_ context.Context, _ []*schema.Message, _ ...einoModel.Option) (*schema.StreamReader[*schema.Message], error) {
	if m.streamErr != nil {
		return nil, m.streamErr
	}
	return schema.StreamReaderFromArray(m.chunks), nil
}

func (m *memoryMaintenanceStreamModel) WithTools(_ []*schema.ToolInfo) (einoModel.ToolCallingChatModel, error) {
	return m, nil
}

func TestShouldRunMemoryMaintenance(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"explicit remember", "记住，我偏好中文沟通", true},
		{"project decision", "这个项目当前目标是 0.1.0 预发布", true},
		{"english preference", "I prefer concise answers.", true},
		{"natural learning state", "我最大的问题不是不会学，是一看到任务很多就想逃。", true},
		{"natural correction", "刚刚说组成原理最怕，但想了想，其实是计组里的存储系统最怕。", true},
		{"trivial activity still delegated", "我在吃饭，等会儿再说", true},
		{"later but trivial", "下次再聊吧", false},
		{"generic question delegated", "解释一下这个错误", true},
		{"search result delegated", "帮我搜索一下今天的新闻", true},
		{"memory read-only summary", "请根据会话记忆总结我的项目偏好", false},
		{"explicit decision update", "你刚才说错了，请更新这个决策：并发超限直接拒绝", true},
		{"future-facing preference", "以后如果我问你吃什么，你可以优先推荐清淡方便的选择", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShouldRunMemoryMaintenance(tt.text); got != tt.want {
				t.Fatalf("ShouldRunMemoryMaintenance(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func TestAutomaticMemoryClaimSerializesPerSession(t *testing.T) {
	agent := &EinoAgent{}
	if !agent.tryClaimAutomaticMemory(17) {
		t.Fatal("first automatic memory claim should succeed")
	}
	if agent.tryClaimAutomaticMemory(17) {
		t.Fatal("second automatic memory claim for the same session should fail")
	}
	if !agent.tryClaimAutomaticMemory(18) {
		t.Fatal("a different session should claim independently")
	}
	agent.memoryAutoClaims.Delete(int64(17))
	if !agent.tryClaimAutomaticMemory(17) {
		t.Fatal("released automatic memory claim should be reusable")
	}
}

func TestMemoryDrainRejectsNewBackgroundTasks(t *testing.T) {
	agent := &EinoAgent{}
	if !agent.startMemoryBackgroundTask() {
		t.Fatal("memory task should start before drain")
	}
	drained := make(chan bool, 1)
	go func() {
		drained <- agent.DrainMemoryTasks(context.Background())
	}()
	select {
	case <-drained:
		t.Fatal("memory drain returned before the active task completed")
	case <-time.After(10 * time.Millisecond):
	}
	agent.memoryTasks.Done()
	if !<-drained {
		t.Fatal("memory tasks should drain after the active task completes")
	}
	if agent.startMemoryBackgroundTask() {
		t.Fatal("memory task should be rejected after drain begins")
	}
}

func TestMemoryMaintenanceInstructionRoutesFictionalPersona(t *testing.T) {
	instruction := buildMemoryMaintenanceInstruction(sessionmemory.DefaultLimits())
	if !strings.Contains(instruction, "fictional") || !strings.Contains(instruction, "Project Context") || !strings.Contains(instruction, "not User Background") {
		t.Fatalf("memory maintenance instruction should route fictional persona into project context:\n%s", instruction)
	}
}

func TestMemoryMaintenanceInstructionUsesConfiguredLimit(t *testing.T) {
	instruction := buildMemoryMaintenanceInstruction(sessionmemory.NormalizeLimits(8000, 0))
	if !strings.Contains(instruction, "under 8000 characters") || !strings.Contains(instruction, "near 6400 characters") {
		t.Fatalf("instruction should include configured limits:\n%s", instruction)
	}
}

func TestMemoryMaintenanceInstructionCarriesMatureMemoryRules(t *testing.T) {
	instruction := buildMemoryMaintenanceInstruction(sessionmemory.DefaultLimits())
	for _, want := range []string{
		"like a human colleague",
		"Selectively maintain memory based on relevance",
		"Rewrite for density and editability",
		"Never invent calendar dates",
		"discourages honest feedback",
		"unsafe, unhealthy, or harmful behavior",
		"Do not write bullets that say",
		"recent conversation window as the source of new facts",
		"most recent user correction or clarification has highest priority",
	} {
		if !strings.Contains(instruction, want) {
			t.Fatalf("memory maintenance instruction missing mature memory rule %q:\n%s", want, instruction)
		}
	}
}

func TestMemoryMaintenanceInstructionRequiresChineseVisibleContent(t *testing.T) {
	instruction := buildMemoryMaintenanceInstruction(sessionmemory.DefaultLimits())
	for _, want := range []string{"Simplified Chinese", "Never create placeholder bullets", "translate its natural-language prose"} {
		if !strings.Contains(instruction, want) {
			t.Fatalf("memory maintenance instruction missing %q:\n%s", want, instruction)
		}
	}
}

func TestContainsHan(t *testing.T) {
	if !containsHan("已更新 GPT-5.6 记忆") {
		t.Fatal("Chinese summary should contain Han characters")
	}
	if containsHan("updated memory") {
		t.Fatal("English summary should not contain Han characters")
	}
}

func TestMemoryMaintenanceSummaryFallsBackByAction(t *testing.T) {
	if got := memoryMaintenanceSummary("compact", "before", "after"); got != "已整理会话记忆" {
		t.Fatalf("compact summary = %q", got)
	}
	if got := memoryMaintenanceSummary("auto", "", "## User Preferences\n- 使用中文。"); !strings.Contains(got, "已创建") {
		t.Fatalf("update summary = %q", got)
	}
}

func TestValidateMemoryMaintenanceLanguage(t *testing.T) {
	chinese, err := sessionmemory.Parse("## User Preferences\n- 回答先给结论，并保留 GPT-5.6、Go 和 API 名称。")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateMemoryMaintenanceLanguage(chinese); err != nil {
		t.Fatalf("Chinese memory should pass: %v", err)
	}

	english, err := sessionmemory.Parse("## User Preferences\n- User prefers concise answers with clear tradeoffs.")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateMemoryMaintenanceLanguage(english); err == nil {
		t.Fatal("English prose should be rejected")
	}

	technical, err := sessionmemory.Parse("## Project Context\n- GPT-5.6 / API")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateMemoryMaintenanceLanguage(technical); err != nil {
		t.Fatalf("technical identifiers should pass: %v", err)
	}
}

func TestBuildMemoryMaintenancePromptUsesRecentWindowForCorrections(t *testing.T) {
	calendar := memoryMaintenanceCalendar{
		CurrentDate:  "2026-07-10",
		CurrentWeek:  "2026-W28",
		CurrentMonth: "2026-07",
		Timezone:     "Asia/Shanghai",
	}
	prompt := buildMemoryMaintenancePrompt("", MemoryMaintenanceRequest{
		UserText: "我的意思是每周训练公式推导和错题复盘。",
		ContextText: strings.Join([]string{
			"user: 今天做了十道多元微分的题，还是不熟。",
			"assistant: 可以每周训练一次多元微分。",
			"user: 我的意思是每周训练公式推导和错题复盘，不是只练多元微分。",
		}, "\n\n"),
	}, "auto", calendar)
	for _, want := range []string{
		"Recent conversation window",
		"不是只练多元微分",
		"Prefer the newest user correction",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("maintenance prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestIsExplicitMemoryMaintenanceRequest(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"remember zh", "请把这条明确记住：我喜欢安静密集的 UI", true},
		{"forget zh", "忘掉刚才那条临时信息", true},
		{"do not remember zh", "这个不要记：验证码是 123456", true},
		{"update decision zh", "你刚才说错了，请更新这个决策：不要排队", true},
		{"remember en", "Remember that I prefer Chinese replies.", true},
		{"project context not explicit", "这个项目当前目标是 0.1.0 预发布", false},
		{"generic", "解释一下这个错误", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsExplicitMemoryMaintenanceRequest(tt.text); got != tt.want {
				t.Fatalf("IsExplicitMemoryMaintenanceRequest(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func TestIsTinyChitChat(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"ok", "好的", true},
		{"later", "下次再说", true},
		{"meaningful short", "数学先学高数", false},
		{"long natural state", "我看到任务很多就想逃", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsTinyChitChat(tt.text); got != tt.want {
				t.Fatalf("IsTinyChitChat(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func TestIsMemoryReadOnlyRequest(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"summary", "请根据会话记忆总结我的项目偏好", true},
		{"answer", "现在不要翻前文，尽量只根据你当前能看到的上下文和会话记忆回答", true},
		{"plain preference", "我偏好安静密集的 UI", false},
		{"explicit update wins elsewhere", "请更新这个决策：不做排队", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsMemoryReadOnlyRequest(tt.text); got != tt.want {
				t.Fatalf("IsMemoryReadOnlyRequest(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func TestBuildMemoryMaintenancePromptExcludesAssistantOutput(t *testing.T) {
	calendar := memoryMaintenanceCalendar{
		CurrentDate:  "2026-07-09",
		CurrentWeek:  "2026-W28",
		CurrentMonth: "2026-07",
		Timezone:     "Asia/Shanghai",
	}
	prompt := buildMemoryMaintenancePrompt("## Decisions\n- Keep current facts.", MemoryMaintenanceRequest{
		UserText:      "请根据会话记忆总结我的项目偏好",
		AssistantText: "Provider 硬编码 OpenAI-compatible，持久化从 JSON 文件起步。",
	}, "auto", calendar)
	if strings.Contains(prompt, "Provider 硬编码") || strings.Contains(prompt, "Assistant response") {
		t.Fatalf("maintenance prompt should not include assistant-originated content:\n%s", prompt)
	}
}

func TestBuildMemoryMaintenancePromptIncludesCurrentDate(t *testing.T) {
	calendar := memoryMaintenanceCalendar{
		CurrentDate:  "2026-07-09",
		CurrentWeek:  "2026-W28",
		CurrentMonth: "2026-07",
		Timezone:     "Asia/Shanghai",
	}
	prompt := buildMemoryMaintenancePrompt("", MemoryMaintenanceRequest{UserText: "明天继续按这个计划来。"}, "compact", calendar)
	if !strings.Contains(prompt, "Current date: 2026-07-09") {
		t.Fatalf("maintenance prompt should include current date:\n%s", prompt)
	}
	for _, want := range []string{"Current week: 2026-W28", "Current month: 2026-07", "Timezone: Asia/Shanghai"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("maintenance prompt should include %q:\n%s", want, prompt)
		}
	}
}

func TestMemoryMaintenanceCalendarUsesRequestTimezone(t *testing.T) {
	now := time.Date(2026, time.July, 12, 16, 30, 0, 0, time.UTC)
	calendar := memoryMaintenanceCalendarAt(now, userLocation([]byte(`{"timezone":"Asia/Shanghai"}`)))
	if calendar.CurrentDate != "2026-07-13" || calendar.CurrentWeek != "2026-W29" || calendar.Timezone != "Asia/Shanghai" {
		t.Fatalf("Shanghai calendar = %+v", calendar)
	}

	utc := memoryMaintenanceCalendarAt(now, userLocation([]byte(`{"timezone":"UTC"}`)))
	if utc.CurrentDate != "2026-07-12" || utc.Timezone != "UTC" {
		t.Fatalf("UTC calendar = %+v", utc)
	}
}

func TestValidateMemoryMaintenanceDatesRejectsInventedDates(t *testing.T) {
	err := validateMemoryMaintenanceDates(
		"## Current Progress\n- Current: 明天继续。",
		"明天继续按这个计划来。",
		"## Current Progress\n- Current: 已产出明日计划（2025-06-18）。",
		"2026-07-09",
	)
	if err == nil || !strings.Contains(err.Error(), "2025-06-18") {
		t.Fatalf("expected invented date to be rejected, got %v", err)
	}
}

func TestValidateMemoryMaintenanceDatesAllowsGroundedDates(t *testing.T) {
	if err := validateMemoryMaintenanceDates(
		"## Current Progress\n- 2026-07-08: 旧进度。",
		"2026-07-10 开始新阶段。",
		"## Current Progress\n- 2026-07-08: 旧进度。\n- 2026-07-09: 今日整理。\n- 2026-07-10: 开始新阶段。",
		"2026-07-09",
	); err != nil {
		t.Fatalf("grounded dates should pass: %v", err)
	}
}

func TestParseMemoryMaintenanceDecision(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "fenced json", raw: "```json\n{\"action\":\"none\"}\n```", want: "none"},
		{name: "trailing text", raw: "{\"action\":\"none\"}\n我已经整理好了。", want: "none"},
		{name: "trailing brace", raw: "{\"action\":\"none\"}\n}", want: "none"},
		{name: "non json", raw: "no memory update", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseMemoryMaintenanceDecision(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected parse error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got.Action != tt.want {
				t.Fatalf("action = %q, want %q", got.Action, tt.want)
			}
		})
	}
}

func TestGenerateMemoryMaintenanceTextStreamsChunks(t *testing.T) {
	model := &memoryMaintenanceStreamModel{chunks: []*schema.Message{
		{Role: schema.Assistant, Content: `{"action":"`},
		{Role: schema.Assistant, Content: `none"}`},
	}}
	got, err := generateMemoryMaintenanceText(context.Background(), model, nil)
	if err != nil {
		t.Fatalf("generateMemoryMaintenanceText returned error: %v", err)
	}
	if got != `{"action":"none"}` {
		t.Fatalf("streamed text = %q", got)
	}
	if model.generateCalled {
		t.Fatal("memory maintenance should use Stream, not Generate")
	}
}

func TestGenerateMemoryMaintenanceTextRejectsEmptyStream(t *testing.T) {
	_, err := generateMemoryMaintenanceText(context.Background(), &memoryMaintenanceStreamModel{}, nil)
	if err == nil || !strings.Contains(err.Error(), "empty output") {
		t.Fatalf("expected empty output error, got %v", err)
	}
}

func TestGenerateMemoryMaintenanceTextReturnsStreamError(t *testing.T) {
	want := errors.New("stream failed")
	_, err := generateMemoryMaintenanceText(context.Background(), &memoryMaintenanceStreamModel{streamErr: want}, nil)
	if !errors.Is(err, want) {
		t.Fatalf("expected stream error %v, got %v", want, err)
	}
}

func TestValidateMemoryMaintenanceUpdate(t *testing.T) {
	before := "## User Preferences\n- 默认中文沟通。\n\n## Project Context\n- FChat 是个人 AI workbench。"
	if err := validateMemoryMaintenanceUpdate(before, "", false); err == nil {
		t.Fatal("expected empty update to be rejected")
	}
	if err := validateMemoryMaintenanceUpdate(before, "", true); err != nil {
		t.Fatalf("explicit deletion should allow empty memory: %v", err)
	}
	after := "## Current Progress\n- 嗯"
	if err := validateMemoryMaintenanceUpdate(before, after, false); err == nil {
		t.Fatal("expected suspicious single-item update to be rejected")
	}
}
