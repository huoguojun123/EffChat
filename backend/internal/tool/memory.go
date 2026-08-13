package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	sessionmemory "github.com/huoguojun123/EffChat/internal/memory"
)

var errMemoryChangedWhileEditing = errors.New("memory changed while editing; view memory again and retry")

// MemoryStore 是 memory 工具所需的最小持久化接口（由 repository.SessionMemoryRepository 实现）。
// 用接口而非具体类型，避免 tool 包反向依赖 repository 包。
type MemoryStore interface {
	Get(sessionID int64) (string, error)
	Set(sessionID int64, content string) error
}

type MemoryChangeStore interface {
	SetWithChange(ctx context.Context, sessionID, userID int64, content, source, action, summary string, maxChars int) error
}

type MemoryCompareAndSetStore interface {
	CompareAndSetWithChange(ctx context.Context, sessionID, userID int64, expectedBefore, content, source, action, summary string, maxChars int) (bool, error)
}

// MemoryTool 让模型在单个会话内读写一段持久笔记。
//
// 用途：跨多轮对话记住用户偏好、关键事实、任务进展等。该笔记每轮注入系统提示，
// 且不参与历史压缩，因此即使旧消息被摘要，记忆仍然完整保留。
// 作用域严格限定在当前会话（sessionID），不跨会话泄漏。
type MemoryTool struct {
	store     MemoryStore
	sessionID int64
	userID    int64
	maxChars  int
}

// MemoryToolMaxChars 记忆正文上限，防止无界增长挤占上下文。
const MemoryToolMaxChars = sessionmemory.MaxChars

func NewMemoryTool(store MemoryStore, sessionID, userID int64) *MemoryTool {
	return NewMemoryToolWithMaxChars(store, sessionID, userID, MemoryToolMaxChars)
}

func NewMemoryToolWithMaxChars(store MemoryStore, sessionID, userID int64, maxChars int) *MemoryTool {
	limits := sessionmemory.NormalizeLimits(maxChars, 0)
	return &MemoryTool{store: store, sessionID: sessionID, userID: userID, maxChars: limits.MaxChars}
}

// MemoryInput memory 工具输入。
type MemoryInput struct {
	Action     string `json:"action"`                // view/read/add/replace/remove/clear/write
	Section    string `json:"section,omitempty"`     // 固定模块 key/title；空值兼容旧调用
	Content    string `json:"content,omitempty"`     // add/replace/write 的正文；remove 可用作匹配文本
	LineNumber int    `json:"line_number,omitempty"` // replace/remove 的 1-based 行号
}

// MemoryOutput memory 工具输出。
type MemoryOutput struct {
	OK          bool         `json:"ok"`
	Message     string       `json:"message,omitempty"`
	Action      string       `json:"action,omitempty"`
	MemoryText  string       `json:"memory_text,omitempty"`
	Items       []MemoryItem `json:"items,omitempty"`        // 带行号的记忆条目，便于 replace/remove
	Section     string       `json:"section,omitempty"`      // 本次影响的 section
	LineNumber  int          `json:"line_number,omitempty"`  // 本次影响的行号
	ChangedItem string       `json:"changed_item,omitempty"` // 本次新增/替换/删除的条目
	Error       string       `json:"error,omitempty"`        // 出错说明（非致命，模型可据此重试）
}

type MemoryItem struct {
	LineNumber int    `json:"line_number"`
	Section    string `json:"section,omitempty"`
	Content    string `json:"content"`
}

func (t *MemoryTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "memory",
		Desc: "View and edit the persistent memory note for the current conversation. The note is injected into future turns and survives history compaction. " +
			"Use action=\"view\" when the user asks what is remembered, asks why memory was not used, references past preferences/work/decisions without re-explaining. Always call action=\"view\" before replace or remove to get current line numbers — never guess line numbers. " +
			"Memory is organized into fixed sections: user_background, user_preferences, project_context, current_progress, decisions, do_not_remember. " +
			"Use user_background only for real durable user background; fictional, simulated, roleplay, or imaginary persona details belong in project_context. " +
			"Use action=\"add\" with a specific section before claiming anything has been remembered when the user explicitly asks you to remember/save/use something later, states a durable preference about your behavior, shares long-lived personal or project context, makes a decision/constraint/task phase that should carry forward, or gives future-facing guidance such as 以后如果我问你...你可以... " +
			"Use action=\"replace\" to update a stale or incorrect memory item. Phrases like 请更新这个决策, 刚才说错了, 修改这条记忆, or update that decision mean you must view memory if needed and then replace/add the relevant item before replying. " +
			"Use action=\"remove\" when the user asks you to forget/delete a specific remembered item. Provide section and line_number when possible. Use action=\"clear\" only when the user explicitly asks to clear all conversation memory. " +
			"Tool output is deliberately compact: view/read returns numbered high-signal memory_text and items for editing; write operations return only ok/message/action/section/line_number/changed_item. Do not quote the JSON output to the user. " +
			"Never store exact passwords, tokens, API keys, authorization credentials, private keys, or other secret values in any section. For do_not_remember, store only the category to avoid, never the value. " +
			"CRITICAL: You cannot remember, update, or forget anything without using this tool first. If the user asks you to remember, update, or forget something and you only acknowledge conversationally, you are lying to them. " +
			"Use action=\"write\" only as a compatibility escape hatch when you must replace the whole note with a concise fixed-section Markdown note. Never use write to fix a failed replace — use view to get current line numbers, then replace again. " +
			"Do not store code structure, git history, bugs just fixed, raw tool outputs, file contents, search results, or one-off details that only matter in this turn. " +
			"Apply saved information naturally without narrating retrieval. NEVER say 记下了, 记好了, 已记录, I have remembered, noted, or any equivalent unless this tool returned success in the current turn.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"action": {
				Type:     schema.String,
				Desc:     "Memory operation. Use view/read to inspect, add to append a durable item, replace to update one item, remove/delete to forget one item, clear to remove all memory, or write to replace the whole note.",
				Required: true,
				Enum:     []string{"view", "read", "add", "replace", "remove", "delete", "clear", "write"},
			},
			"content": {
				Type:     schema.String,
				Desc:     "Durable high-signal memory text for add/replace/write, or text to match when removing without a line_number.",
				Required: false,
			},
			"section": {
				Type:     schema.String,
				Desc:     "Memory section. Required for add. Choose user_background (real durable user role, education, expertise), user_preferences (reply style, language, UI taste), project_context (repo/product/tech stack plus fictional/simulated/roleplay persona and setting), current_progress (ongoing work and milestones), decisions (constraints and agreements), or do_not_remember (topics or data classes to avoid storing).",
				Required: false,
				Enum:     []string{"user_background", "user_preferences", "project_context", "current_progress", "decisions", "do_not_remember"},
			},
			"line_number": {
				Type:     schema.Integer,
				Desc:     "1-based memory item line number from view/read output. With section, it is section-local; without section, it is the global line number.",
				Required: false,
			},
		}),
	}, nil
}

// InvokableRun keeps user-correctable memory edits as structured results.
// Store, parse, and persistence failures return wrapped Go errors; the product
// always runs Tools through governance, which converts those errors into a
// stable public result without aborting the Agent turn.
func (t *MemoryTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	var input MemoryInput
	if err := json.Unmarshal([]byte(argumentsInJSON), &input); err != nil {
		return marshalMemoryOutput(memoryError("memory input is invalid"))
	}

	switch strings.ToLower(strings.TrimSpace(input.Action)) {
	case "read", "view":
		content, err := t.store.Get(t.sessionID)
		if err != nil {
			return "", fmt.Errorf("read memory: %w", err)
		}
		out, err := memoryViewOutput(content)
		if err != nil {
			return "", fmt.Errorf("parse stored memory: %w", err)
		}
		return marshalMemoryOutput(out)

	case "add":
		content := strings.TrimSpace(input.Content)
		if content == "" {
			return marshalMemoryOutput(memoryError("content is required for add"))
		}
		if strings.TrimSpace(input.Section) == "" {
			return marshalMemoryOutput(memoryError("section is required for add; choose user_background, user_preferences, project_context, current_progress, decisions, or do_not_remember"))
		}
		current, err := t.store.Get(t.sessionID)
		if err != nil {
			return "", fmt.Errorf("read memory before add: %w", err)
		}
		doc, err := parseMemory(current)
		if err != nil {
			return "", fmt.Errorf("parse memory before add: %w", err)
		}
		for _, item := range memoryItems(doc) {
			if strings.EqualFold(item.Content, content) {
				return marshalMemoryOutput(memoryActionOutput("add", item.Section, item.LineNumber, item.Content, fmt.Sprintf("Memory already contains item #%d.", item.LineNumber)))
			}
		}
		sectionIdx, err := sectionIndex(doc, input.Section, true)
		if err != nil {
			return marshalMemoryOutput(memoryError(err.Error()))
		}
		doc.Sections[sectionIdx].Items = append(doc.Sections[sectionIdx].Items, content)
		next, normalizedDoc, err := sessionmemory.NormalizeWithLimits(sessionmemory.Serialize(doc), sessionmemory.NormalizeLimits(t.maxChars, 0))
		if err != nil {
			return marshalMemoryOutput(memoryError(err.Error()))
		}
		if err := t.save(ctx, current, next, "update", "added memory item"); err != nil {
			return memoryMutationFailure("add memory item", err)
		}
		storedItem := normalizedDoc.Sections[sectionIdx].Items[len(normalizedDoc.Sections[sectionIdx].Items)-1]
		lineNumber := globalLineNumber(normalizedDoc, normalizedDoc.Sections[sectionIdx].Key, len(normalizedDoc.Sections[sectionIdx].Items))
		return marshalMemoryOutput(memoryActionOutput("add", normalizedDoc.Sections[sectionIdx].Key, lineNumber, storedItem, fmt.Sprintf("Added memory item #%d.", lineNumber)))

	case "replace":
		content := strings.TrimSpace(input.Content)
		if content == "" {
			return marshalMemoryOutput(memoryError("content is required for replace"))
		}
		current, err := t.store.Get(t.sessionID)
		if err != nil {
			return "", fmt.Errorf("read memory before replace: %w", err)
		}
		doc, err := parseMemory(current)
		if err != nil {
			return "", fmt.Errorf("parse memory before replace: %w", err)
		}
		target, err := memoryEntryIndex(doc, input.Section, input.LineNumber, "")
		if err != nil {
			return marshalMemoryOutput(memoryError(err.Error()))
		}
		doc.Sections[target.SectionIndex].Items[target.ItemIndex] = content
		next, normalizedDoc, err := sessionmemory.NormalizeWithLimits(sessionmemory.Serialize(doc), sessionmemory.NormalizeLimits(t.maxChars, 0))
		if err != nil {
			return marshalMemoryOutput(memoryError(err.Error()))
		}
		if err := t.save(ctx, current, next, "update", "replaced memory item"); err != nil {
			return memoryMutationFailure("replace memory item", err)
		}
		storedItem := normalizedDoc.Sections[target.SectionIndex].Items[target.ItemIndex]
		return marshalMemoryOutput(memoryActionOutput("replace", normalizedDoc.Sections[target.SectionIndex].Key, target.GlobalLine, storedItem, fmt.Sprintf("Replaced memory item #%d.", target.GlobalLine)))

	case "remove", "delete":
		current, err := t.store.Get(t.sessionID)
		if err != nil {
			return "", fmt.Errorf("read memory before remove: %w", err)
		}
		doc, err := parseMemory(current)
		if err != nil {
			return "", fmt.Errorf("parse memory before remove: %w", err)
		}
		target, err := memoryEntryIndex(doc, input.Section, input.LineNumber, input.Content)
		if err != nil {
			return marshalMemoryOutput(memoryError(err.Error()))
		}
		removedItem := target.Content
		sectionKey := doc.Sections[target.SectionIndex].Key
		items := doc.Sections[target.SectionIndex].Items
		doc.Sections[target.SectionIndex].Items = append(items[:target.ItemIndex], items[target.ItemIndex+1:]...)
		next := sessionmemory.Serialize(doc)
		if err := t.save(ctx, current, next, "update", "removed memory item"); err != nil {
			return memoryMutationFailure("remove memory item", err)
		}
		return marshalMemoryOutput(memoryActionOutput("remove", sectionKey, target.GlobalLine, removedItem, fmt.Sprintf("Removed memory item #%d.", target.GlobalLine)))

	case "clear":
		current, err := t.store.Get(t.sessionID)
		if err != nil {
			return "", fmt.Errorf("read memory before clear: %w", err)
		}
		if err := t.save(ctx, current, "", "clear", "cleared memory"); err != nil {
			return memoryMutationFailure("clear memory", err)
		}
		return marshalMemoryOutput(MemoryOutput{OK: true, Action: "clear", Message: "Memory cleared."})

	case "write":
		current, err := t.store.Get(t.sessionID)
		if err != nil {
			return "", fmt.Errorf("read memory before write: %w", err)
		}
		content := strings.TrimSpace(input.Content)
		normalized, _, err := sessionmemory.NormalizeWithLimits(content, sessionmemory.NormalizeLimits(t.maxChars, 0))
		if err != nil {
			return marshalMemoryOutput(memoryError(err.Error()))
		}
		content = normalized
		if err := t.validateContent(content); err != nil {
			return marshalMemoryOutput(memoryError(err.Error()))
		}
		if err := t.save(ctx, current, content, "update", "rewrote memory note"); err != nil {
			return memoryMutationFailure("write memory", err)
		}
		return marshalMemoryOutput(MemoryOutput{OK: true, Action: "write", Message: "Memory saved."})

	default:
		return marshalMemoryOutput(memoryError(`unknown action; use view, add, replace, remove, clear, or write`))
	}
}

func memoryMutationFailure(operation string, err error) (string, error) {
	if errors.Is(err, errMemoryChangedWhileEditing) {
		return marshalMemoryOutput(memoryError(errMemoryChangedWhileEditing.Error()))
	}
	return "", fmt.Errorf("%s: %w", operation, err)
}

func (t *MemoryTool) validateContent(content string) error {
	if len([]rune(content)) > t.maxChars {
		return fmt.Errorf("content too long (%d chars, limit %d); store only high-signal facts", len([]rune(content)), t.maxChars)
	}
	return nil
}

func (t *MemoryTool) save(ctx context.Context, expectedBefore, content, action, summary string) error {
	if compareStore, ok := t.store.(MemoryCompareAndSetStore); ok && t.userID > 0 {
		saved, err := compareStore.CompareAndSetWithChange(ctx, t.sessionID, t.userID, expectedBefore, content, "tool", action, summary, t.maxChars)
		if err != nil {
			return err
		}
		if !saved {
			return errMemoryChangedWhileEditing
		}
		return nil
	}
	if changeStore, ok := t.store.(MemoryChangeStore); ok && t.userID > 0 {
		return changeStore.SetWithChange(ctx, t.sessionID, t.userID, content, "tool", action, summary, t.maxChars)
	}
	return t.store.Set(t.sessionID, content)
}

func parseMemory(content string) (sessionmemory.Document, error) {
	return sessionmemory.Parse(content)
}

func memoryViewOutput(content string) (MemoryOutput, error) {
	// Tool results are model-visible. Redact legacy credentials here as well as at prompt assembly so
	// a view call cannot reintroduce a value that predates the current write guard.
	doc, err := parseMemory(sessionmemory.RedactSensitiveValues(content))
	if err != nil {
		return MemoryOutput{}, err
	}
	items := memoryItems(doc)
	message := "Memory loaded."
	if len(items) == 0 {
		message = "Memory is empty."
	}
	return MemoryOutput{
		OK:         true,
		Message:    message,
		Action:     "view",
		MemoryText: memoryText(items),
		Items:      items,
	}, nil
}

func memoryActionOutput(action, section string, lineNumber int, changedItem, message string) MemoryOutput {
	return MemoryOutput{
		OK:          true,
		Message:     message,
		Action:      action,
		Section:     section,
		LineNumber:  lineNumber,
		ChangedItem: changedItem,
	}
}

func memoryError(message string) MemoryOutput {
	return MemoryOutput{OK: false, Message: message, Error: message}
}

type memoryEntryRef struct {
	GlobalLine   int
	SectionIndex int
	ItemIndex    int
	Content      string
}

func memoryItems(doc sessionmemory.Document) []MemoryItem {
	out := make([]MemoryItem, 0)
	line := 1
	for _, section := range doc.Sections {
		for _, entry := range section.Items {
			out = append(out, MemoryItem{
				LineNumber: line,
				Section:    section.Key,
				Content:    entry,
			})
			line++
		}
	}
	return out
}

func memoryText(items []MemoryItem) string {
	if len(items) == 0 {
		return ""
	}
	var builder strings.Builder
	for _, item := range items {
		if builder.Len() > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(fmt.Sprintf("%d [%s] %s", item.LineNumber, item.Section, item.Content))
	}
	return builder.String()
}

func memoryEntryIndex(doc sessionmemory.Document, section string, lineNumber int, content string) (memoryEntryRef, error) {
	all := memoryItems(doc)
	if len(all) == 0 {
		return memoryEntryRef{}, fmt.Errorf("memory is empty")
	}
	sectionKey := sessionmemory.NormalizeSectionKey(section)
	if sectionKey != "" {
		idx, err := sectionIndex(doc, sectionKey, false)
		if err != nil {
			return memoryEntryRef{}, err
		}
		if lineNumber <= 0 {
			return memoryEntryRef{}, fmt.Errorf("line_number is required with section")
		}
		if lineNumber > len(doc.Sections[idx].Items) {
			if lineNumber <= len(all) {
				want := all[lineNumber-1]
				if want.Section == sectionKey {
					return memoryEntryRef{
						GlobalLine:   lineNumber,
						SectionIndex: idx,
						ItemIndex:    lineNumber - globalLineNumber(doc, sectionKey, 1),
						Content:      want.Content,
					}, nil
				}
			}
			return memoryEntryRef{}, fmt.Errorf("line_number %d is out of range for section %s", lineNumber, sectionKey)
		}
		return memoryEntryRef{
			GlobalLine:   globalLineNumber(doc, sectionKey, lineNumber),
			SectionIndex: idx,
			ItemIndex:    lineNumber - 1,
			Content:      doc.Sections[idx].Items[lineNumber-1],
		}, nil
	}
	if lineNumber > 0 {
		if lineNumber > len(all) {
			return memoryEntryRef{}, fmt.Errorf("line_number %d is out of range; view memory first", lineNumber)
		}
		want := all[lineNumber-1]
		for si, section := range doc.Sections {
			if section.Key != want.Section {
				continue
			}
			for ii, entry := range section.Items {
				if entry == want.Content {
					return memoryEntryRef{GlobalLine: lineNumber, SectionIndex: si, ItemIndex: ii, Content: entry}, nil
				}
			}
		}
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return memoryEntryRef{}, fmt.Errorf("line_number or content is required")
	}
	matches := make([]memoryEntryRef, 0, 1)
	global := 1
	for si, section := range doc.Sections {
		for ii, entry := range section.Items {
			if strings.Contains(strings.ToLower(entry), strings.ToLower(content)) {
				matches = append(matches, memoryEntryRef{GlobalLine: global, SectionIndex: si, ItemIndex: ii, Content: entry})
			}
			global++
		}
	}
	if len(matches) == 0 {
		return memoryEntryRef{}, fmt.Errorf("no memory item matched content")
	}
	if len(matches) > 1 {
		return memoryEntryRef{}, fmt.Errorf("content matched multiple memory items; view memory and use line_number")
	}
	return matches[0], nil
}

func sectionIndex(doc sessionmemory.Document, section string, defaultCurrent bool) (int, error) {
	key := sessionmemory.NormalizeSectionKey(section)
	if key == "" && defaultCurrent {
		key = "current_progress"
	}
	if key == "" {
		return 0, fmt.Errorf("section is required")
	}
	for i, s := range doc.Sections {
		if s.Key == key {
			return i, nil
		}
	}
	return 0, fmt.Errorf("unknown memory section %q", section)
}

func globalLineNumber(doc sessionmemory.Document, sectionKey string, sectionLine int) int {
	line := 1
	for _, section := range doc.Sections {
		for i := range section.Items {
			if section.Key == sectionKey && i+1 == sectionLine {
				return line
			}
			line++
		}
	}
	return line
}

func marshalMemoryOutput(out MemoryOutput) (string, error) {
	b, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("failed to marshal memory output: %w", err)
	}
	return string(b), nil
}

// 确保实现了 tool.InvokableTool 接口
var _ tool.InvokableTool = (*MemoryTool)(nil)
