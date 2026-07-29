package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/huoguojun123/EffChat/internal/filepolicy"
	"github.com/huoguojun123/EffChat/internal/model"
)

// FileReadStore 是 file_read 工具依赖的最小仓库接口。
//
// tool 包不直接依赖 repository 的具体实现，方便单元测试用 fake store 验证工具行为。
// 真正的权限边界由仓库实现：只能返回“当前用户 + 当前会话消息引用过”的文件。
type FileReadStore interface {
	GetReadableFileForAgent(userID, sessionID, fileID int64) (*model.File, error)
}

type FileWorkspaceStore interface {
	FileReadStore
	ListReadableFilesForAgent(userID, sessionID int64) ([]*model.File, error)
}

// FileReadTool 让模型按需读取已上传附件的提取正文。
//
// 设计背景：
// 旧方案会把每个文本附件的 extracted_text 全文拼进用户消息 content。这样虽然简单，
// 但大文件会在第一轮就吃掉大量上下文，甚至直接触发压缩；更糟糕的是，模型不需要
// 文件内容时也要为这些 token 付费。现在改成“消息里只放附件清单 + 模型需要时调用
// file_read”的模式，贴近 Claude Code 的工作区按需读取思路，但 v1 保持简单：
// 不做向量库、不做 RAG、不引入 MCP runtime，只基于已持久化文本做确定性的片段返回。
type FileReadTool struct {
	store           FileReadStore
	userID          int64
	sessionID       int64
	defaultMaxChars int
	hardMaxChars    int
}

const (
	defaultFileReadMaxChars = 4000
	hardFileReadMaxChars    = 12000
)

func NewFileReadTool(store FileReadStore, userID, sessionID int64) *FileReadTool {
	return &FileReadTool{
		store:           store,
		userID:          userID,
		sessionID:       sessionID,
		defaultMaxChars: defaultFileReadMaxChars,
		hardMaxChars:    hardFileReadMaxChars,
	}
}

type FileReadInput struct {
	FileID   int64  `json:"file_id"`
	Query    string `json:"query,omitempty"`
	Offset   int    `json:"offset,omitempty"`
	MaxChars int    `json:"max_chars,omitempty"`
}

type FileReadOutput struct {
	FileID        int64  `json:"file_id"`
	Filename      string `json:"filename,omitempty"`
	FileType      string `json:"file_type,omitempty"`
	ExtractStatus string `json:"extract_status,omitempty"`
	Matched       bool   `json:"matched"`
	StartOffset   int    `json:"start_offset"`
	NextOffset    int    `json:"next_offset"`
	Truncated     bool   `json:"truncated"`
	Content       string `json:"content,omitempty"`
	Message       string `json:"message"`
	Error         string `json:"error,omitempty"`
}

func (t *FileReadTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "file_read",
		Desc: "Read extracted text from a file attached in the current conversation. " +
			"Use this when the user asks about an uploaded PDF, document, spreadsheet, markdown, or text file. " +
			"Call file_read with the file_id before relying on file content that is not already visible in context. " +
			"For large files, use file_search first or use query to return a nearby window instead of starting at the beginning. " +
			"Use next_offset to continue only when the missing passage is necessary; do not page through an entire long file unless the user explicitly asks for a full pass. " +
			"Use file name, file_id, offsets, section names, or other returned identifiers when explaining evidence. Do not fabricate page numbers or line citations that the tool did not return. " +
			"Stop once you have enough evidence. " +
			"This tool cannot read files not referenced by the current conversation.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"file_id": {
				Type:     schema.Integer,
				Desc:     "The file_id shown in the attachment list of the current conversation.",
				Required: true,
			},
			"query": {
				Type:     schema.String,
				Desc:     "Optional focused keywords, phrase, section title, entity, claim, table name, or metric. Matching returns a window near the hit instead of always reading from the beginning.",
				Required: false,
			},
			"offset": {
				Type:     schema.Integer,
				Desc:     "Optional character offset for continuing a previous read. Use next_offset returned by the previous call.",
				Required: false,
			},
			"max_chars": {
				Type:     schema.Integer,
				Desc:     "Maximum characters to return. Defaults to 4000 and is clamped to 12000.",
				Required: false,
			},
		}),
	}, nil
}

// InvokableRun 执行文件读取。除了输入 JSON 结构不可解析这种协议错误外，业务失败
// 都返回结构化 JSON，不抛 Go error，避免因为某个附件不可读而中断整轮 Agent。
func (t *FileReadTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	var input FileReadInput
	if err := json.Unmarshal([]byte(argumentsInJSON), &input); err != nil {
		return marshalFileReadOutput(FileReadOutput{Error: "invalid input: " + err.Error(), Message: "file_read input is invalid."})
	}
	if input.FileID <= 0 {
		return marshalFileReadOutput(FileReadOutput{FileID: input.FileID, Error: "file_id must be positive", Message: "file_id must be positive."})
	}
	if t.store == nil || t.userID <= 0 || t.sessionID <= 0 {
		return marshalFileReadOutput(FileReadOutput{FileID: input.FileID, Error: "file_read is not available in this context", Message: "file_read is unavailable."})
	}

	maxChars := clampFileReadMaxChars(input.MaxChars, t.defaultMaxChars, t.hardMaxChars)
	f, err := t.store.GetReadableFileForAgent(t.userID, t.sessionID, input.FileID)
	if err != nil {
		return marshalFileReadOutput(FileReadOutput{
			FileID:  input.FileID,
			Error:   "file is not readable in this conversation",
			Message: "The file was not found or is not referenced by the current conversation.",
		})
	}

	out := FileReadOutput{
		FileID:        f.ID,
		Filename:      f.FileName,
		FileType:      f.FileType,
		ExtractStatus: f.ExtractStatus,
	}
	if strings.HasPrefix(f.FileType, "image/") {
		out.Message = "This is an image attachment. Use the model vision input when available; file_read only returns extracted text."
		return marshalFileReadOutput(out)
	}
	if strings.TrimSpace(f.ExtractStatus) != "" && f.ExtractStatus != "ready" {
		out.Message = fmt.Sprintf("File text is not ready (status=%s).", f.ExtractStatus)
		if f.ExtractError != nil && strings.TrimSpace(*f.ExtractError) != "" {
			out.Error = strings.TrimSpace(*f.ExtractError)
		}
		return marshalFileReadOutput(out)
	}
	text, readErr := extractedTextFromFile(f)
	if readErr != nil {
		out.Error = readErr.Error()
		out.Message = "Failed to read extracted text sidecar for this file."
		return marshalFileReadOutput(out)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		out.Message = "No extracted text is available for this file."
		return marshalFileReadOutput(out)
	}

	snippet := selectFileSnippet(text, input.Query, input.Offset, maxChars)
	out.Content = snippet.Content
	out.Matched = snippet.Matched
	out.StartOffset = snippet.StartOffset
	out.NextOffset = snippet.NextOffset
	out.Truncated = snippet.Truncated
	if strings.TrimSpace(input.Query) == "" {
		if snippet.StartOffset > 0 {
			out.Message = "Returned extracted file text starting from the requested offset."
		} else {
			out.Message = "Returned the beginning of the extracted file text."
		}
	} else if snippet.Matched {
		out.Message = "Returned a window around text matching the query."
	} else {
		out.Message = "No text matched the query; returned file text from the requested offset instead."
	}
	return marshalFileReadOutput(out)
}

func extractedTextFromFile(f *model.File) (string, error) {
	// 解析正文只从 data/storage 下的文本文件读取；数据库不保存正文，也不做旧字段回退。
	if f.ExtractedTextPath == nil || strings.TrimSpace(*f.ExtractedTextPath) == "" {
		return "", nil
	}
	path, err := filepolicy.ExistingPath(*f.ExtractedTextPath)
	if err != nil {
		return "", fmt.Errorf("failed to read extracted text file: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read extracted text file: %w", err)
	}
	return string(data), nil
}

type fileSnippet struct {
	Content     string
	Matched     bool
	StartOffset int
	NextOffset  int
	Truncated   bool
}

func clampFileReadMaxChars(requested, fallback, hard int) int {
	if requested <= 0 {
		return fallback
	}
	if requested > hard {
		return hard
	}
	return requested
}

func selectFileSnippet(text, query string, offset, maxChars int) fileSnippet {
	totalRunes := utf8.RuneCountInString(text)
	offset = clampOffset(offset, totalRunes)
	query = strings.TrimSpace(query)
	if query == "" {
		return sliceFileReadRunes(text, offset, maxChars)
	}

	tokens := queryTokens(query)
	if len(tokens) == 0 {
		return sliceFileReadRunes(text, offset, maxChars)
	}

	if matchOffset, ok := firstTokenMatchOffset(text, tokens, offset); ok {
		if start, end := paragraphBoundsAtRuneOffset(text, matchOffset); end > start && end-start <= maxChars {
			snippet := sliceFileReadRunes(text, start, end-start)
			snippet.Matched = true
			return snippet
		}
		// 命中时给命中点之前留少量上下文，避免用户只看到句子后半段。
		start := matchOffset - maxChars/5
		if start < 0 {
			start = 0
		}
		snippet := sliceFileReadRunes(text, start, maxChars)
		snippet.Matched = true
		return snippet
	}

	return sliceFileReadRunes(text, offset, maxChars)
}

func paragraphBoundsAtRuneOffset(text string, offset int) (int, int) {
	runes := []rune(text)
	if len(runes) == 0 {
		return 0, 0
	}
	offset = clampOffset(offset, len(runes)-1)
	start := offset
	for start > 0 {
		if runes[start-1] == '\n' {
			break
		}
		start--
	}
	end := offset
	for end < len(runes) {
		if runes[end] == '\n' {
			break
		}
		end++
	}
	return start, end
}

func queryTokens(query string) []string {
	fields := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
	})
	if len(fields) == 0 {
		return []string{strings.ToLower(query)}
	}
	seen := map[string]struct{}{}
	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		tokens = append(tokens, field)
	}
	return tokens
}

func firstTokenMatchOffset(text string, tokens []string, minOffset int) (int, bool) {
	lowerRunes := []rune(strings.ToLower(text))
	if minOffset > len(lowerRunes) {
		minOffset = len(lowerRunes)
	}
	searchText := string(lowerRunes[minOffset:])
	best := -1
	for _, token := range tokens {
		if token == "" {
			continue
		}
		idx := strings.Index(searchText, token)
		if idx < 0 {
			continue
		}
		runeIdx := minOffset + utf8.RuneCountInString(searchText[:idx])
		if best < 0 || runeIdx < best {
			best = runeIdx
		}
	}
	if best < 0 {
		return 0, false
	}
	return best, true
}

func sliceFileReadRunes(text string, offset, maxChars int) fileSnippet {
	if maxChars <= 0 {
		return fileSnippet{StartOffset: offset, NextOffset: offset, Truncated: strings.TrimSpace(text) != ""}
	}
	runes := []rune(text)
	offset = clampOffset(offset, len(runes))
	if offset >= len(runes) {
		return fileSnippet{StartOffset: offset, NextOffset: offset, Truncated: false}
	}
	end := offset + maxChars
	if end > len(runes) {
		end = len(runes)
	}
	return fileSnippet{
		Content:     string(runes[offset:end]),
		StartOffset: offset,
		NextOffset:  end,
		Truncated:   end < len(runes),
	}
}

func clampOffset(offset, total int) int {
	if offset < 0 {
		return 0
	}
	if offset > total {
		return total
	}
	return offset
}

func marshalFileReadOutput(out FileReadOutput) (string, error) {
	b, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("failed to marshal file_read output: %w", err)
	}
	return string(b), nil
}

// 确保实现了 tool.InvokableTool 接口。
var _ tool.InvokableTool = (*FileReadTool)(nil)
