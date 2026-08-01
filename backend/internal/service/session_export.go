package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/huoguojun123/EffChat/internal/model"
)

type SessionMarkdownExport struct {
	Filename string
	Content  []byte
}

type exportAttachment struct {
	FileID      int64  `json:"file_id"`
	Filename    string `json:"filename"`
	FileType    string `json:"file_type"`
	Unavailable bool   `json:"unavailable"`
}

type exportToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ToolName  string `json:"tool_name"`
	Arguments string `json:"arguments"`
	Function  struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type exportSegment struct {
	ToolCalls []exportToolCall `json:"tool_calls"`
}

type exportMessageData struct {
	Role        string                 `json:"role"`
	Content     string                 `json:"content"`
	ToolCallID  string                 `json:"tool_call_id"`
	ToolName    string                 `json:"tool_name"`
	ToolCalls   []exportToolCall       `json:"tool_calls"`
	Segments    []exportSegment        `json:"segments"`
	Attachments []exportAttachment     `json:"attachments"`
	Metadata    map[string]interface{} `json:"metadata"`
}

type exportToolSummary struct {
	ID        string
	Name      string
	Status    string
	URLs      []string
	Filenames []string
}

type exportTurn struct {
	UserContent      string
	AssistantContent []string
	Attachments      []exportAttachment
	Tools            []*exportToolSummary
	toolsByID        map[string]*exportToolSummary
}

func (s *MessageService) ExportSessionMarkdown(ctx context.Context, sessionID, userID int64, includeTools bool, exportedAt time.Time) (*SessionMarkdownExport, error) {
	session, err := s.sessionRepo.GetByIDContext(ctx, sessionID, userID)
	if err != nil {
		return nil, sessionLookupError(err)
	}
	messages, err := s.messageRepo.ListAllBySessionContext(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to load session export: %w", err)
	}

	activeFiles := map[int64]struct{}{}
	if s.fileRepo != nil {
		ids := exportAttachmentIDs(messages)
		files, fileErr := s.fileRepo.GetFormalFilesForSessionContext(ctx, userID, sessionID, ids)
		if fileErr != nil {
			return nil, fmt.Errorf("failed to load session attachments: %w", fileErr)
		}
		for id := range files {
			activeFiles[id] = struct{}{}
		}
	}

	content, err := buildSessionMarkdown(session, messages, activeFiles, s.fileRepo != nil, includeTools, exportedAt)
	if err != nil {
		return nil, err
	}
	return &SessionMarkdownExport{
		Filename: safeSessionExportFilename(session.Title, session.ID, exportedAt),
		Content:  []byte(content),
	}, nil
}

func buildSessionMarkdown(session *model.Session, messages []*model.Message, activeFiles map[int64]struct{}, resolvedFiles, includeTools bool, exportedAt time.Time) (string, error) {
	turns := make([]*exportTurn, 0)
	fileNames := make(map[int64]string)
	var current *exportTurn

	for _, message := range messages {
		var data exportMessageData
		if err := json.Unmarshal(message.MessageData, &data); err != nil {
			return "", fmt.Errorf("failed to decode message %d for export: %w", message.ID, err)
		}
		if exportMetadataBool(data.Metadata, "compaction_summary") {
			continue
		}

		switch message.Role {
		case "user":
			current = &exportTurn{
				UserContent: strings.TrimSpace(data.Content),
				Attachments: append([]exportAttachment(nil), data.Attachments...),
				toolsByID:   make(map[string]*exportToolSummary),
			}
			for i := range current.Attachments {
				attachment := &current.Attachments[i]
				attachment.Filename = strings.TrimSpace(attachment.Filename)
				fileNames[attachment.FileID] = attachment.Filename
				if resolvedFiles {
					_, active := activeFiles[attachment.FileID]
					attachment.Unavailable = attachment.Unavailable || !active
				}
			}
			turns = append(turns, current)
		case "assistant":
			if current == nil || exportMetadataBool(data.Metadata, "error") || exportMetadataBool(data.Metadata, "ephemeral_error") {
				continue
			}
			if content := strings.TrimSpace(data.Content); content != "" {
				current.AssistantContent = append(current.AssistantContent, content)
			}
			if includeTools {
				for _, call := range exportToolCalls(data) {
					current.addToolCall(call, fileNames)
				}
			}
		case "tool":
			if current == nil || !includeTools {
				continue
			}
			current.applyToolResult(data)
		}
	}

	var out strings.Builder
	title := exportSingleLine(session.Title)
	if title == "" {
		title = fmt.Sprintf("会话 %d", session.ID)
	}
	fmt.Fprintf(&out, "# %s\n\n", title)
	fmt.Fprintf(&out, "- 创建时间：%s\n", session.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(&out, "- 导出时间：%s\n", exportedAt.Format(time.RFC3339))

	for _, turn := range turns {
		out.WriteString("\n## 用户\n\n")
		if turn.UserContent != "" {
			out.WriteString(turn.UserContent)
			out.WriteString("\n")
		}
		if len(turn.Attachments) > 0 {
			out.WriteString("\n### 附件\n\n")
			for _, attachment := range turn.Attachments {
				name := exportInlineCode(attachment.Filename)
				if name == "" {
					name = fmt.Sprintf("文件 %d", attachment.FileID)
				}
				fileType := exportInlineCode(strings.TrimSpace(attachment.FileType))
				if fileType == "" {
					fileType = "未知类型"
				}
				status := "可用"
				if attachment.Unavailable {
					status = "已删除"
				}
				fmt.Fprintf(&out, "- `%s` · `%s` · %s\n", name, fileType, status)
			}
		}

		if len(turn.AssistantContent) > 0 {
			out.WriteString("\n## 助手\n\n")
			out.WriteString(strings.Join(turn.AssistantContent, "\n\n"))
			out.WriteString("\n")
		}
		if includeTools && len(turn.Tools) > 0 {
			out.WriteString("\n### 工具摘要\n\n")
			for _, tool := range turn.Tools {
				fmt.Fprintf(&out, "- `%s` · %s", exportInlineCode(tool.Name), exportToolStatusLabel(tool.Status))
				if len(tool.Filenames) > 0 {
					out.WriteString(" · 附件：")
					for i, filename := range tool.Filenames {
						if i > 0 {
							out.WriteString("、")
						}
						fmt.Fprintf(&out, "`%s`", exportInlineCode(filename))
					}
				}
				out.WriteString("\n")
				for _, sourceURL := range tool.URLs {
					fmt.Fprintf(&out, "  - 来源：<%s>\n", sourceURL)
				}
			}
		}
	}
	return strings.TrimSpace(out.String()) + "\n", nil
}

func exportAttachmentIDs(messages []*model.Message) []int64 {
	seen := make(map[int64]struct{})
	for _, message := range messages {
		var data exportMessageData
		if json.Unmarshal(message.MessageData, &data) != nil {
			continue
		}
		for _, attachment := range data.Attachments {
			if attachment.FileID > 0 {
				seen[attachment.FileID] = struct{}{}
			}
		}
	}
	ids := make([]int64, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func exportToolCalls(data exportMessageData) []exportToolCall {
	calls := append([]exportToolCall(nil), data.ToolCalls...)
	for _, segment := range data.Segments {
		calls = append(calls, segment.ToolCalls...)
	}
	return calls
}

func (t *exportTurn) addToolCall(call exportToolCall, fileNames map[int64]string) {
	name := strings.TrimSpace(call.Name)
	if name == "" {
		name = strings.TrimSpace(call.ToolName)
	}
	if name == "" {
		name = strings.TrimSpace(call.Function.Name)
	}
	if name == "" {
		return
	}
	id := strings.TrimSpace(call.ID)
	if id == "" {
		id = fmt.Sprintf("%s:%d", name, len(t.Tools))
	}
	if _, exists := t.toolsByID[id]; exists {
		return
	}
	arguments := strings.TrimSpace(call.Arguments)
	if arguments == "" {
		arguments = strings.TrimSpace(call.Function.Arguments)
	}
	summary := &exportToolSummary{ID: id, Name: name, Status: "failed"}
	if strings.HasPrefix(name, "file_") {
		summary.Filenames = exportToolFilenames(arguments, fileNames)
	}
	t.toolsByID[id] = summary
	t.Tools = append(t.Tools, summary)
}

func (t *exportTurn) applyToolResult(data exportMessageData) {
	id := strings.TrimSpace(data.ToolCallID)
	name := strings.TrimSpace(data.ToolName)
	summary := t.toolsByID[id]
	if summary == nil {
		if name == "" {
			return
		}
		summary = &exportToolSummary{ID: id, Name: name}
		t.Tools = append(t.Tools, summary)
		if id != "" {
			t.toolsByID[id] = summary
		}
	}
	summary.Status, summary.URLs = exportToolResultFacts(summary.Name, data.Content)
}

func exportToolResultFacts(toolName, content string) (string, []string) {
	status := "success"
	var payload map[string]interface{}
	if json.Unmarshal([]byte(content), &payload) != nil {
		return status, nil
	}
	if failed, _ := payload["search_failed"].(bool); failed {
		status = "failed"
	} else if ok, exists := payload["ok"].(bool); exists && !ok {
		status = "failed"
	} else if errText, _ := payload["error"].(string); strings.TrimSpace(errText) != "" {
		status = "failed"
	} else if degraded, _ := payload["degraded"].(bool); degraded || exportFallbackUsed(payload) {
		status = "degraded"
	}

	urls := make([]string, 0)
	if toolName == "web_extract" {
		if pageURL, _ := payload["url"].(string); pageURL != "" {
			if safeURL, ok := exportSourceURL(pageURL); ok {
				urls = append(urls, safeURL)
			}
		}
	}
	if toolName == "web_search" {
		if citations, ok := payload["citations"].([]interface{}); ok {
			for _, item := range citations {
				citation, _ := item.(map[string]interface{})
				pageURL, _ := citation["url"].(string)
				if safeURL, ok := exportSourceURL(pageURL); ok {
					urls = append(urls, safeURL)
				}
			}
		}
	}
	return status, uniqueStrings(urls)
}

func exportFallbackUsed(payload map[string]interface{}) bool {
	source, _ := payload["source"].(string)
	attempted, _ := payload["attempted_sources"].([]interface{})
	return strings.TrimSpace(source) != "" && len(attempted) > 1
}

func exportToolFilenames(arguments string, fileNames map[int64]string) []string {
	var payload map[string]interface{}
	if json.Unmarshal([]byte(arguments), &payload) != nil {
		return nil
	}
	ids := make([]int64, 0)
	if id, ok := toInt64(payload["file_id"]); ok {
		ids = append(ids, id)
	}
	if values, ok := payload["file_ids"].([]interface{}); ok {
		for _, value := range values {
			if id, valid := toInt64(value); valid {
				ids = append(ids, id)
			}
		}
	}
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		if name := strings.TrimSpace(fileNames[id]); name != "" {
			names = append(names, name)
		}
	}
	return uniqueStrings(names)
}

func exportMetadataBool(metadata map[string]interface{}, key string) bool {
	value, _ := metadata[key].(bool)
	return value
}

func exportToolStatusLabel(status string) string {
	switch status {
	case "degraded":
		return "降级完成"
	case "failed":
		return "失败"
	default:
		return "成功"
	}
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func exportSourceURL(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if strings.ContainsAny(value, "\r\n<>") {
		return "", false
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil {
		return "", false
	}
	for key := range parsed.Query() {
		normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
		if normalized == "token" || normalized == "key" || normalized == "api_key" || normalized == "auth" ||
			normalized == "signature" || normalized == "sig" || normalized == "expires" || strings.HasPrefix(normalized, "x_amz_") {
			return "", false
		}
	}
	parsed.Fragment = ""
	return parsed.String(), true
}

var unsafeExportFilename = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)

func safeSessionExportFilename(title string, sessionID int64, exportedAt time.Time) string {
	name := unsafeExportFilename.ReplaceAllString(exportSingleLine(title), " ")
	name = strings.Trim(name, " .")
	if name == "" {
		name = fmt.Sprintf("conversation-%s-%d", exportedAt.Format("20060102"), sessionID)
	}
	runes := []rune(name)
	if len(runes) > 80 {
		name = strings.TrimSpace(string(runes[:80]))
	}
	return name + ".md"
}

func exportSingleLine(value string) string {
	return strings.Join(strings.FieldsFunc(value, unicode.IsSpace), " ")
}

func exportInlineCode(value string) string {
	return strings.ReplaceAll(exportSingleLine(value), "`", "'")
}
