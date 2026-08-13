package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

const (
	defaultAgentMaxIterations = 20
	defaultToolRoundLimit     = 8
	defaultToolCallLimit      = 24
	minToolContextTokens      = 2048
	maxToolContextTokens      = 32000
	minToolResultGrantTokens  = 512
)

type toolBudgetMiddleware struct {
	*adk.BaseChatModelAgentMiddleware
	sessionID int64
	modelID   string
	provider  string
	maxRounds int
	maxCalls  int

	maxResultTokens  int
	maxContextTokens int
	maxSkillTokens   int

	initialized bool
	baseline    int
	noteAdded   bool
	forceFinal  atomic.Bool
	actualCalls atomic.Int64

	budgetMu        sync.Mutex
	contextUsed     int
	contextReserved int
	skillUsed       int
	skillReserved   int
	skillBlocked    bool
}

type toolContextGrant struct {
	tokens int
	skill  bool
}

type agenticToolBudgetMiddleware struct {
	*adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]
	budget *toolBudgetMiddleware
}

func newAgenticToolBudgetMiddleware(budget *toolBudgetMiddleware) *agenticToolBudgetMiddleware {
	return &agenticToolBudgetMiddleware{
		TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]{},
		budget:                            budget,
	}
}

func (m *agenticToolBudgetMiddleware) BeforeModelRewriteState(ctx context.Context, state *adk.TypedChatModelAgentState[*schema.AgenticMessage], _ *adk.TypedModelContext[*schema.AgenticMessage]) (context.Context, *adk.TypedChatModelAgentState[*schema.AgenticMessage], error) {
	if m == nil || m.budget == nil || state == nil {
		return ctx, state, nil
	}
	b := m.budget
	if !b.initialized {
		b.initialized = true
		b.baseline = len(state.Messages)
	}
	rounds, calls := countAgenticGeneratedToolUse(state.Messages, b.baseline)
	actualCalls := int(b.actualCalls.Load())
	if rounds >= b.maxRounds || calls >= b.maxCalls || actualCalls >= b.maxCalls || b.forceFinal.Load() {
		b.forceFinal.Store(true)
		state.ToolInfos = nil
		state.DeferredToolInfos = nil
		if !b.noteAdded {
			b.noteAdded = true
			state.Messages = append(state.Messages, schema.UserAgenticMessage(toolBudgetFinalNotice(rounds, maxInt(calls, actualCalls), b.maxRounds, b.maxCalls)))
			log.Printf("[agent] agentic tool budget exhausted, forcing final answer: session=%d provider=%s model=%s rounds=%d calls=%d actual_calls=%d max_rounds=%d max_calls=%d",
				b.sessionID, b.provider, b.modelID, rounds, calls, actualCalls, b.maxRounds, b.maxCalls)
		}
	}
	return ctx, state, nil
}

func countAgenticGeneratedToolUse(messages []*schema.AgenticMessage, baseline int) (rounds, calls int) {
	if baseline < 0 {
		baseline = 0
	}
	if baseline > len(messages) {
		baseline = len(messages)
	}
	for _, message := range messages[baseline:] {
		if message == nil || message.Role != schema.AgenticRoleTypeAssistant {
			continue
		}
		messageCalls := 0
		for _, block := range message.ContentBlocks {
			if block != nil && block.FunctionToolCall != nil {
				messageCalls++
			}
		}
		if messageCalls > 0 {
			rounds++
			calls += messageCalls
		}
	}
	return rounds, calls
}

// newToolBudgetMiddleware 给单轮 Agent run 加一层工具预算保险。
//
// Eino ADK 的 MaxIterations 能防止无限循环，但报错方式是 "exceeds max
// iterations"，用户只能看到失败。本中间件更早介入：达到工具轮次/次数上限后
// 移除可用工具，并向模型追加一条用户态系统提示，要求它基于已拿到的结果转入正文。
//
// 这里同时维护两种计数：
//   - state.Messages 中新增 assistant tool_calls，用于判断模型已经发起了几轮工具调用；
//   - WrapInvokableToolCall 中的 actualCalls，用于判断工具实际执行了几次。
//
// 保留双计数是为了覆盖 parallel tool calls 和异常重试：有些执行路径里 state 尚未完整
// 回写，但工具 wrapper 已经被调用；也有些历史消息里有 tool_calls，但本轮实际未执行。
func newToolBudgetMiddleware(req *ChatRequest) *toolBudgetMiddleware {
	contextWindow := 128000
	var sessionID int64
	var modelID, provider string
	if req != nil && req.ContextWindow > 0 {
		contextWindow = req.ContextWindow
	}
	if req != nil {
		sessionID = req.SessionID
		modelID = req.ModelID
		provider = req.Provider
	}
	contextTokens := contextWindow / 8
	minimum := minInt(minToolContextTokens, contextWindow/4)
	if contextTokens < minimum {
		contextTokens = minimum
	}
	if contextTokens > maxToolContextTokens {
		contextTokens = maxToolContextTokens
	}
	resultTokens := contextTokens / 4
	usableTokens := contextTokens - terminalToolReserveTokens(contextTokens)
	if resultTokens < minToolResultGrantTokens && usableTokens >= minToolResultGrantTokens {
		resultTokens = minToolResultGrantTokens
	}
	if resultTokens > 8000 {
		resultTokens = 8000
	}
	skillTokens := contextTokens / 2
	if skillTokens > 12000 {
		skillTokens = 12000
	}
	return &toolBudgetMiddleware{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		sessionID:                    sessionID,
		modelID:                      modelID,
		provider:                     provider,
		maxRounds:                    defaultToolRoundLimit,
		maxCalls:                     defaultToolCallLimit,
		maxResultTokens:              resultTokens,
		maxContextTokens:             contextTokens,
		maxSkillTokens:               skillTokens,
	}
}

func (m *toolBudgetMiddleware) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	if state == nil {
		return ctx, state, nil
	}
	if !m.initialized {
		m.initialized = true
		m.baseline = len(state.Messages)
	}

	rounds, calls := countGeneratedToolUse(state.Messages, m.baseline)
	actualCalls := int(m.actualCalls.Load())
	if rounds >= m.maxRounds || calls >= m.maxCalls || actualCalls >= m.maxCalls || m.forceFinal.Load() {
		m.forceFinal.Store(true)
		state.ToolInfos = nil
		state.DeferredToolInfos = nil
		if !m.noteAdded {
			m.noteAdded = true
			state.Messages = append(state.Messages, schema.UserMessage(toolBudgetFinalNotice(rounds, maxInt(calls, actualCalls), m.maxRounds, m.maxCalls)))
			log.Printf("[agent] tool budget exhausted, forcing final answer: session=%d provider=%s model=%s rounds=%d calls=%d actual_calls=%d max_rounds=%d max_calls=%d",
				m.sessionID, m.provider, m.modelID, rounds, calls, actualCalls, m.maxRounds, m.maxCalls)
		}
	}
	return ctx, state, nil
}

func (m *toolBudgetMiddleware) WrapInvokableToolCall(_ context.Context, endpoint adk.InvokableToolCallEndpoint, tCtx *adk.ToolContext) (adk.InvokableToolCallEndpoint, error) {
	return endpoint, nil
}

func (m *toolBudgetMiddleware) prepareToolCall(toolName, arguments string) (string, toolContextGrant, string) {
	if m == nil {
		return arguments, toolContextGrant{}, ""
	}
	m.budgetMu.Lock()
	defer m.budgetMu.Unlock()

	if m.forceFinal.Load() {
		return arguments, toolContextGrant{}, toolBudgetBlockedResult(toolName, "The tool-call budget is exhausted. Stop calling tools and produce the final answer from the evidence already available.")
	}
	skill := isSkillTool(toolName)
	if skill && m.skillBlocked {
		return arguments, toolContextGrant{}, toolContextBudgetBlockedResult(toolName, "skill_context_budget_exhausted", "The skill context budget is exhausted. Some instructions may remain unread; do not claim complete skill compliance.")
	}
	if int(m.actualCalls.Load()) >= m.maxCalls {
		m.forceFinal.Store(true)
		log.Printf("[agent] tool call limit reached, blocking tool: session=%d provider=%s model=%s tool=%s actual_calls=%d max_calls=%d",
			m.sessionID, m.provider, m.modelID, toolName, m.actualCalls.Load(), m.maxCalls)
		return arguments, toolContextGrant{}, toolBudgetBlockedResult(toolName, "The tool-call limit has been reached. Stop calling tools and summarize the available results.")
	}

	limit := m.maxResultTokens
	usableMaximum := m.maxContextTokens - terminalToolReserveTokens(m.maxContextTokens)
	if remaining := usableMaximum - m.contextUsed - m.contextReserved; remaining < limit {
		limit = remaining
	}
	if skill {
		if remaining := m.maxSkillTokens - m.skillUsed - m.skillReserved; remaining < limit {
			limit = remaining
		}
	}
	if limit < minToolResultGrantTokens {
		if skill && m.skillReserved == 0 {
			m.skillBlocked = true
			return arguments, toolContextGrant{}, toolContextBudgetBlockedResult(toolName, "skill_context_budget_exhausted", "The skill context budget is exhausted. Some instructions may remain unread; do not claim complete skill compliance.")
		}
		if m.contextReserved == 0 {
			m.forceFinal.Store(true)
			return arguments, toolContextGrant{}, toolContextBudgetBlockedResult(toolName, "tool_context_budget_exhausted", "The tool context budget is exhausted. Produce the final answer from the evidence already available.")
		}
		return arguments, toolContextGrant{}, toolContextBudgetBlockedResult(toolName, "tool_context_budget_pending", "Parallel tool calls are using the remaining context budget. Continue with results already available.")
	}

	m.contextReserved += limit
	if skill {
		m.skillReserved += limit
	}
	m.actualCalls.Add(1)
	return boundPagedToolArguments(toolName, arguments, limit), toolContextGrant{tokens: limit, skill: skill}, ""
}

func (m *toolBudgetMiddleware) cancelToolGrant(grant toolContextGrant, releaseCall bool) {
	if m == nil || grant.tokens <= 0 {
		return
	}
	m.budgetMu.Lock()
	defer m.budgetMu.Unlock()
	m.contextReserved = maxInt(m.contextReserved-grant.tokens, 0)
	if grant.skill {
		m.skillReserved = maxInt(m.skillReserved-grant.tokens, 0)
	}
	if releaseCall {
		m.actualCalls.Add(-1)
	}
}

func (m *toolBudgetMiddleware) accountUnreservedResult(toolName, result string) string {
	if m == nil || strings.TrimSpace(result) == "" {
		return result
	}
	m.budgetMu.Lock()
	defer m.budgetMu.Unlock()
	remaining := m.maxContextTokens - m.contextUsed - m.contextReserved
	if remaining <= 0 {
		return ""
	}
	perNotice := maxInt(terminalToolReserveTokens(m.maxContextTokens)/maxInt(m.maxCalls, 1), 8)
	limit := minInt(remaining, perNotice)
	bounded := boundTerminalToolResult(result, limit)
	m.contextUsed += estimateTextTokens(bounded)
	if m.maxContextTokens-m.contextUsed < minToolResultGrantTokens && m.contextReserved == 0 {
		m.forceFinal.Store(true)
	}
	return bounded
}

func terminalToolReserveTokens(maximum int) int {
	if maximum <= 0 {
		return 0
	}
	reserve := minInt(576, maximum/4)
	if maximum-reserve < minToolResultGrantTokens {
		return maximum
	}
	return reserve
}

func boundTerminalToolResult(result string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if estimateTextTokens(result) <= limit {
		return result
	}
	var original map[string]interface{}
	_ = json.Unmarshal([]byte(result), &original)
	payload := map[string]interface{}{"ok": false}
	for _, key := range []string{"reason", "code", "tool"} {
		if value, exists := original[key]; exists {
			payload[key] = value
		}
	}
	data, _ := json.Marshal(payload)
	if estimateTextTokens(string(data)) <= limit {
		return string(data)
	}
	minimal := `{"ok":false}`
	if estimateTextTokens(minimal) <= limit {
		return minimal
	}
	return "{}"
}

func (m *toolBudgetMiddleware) finishToolResult(toolName, result string, grant toolContextGrant) string {
	if m == nil || grant.tokens <= 0 {
		return result
	}
	bounded := boundToolResultToTokens(toolName, result, grant.tokens)
	consumed := estimateTextTokens(bounded)
	if consumed > grant.tokens {
		bounded = toolContextBudgetBlockedResult(toolName, "tool_result_too_large", "The tool result could not fit safely inside the remaining context budget.")
		consumed = estimateTextTokens(bounded)
	}

	m.budgetMu.Lock()
	defer m.budgetMu.Unlock()
	m.contextReserved = maxInt(m.contextReserved-grant.tokens, 0)
	m.contextUsed += consumed
	if grant.skill {
		m.skillReserved = maxInt(m.skillReserved-grant.tokens, 0)
		m.skillUsed += consumed
		if m.maxSkillTokens-m.skillUsed < minToolResultGrantTokens && m.skillReserved == 0 {
			m.skillBlocked = true
		}
	}
	if m.maxContextTokens-m.contextUsed < minToolResultGrantTokens && m.contextReserved == 0 {
		m.forceFinal.Store(true)
	}
	return bounded
}

func boundPagedToolArguments(toolName, arguments string, limit int) string {
	if toolName != "skill_read" && toolName != "file_read" && toolName != "skill_list" {
		return arguments
	}
	var input map[string]interface{}
	if json.Unmarshal([]byte(arguments), &input) != nil {
		return arguments
	}
	if input == nil {
		input = make(map[string]interface{})
	}
	if toolName == "skill_list" {
		maxItems := maxInt(limit/300, 1)
		if current, ok := jsonNumberAsInt(input["max_items"]); !ok || current <= 0 || current > maxItems {
			input["max_items"] = maxItems
		}
		input["max_chars"] = maxInt(limit, 256)
	} else {
		maxChars := maxInt(limit-200, 64)
		if current, ok := jsonNumberAsInt(input["max_chars"]); !ok || current <= 0 || current > maxChars {
			input["max_chars"] = maxChars
		}
	}
	data, err := json.Marshal(input)
	if err != nil {
		return arguments
	}
	return string(data)
}

func jsonNumberAsInt(value interface{}) (int, bool) {
	number, ok := value.(float64)
	if !ok {
		return 0, false
	}
	return int(number), true
}

func boundToolResultToTokens(toolName, result string, limit int) string {
	if estimateTextTokens(result) <= limit {
		return result
	}
	if toolName == "skill_read" || toolName == "file_read" {
		if bounded, ok := truncatePagedToolResult(result, limit); ok {
			return bounded
		}
	}
	if toolName == "skill_list" {
		if bounded, ok := truncateSkillListToolResult(result, limit); ok {
			return bounded
		}
	}
	return truncateToolResultForContext(toolName, result, estimateTextTokens(result), limit)
}

func truncatePagedToolResult(result string, limit int) (string, bool) {
	var payload map[string]interface{}
	if json.Unmarshal([]byte(result), &payload) != nil {
		return "", false
	}
	content, ok := payload["content"].(string)
	if !ok {
		return "", false
	}
	start, _ := jsonNumberAsInt(payload["start_offset"])
	runes := []rune(content)
	low, high := 0, len(runes)
	var best string
	for low <= high {
		mid := low + (high-low)/2
		payload["content"] = string(runes[:mid])
		payload["next_offset"] = start + mid
		payload["truncated"] = true
		payload["message"] = "The result was shortened to fit the current run context budget. Continue from next_offset if more text is necessary."
		data, err := json.Marshal(payload)
		if err != nil {
			return "", false
		}
		if estimateTextTokens(string(data)) <= limit {
			best = string(data)
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	return best, best != ""
}

func truncateSkillListToolResult(result string, limit int) (string, bool) {
	var payload map[string]interface{}
	if json.Unmarshal([]byte(result), &payload) != nil {
		return "", false
	}
	scope, _ := payload["scope"].(string)
	start, _ := jsonNumberAsInt(payload["start_offset"])
	encode := func() (string, bool) {
		data, err := json.Marshal(payload)
		if err != nil {
			return "", false
		}
		return string(data), estimateTextTokens(string(data)) <= limit
	}
	for {
		if encoded, fits := encode(); fits {
			return encoded, true
		}
		skills, _ := payload["skills"].([]interface{})
		if len(skills) > 1 {
			payload["skills"] = skills[:len(skills)-1]
			payload["next_offset"] = start + len(skills) - 1
			payload["has_more"] = true
			payload["truncated"] = true
			continue
		}
		if len(skills) == 1 {
			item, _ := skills[0].(map[string]interface{})
			files, _ := item["files"].([]interface{})
			if len(files) > 0 {
				item["files"] = files[:len(files)-1]
				item["files_truncated"] = true
				fileStart := 0
				if scope == "files" {
					fileStart = start
				}
				item["next_file_offset"] = fileStart + len(files) - 1
				payload["has_more"] = true
				payload["truncated"] = true
				if scope == "files" {
					payload["next_offset"] = start + len(files) - 1
				}
				continue
			}
			if _, exists := item["description"]; exists {
				delete(item, "description")
				continue
			}
			if _, exists := item["name"]; exists {
				delete(item, "name")
				continue
			}
		}
		payload["skills"] = []interface{}{}
		payload["next_offset"] = start
		payload["has_more"] = true
		payload["truncated"] = true
		payload["message"] = "Skill metadata exceeded the remaining context budget. Retry this page with the same offset."
		encoded, fits := encode()
		return encoded, fits
	}
}

func truncateToolResultForContext(toolName, result string, originalTokens, limit int) string {
	if limit < minToolResultGrantTokens {
		return toolContextBudgetBlockedResult(toolName, "tool_context_budget_exhausted", "The remaining tool context budget is exhausted.")
	}
	runes := []rune(strings.TrimSpace(result))
	ok := true
	var original map[string]interface{}
	if json.Unmarshal([]byte(result), &original) == nil {
		if value, exists := original["ok"].(bool); exists {
			ok = value
		} else if value, exists := original["error"].(string); exists && strings.TrimSpace(value) != "" {
			ok = false
		}
	}
	build := func(content string) string {
		payload := map[string]interface{}{
			"ok":                      ok,
			"tool":                    toolName,
			"truncated":               true,
			"original_context_tokens": originalTokens,
			"content":                 content,
			"message":                 "The tool result exceeded the context budget. Only a bounded excerpt is available; do not claim omitted content was read.",
		}
		data, err := json.Marshal(payload)
		if err != nil {
			return `{"ok":false,"truncated":true,"error":"tool result could not be encoded"}`
		}
		return string(data)
	}
	low, high := 0, len(runes)
	best := build("")
	if estimateTextTokens(best) > limit {
		return toolContextBudgetBlockedResult(toolName, "tool_result_too_large", "The tool result exceeded the remaining context budget.")
	}
	for low <= high {
		mid := low + (high-low)/2
		candidate := build(string(runes[:mid]))
		if estimateTextTokens(candidate) <= limit {
			best = candidate
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	return best
}

func toolContextBudgetBlockedResult(toolName, reason, message string) string {
	payload := map[string]interface{}{
		"ok":      false,
		"tool":    toolName,
		"blocked": true,
		"reason":  reason,
		"message": message,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return `{"ok":false,"blocked":true,"reason":"tool_context_budget_exhausted"}`
	}
	return string(data)
}

func isSkillTool(toolName string) bool {
	return strings.HasPrefix(strings.TrimSpace(toolName), "skill_")
}

func countGeneratedToolUse(messages []*schema.Message, baseline int) (rounds, calls int) {
	if baseline < 0 {
		baseline = 0
	}
	if baseline > len(messages) {
		baseline = len(messages)
	}
	for _, msg := range messages[baseline:] {
		if msg == nil || msg.Role != schema.Assistant || len(msg.ToolCalls) == 0 {
			continue
		}
		rounds++
		calls += len(msg.ToolCalls)
	}
	return rounds, calls
}

func toolBudgetFinalNotice(rounds, calls, maxRounds, maxCalls int) string {
	return fmt.Sprintf("System notice: the tool-call budget for this turn is exhausted (used %d rounds / %d calls; limit %d rounds / %d calls). Do not call more tools. Answer directly from the tool results and context already available. If evidence is still insufficient, state what is missing and what could be verified next.",
		rounds, calls, maxRounds, maxCalls)
}

func toolBudgetBlockedResult(toolName, message string) string {
	payload := map[string]interface{}{
		"tool":    toolName,
		"blocked": true,
		"reason":  "tool_budget_exhausted",
		"message": message,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return `{"blocked":true,"reason":"tool_budget_exhausted"}`
	}
	return string(data)
}

func appendToolBudgetInstruction(instruction string, maxRounds, maxCalls int) string {
	return instruction + fmt.Sprintf("\n\n## Tool-Call Budget\n- This turn allows at most %d tool rounds and %d total tool calls.\n- Before each tool call, decide whether it is still necessary. Once you have enough evidence, stop calling tools and answer directly.\n- When near the budget or seeing repeated tool results, stop searching/extracting, summarize the evidence, name uncertainties, and give the next verifiable direction.",
		maxRounds, maxCalls)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
