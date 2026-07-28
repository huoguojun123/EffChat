package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

func TestToolBudgetNeverExceedsSmallContextWindow(t *testing.T) {
	for _, window := range []int{1024, 4096} {
		mw := newToolBudgetMiddleware(&ChatRequest{ContextWindow: window})
		if mw.maxContextTokens > window {
			t.Fatalf("window=%d tool budget=%d exceeds model window", window, mw.maxContextTokens)
		}
		_, grant, blocked := mw.prepareToolCall("web_search", `{}`)
		if window == 1024 && !strings.Contains(blocked, `"reason":"tool_context_budget_exhausted"`) {
			t.Fatalf("tiny model should reject tools with a stable reason: %s", blocked)
		}
		if window == 4096 && (blocked != "" || grant.tokens < minToolResultGrantTokens) {
			t.Fatalf("4096-token model should allow one bounded tool result: grant=%#v blocked=%s", grant, blocked)
		}
	}
}

func TestToolBudgetMiddlewareForcesFinalWithoutCountingHistory(t *testing.T) {
	mw := &toolBudgetMiddleware{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		maxRounds:                    2,
		maxCalls:                     10,
	}
	history := []*schema.Message{
		assistantToolCall("history_call"),
		{Role: schema.Tool, ToolCallID: "history_call", Content: "old result"},
	}
	state := &adk.ChatModelAgentState{
		Messages:  history,
		ToolInfos: []*schema.ToolInfo{{Name: "web_search"}},
	}

	_, state, err := mw.BeforeModelRewriteState(context.Background(), state, nil)
	if err != nil {
		t.Fatalf("BeforeModelRewriteState first call: %v", err)
	}
	if len(state.ToolInfos) == 0 {
		t.Fatal("historical tool calls should not exhaust current run budget")
	}

	state.Messages = append(state.Messages,
		assistantToolCall("call_1"),
		&schema.Message{Role: schema.Tool, ToolCallID: "call_1", Content: "result 1"},
		assistantToolCall("call_2"),
		&schema.Message{Role: schema.Tool, ToolCallID: "call_2", Content: "result 2"},
	)
	_, state, err = mw.BeforeModelRewriteState(context.Background(), state, nil)
	if err != nil {
		t.Fatalf("BeforeModelRewriteState second call: %v", err)
	}
	if len(state.ToolInfos) != 0 {
		t.Fatalf("tool infos should be cleared after budget exhaustion, got %#v", state.ToolInfos)
	}
	if got := state.Messages[len(state.Messages)-1].Content; !strings.Contains(got, "Do not call more tools") {
		t.Fatalf("final notice missing, got: %q", got)
	}
}

func TestToolBudgetMiddlewareBlocksExcessToolCalls(t *testing.T) {
	mw := &toolBudgetMiddleware{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		maxRounds:                    8,
		maxCalls:                     1,
		maxResultTokens:              1000,
		maxContextTokens:             2000,
		maxSkillTokens:               1000,
	}
	_, grant, blocked := mw.prepareToolCall("web_search", `{}`)
	if blocked != "" || grant.tokens == 0 {
		t.Fatalf("first call should receive a budget grant: blocked=%q grant=%#v", blocked, grant)
	}
	mw.cancelToolGrant(grant, false)
	_, _, blocked = mw.prepareToolCall("web_search", `{}`)
	if !strings.Contains(blocked, `"reason":"tool_budget_exhausted"`) {
		t.Fatalf("second call should be blocked, got: %s", blocked)
	}
}

func TestToolBudgetMiddlewareBoundsSingleAndCumulativeResults(t *testing.T) {
	mw := &toolBudgetMiddleware{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		maxRounds:                    8,
		maxCalls:                     8,
		maxResultTokens:              800,
		maxContextTokens:             1600,
		maxSkillTokens:               800,
	}
	arguments, grant, blocked := mw.prepareToolCall("skill_read", `{"skill_id":"demo","path":"SKILL.md","max_chars":5000}`)
	if blocked != "" || !strings.Contains(arguments, `"max_chars":600`) {
		t.Fatalf("skill read should be bounded before execution: args=%s blocked=%s", arguments, blocked)
	}
	raw := `{"skill_id":"demo","path":"SKILL.md","start_offset":0,"next_offset":5000,"truncated":true,"content":"` + strings.Repeat("证据", 800) + `","message":"page"}`
	first := mw.finishToolResult("skill_read", raw, grant)
	if estimateTextTokens(first) > grant.tokens {
		t.Fatalf("bounded result exceeded grant: tokens=%d grant=%d", estimateTextTokens(first), grant.tokens)
	}
	var output struct {
		Content    string `json:"content"`
		NextOffset int    `json:"next_offset"`
		Truncated  bool   `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(first), &output); err != nil {
		t.Fatalf("bounded result is invalid JSON: %v", err)
	}
	if !output.Truncated || output.NextOffset != len([]rune(output.Content)) {
		t.Fatalf("paged result skipped unseen content: %#v", output)
	}
	_, _, blocked = mw.prepareToolCall("skill_read", `{}`)
	if !strings.Contains(blocked, `"reason":"skill_context_budget_exhausted"`) {
		t.Fatalf("skill cumulative budget should stop further reads: %s", blocked)
	}
}

func TestEstimateTextTokensIsConservativeForChinese(t *testing.T) {
	if got := estimateTextTokens("中文内容"); got < 4 {
		t.Fatalf("Chinese text was underestimated: %d", got)
	}
	if got := estimateTextTokens("abcdef"); got < 2 {
		t.Fatalf("ASCII text was underestimated: %d", got)
	}
}

func TestSkillListBudgetPreservesRetryCursor(t *testing.T) {
	result := `{"scope":"files","skill_id":"demo","start_offset":5,"next_offset":6,"has_more":true,"truncated":true,"skills":[{"skill_id":"demo","name":"Demo","files":[{"path":"` +
		strings.Repeat("very-long-path/", 500) + `file.md","kind":"reference","size":1}]}],"message":"page"}`
	bounded := boundToolResultToTokens("skill_list", result, 512)
	if estimateTextTokens(bounded) > 512 {
		t.Fatalf("skill_list result exceeded grant: %d", estimateTextTokens(bounded))
	}
	var output struct {
		Scope       string `json:"scope"`
		SkillID     string `json:"skill_id"`
		StartOffset int    `json:"start_offset"`
		NextOffset  int    `json:"next_offset"`
		HasMore     bool   `json:"has_more"`
	}
	if err := json.Unmarshal([]byte(bounded), &output); err != nil {
		t.Fatalf("bounded skill_list result is invalid JSON: %v", err)
	}
	if output.Scope != "files" || output.SkillID != "demo" || output.StartOffset != 5 || output.NextOffset != 5 || !output.HasMore {
		t.Fatalf("skill_list retry cursor was lost: %#v\n%s", output, bounded)
	}
}

func TestToolContextBudgetConcurrentReservationsRemainRecoverable(t *testing.T) {
	mw := &toolBudgetMiddleware{
		maxCalls:         8,
		maxResultTokens:  600,
		maxContextTokens: 1800,
		maxSkillTokens:   600,
	}
	type outcome struct {
		grant   toolContextGrant
		blocked string
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, 3)
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, grant, blocked := mw.prepareToolCall("web_search", `{}`)
			outcomes <- outcome{grant: grant, blocked: blocked}
		}()
	}
	close(start)
	wg.Wait()
	close(outcomes)

	grants := make([]toolContextGrant, 0, 2)
	blocked := 0
	for outcome := range outcomes {
		if outcome.grant.tokens > 0 {
			grants = append(grants, outcome.grant)
		} else if strings.Contains(outcome.blocked, `"reason":"tool_context_budget_pending"`) {
			blocked++
		}
	}
	if len(grants) != 2 || blocked != 1 {
		t.Fatalf("unexpected concurrent grants: grants=%d pending=%d", len(grants), blocked)
	}
	mw.budgetMu.Lock()
	if mw.contextUsed+mw.contextReserved > mw.maxContextTokens {
		t.Fatalf("budget invariant violated: used=%d reserved=%d max=%d", mw.contextUsed, mw.contextReserved, mw.maxContextTokens)
	}
	mw.budgetMu.Unlock()

	mw.finishToolResult("web_search", `{"ok":true,"content":"short"}`, grants[0])
	mw.cancelToolGrant(grants[1], false)
	_, grant, blockedResult := mw.prepareToolCall("web_search", `{}`)
	if blockedResult != "" || grant.tokens == 0 || mw.forceFinal.Load() {
		t.Fatalf("released parallel reservations should remain recoverable: grant=%#v blocked=%s", grant, blockedResult)
	}
}

func TestBlockedToolResultsStayInsideReservedTerminalBudget(t *testing.T) {
	mw := &toolBudgetMiddleware{
		maxCalls:         defaultToolCallLimit,
		maxResultTokens:  64,
		maxContextTokens: 256,
		maxSkillTokens:   64,
	}
	totalOutputTokens := 0
	for i := 0; i < defaultToolCallLimit; i++ {
		result := mw.accountUnreservedResult("web_search", toolContextBudgetBlockedResult(
			"web_search",
			"tool_context_budget_exhausted",
			"The tool context budget is exhausted. Produce the final answer from evidence already available.",
		))
		totalOutputTokens += estimateTextTokens(result)
	}
	mw.budgetMu.Lock()
	defer mw.budgetMu.Unlock()
	if totalOutputTokens > mw.maxContextTokens || mw.contextUsed+mw.contextReserved > mw.maxContextTokens {
		t.Fatalf("terminal outputs exceeded hard budget: outputs=%d used=%d reserved=%d max=%d",
			totalOutputTokens, mw.contextUsed, mw.contextReserved, mw.maxContextTokens)
	}
}

func assistantToolCall(id string) *schema.Message {
	return &schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{
			{ID: id, Type: "function", Function: schema.FunctionCall{Name: "web_search", Arguments: `{}`}},
		},
	}
}
