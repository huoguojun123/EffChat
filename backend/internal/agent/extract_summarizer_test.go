package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/testutil"
	internaltool "github.com/huoguojun123/EffChat/internal/tool"
	modelusage "github.com/huoguojun123/EffChat/internal/usage"
)

func TestExtractSummaryErrorMessageIsStable(t *testing.T) {
	if got := extractSummaryErrorMessage("first_output_timeout"); got != "网页内容提炼等待首个输出超时" {
		t.Fatalf("first-output timeout message = %q", got)
	}
	if got := extractSummaryErrorMessage("timeout"); got != "网页内容提炼超时" {
		t.Fatalf("timeout message = %q", got)
	}
	if got := extractSummaryErrorMessage("model_error"); got != "网页内容提炼失败" {
		t.Fatalf("model error message = %q", got)
	}
}

type captureSummaryChatModel struct {
	messages       []*schema.Message
	calls          int
	generateCalled bool
	chunks         []*schema.Message
}

func (m *captureSummaryChatModel) Generate(_ context.Context, _ []*schema.Message, _ ...einoModel.Option) (*schema.Message, error) {
	m.generateCalled = true
	return nil, fmt.Errorf("Generate must not be called")
}

func (m *captureSummaryChatModel) Stream(_ context.Context, messages []*schema.Message, _ ...einoModel.Option) (*schema.StreamReader[*schema.Message], error) {
	m.messages = messages
	m.calls++
	if m.chunks != nil {
		return schema.StreamReaderFromArray(m.chunks), nil
	}
	return schema.StreamReaderFromArray([]*schema.Message{
		{Role: schema.Assistant, Content: "提炼"},
		{Role: schema.Assistant, Content: "结果"},
	}), nil
}

func TestExtractSummarizerStripsLegacyInlineThinking(t *testing.T) {
	model := &captureSummaryChatModel{chunks: []*schema.Message{
		{Role: schema.Assistant, Content: "<think>内部推理"},
		{Role: schema.Assistant, Content: "过程</think>页面标题：RFC 9110"},
	}}
	summarizer := &extractSummarizer{chatModel: model}

	got, err := summarizer.Summarize(t.Context(), "提取标题", "RFC", "page text", "summary")
	if err != nil {
		t.Fatalf("Summarize returned error: %v", err)
	}
	if got != "页面标题：RFC 9110" {
		t.Fatalf("summary = %q, want legacy thinking removed", got)
	}
}

func (m *captureSummaryChatModel) WithTools(_ []*schema.ToolInfo) (einoModel.ToolCallingChatModel, error) {
	return m, nil
}

func TestExtractSummaryInstructionKeepsEvidenceShape(t *testing.T) {
	for _, want := range []string{
		"web_extract",
		"session web evidence",
		"extraction goal",
		"facts, numbers, dates",
		"own words",
		"Do not invent",
		"与目标相关性弱",
		"source material",
		"Do not trade away material qualifications",
	} {
		if !strings.Contains(extractSummaryInstruction, want) {
			t.Fatalf("extract summary instruction missing %q:\n%s", want, extractSummaryInstruction)
		}
	}
}

func TestExtractSummarizerBuildsReusableEvidencePrompt(t *testing.T) {
	model := &captureSummaryChatModel{}
	summarizer := &extractSummarizer{chatModel: model}

	got, err := summarizer.Summarize(context.Background(), "判断这个服务是否适合自托管搜索", "SearXNG docs", "SearXNG is a metasearch engine.", "detailed")
	if err != nil {
		t.Fatalf("Summarize returned error: %v", err)
	}
	if got != "提炼结果" {
		t.Fatalf("summary = %q", got)
	}
	if model.generateCalled {
		t.Fatal("extract summarizer must use Stream, not Generate")
	}
	if len(model.messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(model.messages))
	}
	if model.messages[0].Role != schema.System || !strings.Contains(model.messages[0].Content, "small summarization model") {
		t.Fatalf("system prompt not injected: %#v", model.messages[0])
	}
	userPrompt := model.messages[1].Content
	for _, want := range []string{
		"Extraction goal: 判断这个服务是否适合自托管搜索",
		"Page title: SearXNG docs",
		"Requested detail: detailed",
		"Output use: this summary will be returned as a web_extract tool result",
		"session web evidence",
		"Page text:",
		"SearXNG is a metasearch engine.",
	} {
		if !strings.Contains(userPrompt, want) {
			t.Fatalf("summarizer user prompt missing %q:\n%s", want, userPrompt)
		}
	}
}

func TestExtractSummarizerSkipsModelDuringToolCooldown(t *testing.T) {
	db := testutil.OpenPostgresTestDB(t)
	user := &model.User{Username: fmt.Sprintf("extract-cooldown-%d", time.Now().UnixNano()), PasswordHash: "test", Role: "user", IsActive: true, Permissions: []byte(`{}`), Preferences: []byte(`{}`)}
	if err := repository.NewUserRepository(db).Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	session := &model.Session{UserID: user.ID, Title: "extract cooldown", ModelID: "gpt-4o", Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := repository.NewSessionRepository(db).Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}

	taskRuns := repository.NewModelTaskRunRepository(db)
	now := time.Now()
	retryAfter := now.Add(30 * time.Minute)
	if _, err := taskRuns.Record(t.Context(), repository.RecordModelTaskRunInput{
		TaskKey:    repository.ModelTaskToolExtractSummary,
		UserID:     user.ID,
		SessionID:  session.ID,
		Source:     repository.ModelTaskSourceTool,
		Status:     repository.ModelTaskStatusFailed,
		RetryAfter: &retryAfter,
		StartedAt:  now.Add(-time.Second),
		FinishedAt: now,
	}); err != nil {
		t.Fatalf("record cooldown: %v", err)
	}

	chatModel := &captureSummaryChatModel{}
	summarizer := &extractSummarizer{chatModel: chatModel, taskRuns: taskRuns}
	ctx := modelusage.WithMeta(t.Context(), modelusage.Meta{UserID: user.ID, SessionID: session.ID, RunID: "run-cooldown"})
	_, err := summarizer.Summarize(ctx, "goal", "title", "content", "summary")
	if err == nil || !internaltool.IsRefinementReason(err, internaltool.RefinementCooldown) {
		t.Fatalf("Summarize() error = %v, want cooldown", err)
	}
	if chatModel.calls != 0 {
		t.Fatalf("model calls = %d, want 0 during cooldown", chatModel.calls)
	}
}

func TestExtractSummarizerRetriesWhenRuntimeVersionChanges(t *testing.T) {
	db := testutil.OpenPostgresTestDB(t)
	user := &model.User{Username: fmt.Sprintf("extract-runtime-%d", time.Now().UnixNano()), PasswordHash: "test", Role: "user", IsActive: true, Permissions: []byte(`{}`), Preferences: []byte(`{}`)}
	if err := repository.NewUserRepository(db).Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	session := &model.Session{UserID: user.ID, Title: "extract runtime", ModelID: "gpt-4o", Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := repository.NewSessionRepository(db).Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}

	taskRuns := repository.NewModelTaskRunRepository(db)
	now := time.Now()
	retryAfter := now.Add(30 * time.Minute)
	if _, err := taskRuns.Record(t.Context(), repository.RecordModelTaskRunInput{
		TaskKey: repository.ModelTaskToolExtractSummary, UserID: user.ID, SessionID: session.ID,
		Source: repository.ModelTaskSourceTool, Status: repository.ModelTaskStatusFailed,
		TargetType: "web_extract", TargetID: "previous-runtime", RetryAfter: &retryAfter,
		StartedAt: now.Add(-time.Second), FinishedAt: now,
	}); err != nil {
		t.Fatalf("record cooldown: %v", err)
	}

	chatModel := &captureSummaryChatModel{}
	summarizer := &extractSummarizer{chatModel: chatModel, taskRuns: taskRuns, runtimeVersion: "next-runtime"}
	ctx := modelusage.WithMeta(t.Context(), modelusage.Meta{UserID: user.ID, SessionID: session.ID, RunID: "run-runtime"})
	if _, err := summarizer.Summarize(ctx, "goal", "title", "content", "summary"); err != nil {
		t.Fatalf("Summarize returned error after runtime change: %v", err)
	}
	if chatModel.calls != 1 {
		t.Fatalf("model calls = %d, want one probe after runtime change", chatModel.calls)
	}
}
