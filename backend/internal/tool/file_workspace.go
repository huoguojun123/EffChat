package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/huoguojun123/effchat/internal/filepolicy"
	"github.com/huoguojun123/effchat/internal/model"
)

type FileListTool struct {
	store     FileWorkspaceStore
	userID    int64
	sessionID int64
}

type FileSearchTool struct {
	store     FileWorkspaceStore
	userID    int64
	sessionID int64
}

type FileListOutput struct {
	Files   []FileListItem `json:"files"`
	Message string         `json:"message"`
	Error   string         `json:"error,omitempty"`
}

type FileListItem struct {
	FileID        int64  `json:"file_id"`
	Filename      string `json:"filename"`
	FileType      string `json:"file_type"`
	Size          int64  `json:"size"`
	ExtractStatus string `json:"extract_status,omitempty"`
	TokenEstimate int    `json:"token_estimate,omitempty"`
}

type FileSearchInput struct {
	Query      string `json:"query"`
	FileID     int64  `json:"file_id,omitempty"`
	MaxResults int    `json:"max_results,omitempty"`
}

type FileSearchOutput struct {
	Query         string            `json:"query"`
	Results       []FileSearchMatch `json:"results"`
	ScannedRanges []FileSearchRange `json:"scanned_ranges,omitempty"`
	ScannedFiles  int               `json:"scanned_files,omitempty"`
	ScannedBytes  int64             `json:"scanned_bytes,omitempty"`
	ScanLimited   bool              `json:"scan_limited,omitempty"`
	Message       string            `json:"message"`
	Error         string            `json:"error,omitempty"`
}

type FileSearchMatch struct {
	FileID   int64  `json:"file_id"`
	Filename string `json:"filename"`
	Offset   int    `json:"offset"`
	Snippet  string `json:"snippet"`
}

type FileSearchRange struct {
	FileID      int64  `json:"file_id"`
	Filename    string `json:"filename"`
	StartOffset int    `json:"start_offset"`
	EndOffset   int    `json:"end_offset"`
}

func NewFileListTool(store FileWorkspaceStore, userID, sessionID int64) *FileListTool {
	return &FileListTool{store: store, userID: userID, sessionID: sessionID}
}

func NewFileSearchTool(store FileWorkspaceStore, userID, sessionID int64) *FileSearchTool {
	return &FileSearchTool{store: store, userID: userID, sessionID: sessionID}
}

const (
	fileSearchMaxScanBytes = 5 * 1024 * 1024
	fileSearchWindowRunes  = 128 * 1024
	fileSearchMaxWindows   = 10
)

func (t *FileListTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name:        "file_list",
		Desc:        "List files available in the current conversation workspace. Use this when the user asks about uploaded files, when a prompt implies a file should exist, when you need file_id values, or when you are unsure which file to inspect. File names and metadata are not evidence of document contents; use file_search or file_read before relying on the text of a document.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{}),
	}, nil
}

func (t *FileListTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	if t.store == nil || t.userID <= 0 || t.sessionID <= 0 {
		return marshalFileWorkspaceOutput(FileListOutput{Error: "file_list is not available", Message: "file_list is unavailable."})
	}
	files, err := t.store.ListReadableFilesForAgent(t.userID, t.sessionID)
	if err != nil {
		return marshalFileWorkspaceOutput(FileListOutput{Error: err.Error(), Message: "Failed to list conversation files."})
	}
	items := make([]FileListItem, 0, len(files))
	for _, f := range files {
		items = append(items, fileListItem(f))
	}
	return marshalFileWorkspaceOutput(FileListOutput{
		Files:   items,
		Message: fmt.Sprintf("Found %d file(s) in the current conversation workspace.", len(items)),
	})
}

func (t *FileSearchTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "file_search",
		Desc: "Search extracted text of files in the current conversation workspace by keyword or phrase. Use this when relevant document content is not already in context, especially before file_read for large documents, papers, spreadsheets, or multi-file tasks. Build focused queries that include the important entity names plus the user's concrete terms, such as section titles, claims, table names, methods, metrics, product names, author names, or quoted phrases. Avoid short generic queries that will match unrelated passages. If the user asks in Chinese or another non-English language but the document may use English terminology, call file_search again with likely English equivalents when needed. Results are leads, not proof; read or cite only passages that directly support the answer.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"query": {
				Type:     schema.String,
				Desc:     "Focused keyword, phrase, section title, entity, claim, table name, method, metric, or quoted term to search for. Include important entity names.",
				Required: true,
			},
			"file_id": {
				Type:     schema.Integer,
				Desc:     "Optional file_id to search inside a single file when the target document is known. Leave empty to search all conversation files.",
				Required: false,
			},
			"max_results": {
				Type:     schema.Integer,
				Desc:     "Maximum matches to return. Defaults to 8 and is clamped to 20; use enough results to compare candidates without flooding the context.",
				Required: false,
			},
		}),
	}, nil
}

func (t *FileSearchTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	var input FileSearchInput
	if err := json.Unmarshal([]byte(argumentsInJSON), &input); err != nil {
		return marshalFileWorkspaceOutput(FileSearchOutput{Error: "invalid input: " + err.Error(), Message: "file_search input is invalid."})
	}
	input.Query = strings.TrimSpace(input.Query)
	if input.Query == "" {
		return marshalFileWorkspaceOutput(FileSearchOutput{Error: "query is required", Message: "file_search query is required."})
	}
	if t.store == nil || t.userID <= 0 || t.sessionID <= 0 {
		return marshalFileWorkspaceOutput(FileSearchOutput{Query: input.Query, Error: "file_search is not available", Message: "file_search is unavailable."})
	}

	maxResults := input.MaxResults
	if maxResults <= 0 {
		maxResults = 8
	}
	if maxResults > 20 {
		maxResults = 20
	}

	files, err := filesForSearch(t.store, t.userID, t.sessionID, input.FileID)
	if err != nil {
		return marshalFileWorkspaceOutput(FileSearchOutput{Query: input.Query, Error: err.Error(), Message: "Failed to load searchable files."})
	}
	matches := make([]FileSearchMatch, 0, maxResults)
	tokens := queryTokens(input.Query)
	seenMatches := map[string]bool{}
	scannedRanges := make([]FileSearchRange, 0)
	scannedFiles := 0
	var scannedBytes int64
	scanLimited := false
	for _, f := range files {
		if strings.HasPrefix(f.FileType, "image/") {
			continue
		}
		windows, limited, bytesRead, err := extractedTextSearchWindowsFromFile(f)
		if err != nil || len(windows) == 0 {
			continue
		}
		scannedFiles++
		scannedBytes += bytesRead
		scanLimited = scanLimited || limited
		for _, window := range windows {
			scannedRanges = append(scannedRanges, FileSearchRange{
				FileID:      f.ID,
				Filename:    f.FileName,
				StartOffset: window.StartOffset,
				EndOffset:   window.EndOffset,
			})
			for _, match := range searchFileText(f, window.Text, tokens, maxResults-len(matches)) {
				match.Offset += window.StartOffset
				key := fmt.Sprintf("%d:%d", match.FileID, match.Offset)
				if seenMatches[key] {
					continue
				}
				seenMatches[key] = true
				matches = append(matches, match)
			}
			if len(matches) >= maxResults {
				break
			}
		}
		if len(matches) >= maxResults {
			break
		}
	}
	message := fmt.Sprintf("Found %d match(es).", len(matches))
	if len(matches) == 0 {
		message = "No extracted file text matched the query."
	}
	if scanLimited {
		message += " Very large files were searched with sampled windows from the beginning, middle, and end; use file_read with offsets from scanned_ranges and next_offset to inspect unsampled sections."
	}
	return marshalFileWorkspaceOutput(FileSearchOutput{
		Query:         input.Query,
		Results:       matches,
		ScannedRanges: scannedRanges,
		ScannedFiles:  scannedFiles,
		ScannedBytes:  scannedBytes,
		ScanLimited:   scanLimited,
		Message:       message,
	})
}

type fileSearchWindow struct {
	StartOffset int
	EndOffset   int
	Text        string
}

func extractedTextSearchWindowsFromFile(f *model.File) ([]fileSearchWindow, bool, int64, error) {
	if f.ExtractedTextPath == nil || strings.TrimSpace(*f.ExtractedTextPath) == "" {
		return nil, false, 0, nil
	}
	path, err := filepolicy.ExistingPath(*f.ExtractedTextPath)
	if err != nil {
		return nil, false, 0, fmt.Errorf("failed to read extracted text for search: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, 0, fmt.Errorf("failed to read extracted text for search: %w", err)
	}
	for len(data) > 0 && !utf8.Valid(data) {
		data = data[:len(data)-1]
	}
	text := string(data)
	if strings.TrimSpace(text) == "" {
		return nil, false, int64(len(data)), nil
	}
	runes := []rune(text)
	ranges := fileSearchRuneRanges(len(runes), len(data) > fileSearchMaxScanBytes)
	windows := make([]fileSearchWindow, 0, len(ranges))
	var scannedBytes int64
	for _, r := range ranges {
		if r.StartOffset >= r.EndOffset || r.StartOffset >= len(runes) {
			continue
		}
		end := min(r.EndOffset, len(runes))
		windowText := string(runes[r.StartOffset:end])
		scannedBytes += int64(len([]byte(windowText)))
		windows = append(windows, fileSearchWindow{
			StartOffset: r.StartOffset,
			EndOffset:   end,
			Text:        windowText,
		})
	}
	return windows, len(data) > fileSearchMaxScanBytes, scannedBytes, nil
}

func fileSearchRuneRanges(totalRunes int, limited bool) []FileSearchRange {
	if totalRunes <= 0 {
		return nil
	}
	if !limited || totalRunes <= fileSearchWindowRunes*fileSearchMaxWindows {
		return []FileSearchRange{{StartOffset: 0, EndOffset: totalRunes}}
	}

	starts := []int{0, maxInt((totalRunes-fileSearchWindowRunes)/2, 0), maxInt(totalRunes-fileSearchWindowRunes, 0)}
	for i := 1; len(starts) < fileSearchMaxWindows && i < fileSearchMaxWindows; i++ {
		pos := (totalRunes - fileSearchWindowRunes) * i / fileSearchMaxWindows
		starts = append(starts, maxInt(pos, 0))
	}
	return mergeFileSearchRanges(starts, totalRunes)
}

func mergeFileSearchRanges(starts []int, totalRunes int) []FileSearchRange {
	sort.Ints(starts)
	ranges := make([]FileSearchRange, 0, len(starts))
	for _, start := range starts {
		end := min(start+fileSearchWindowRunes, totalRunes)
		if len(ranges) == 0 || start > ranges[len(ranges)-1].EndOffset {
			ranges = append(ranges, FileSearchRange{StartOffset: start, EndOffset: end})
			continue
		}
		if end > ranges[len(ranges)-1].EndOffset {
			ranges[len(ranges)-1].EndOffset = end
		}
	}
	return ranges
}

func filesForSearch(store FileWorkspaceStore, userID, sessionID, fileID int64) ([]*model.File, error) {
	if fileID > 0 {
		f, err := store.GetReadableFileForAgent(userID, sessionID, fileID)
		if err != nil {
			return nil, err
		}
		return []*model.File{f}, nil
	}
	return store.ListReadableFilesForAgent(userID, sessionID)
}

func searchFileText(f *model.File, text string, tokens []string, limit int) []FileSearchMatch {
	if limit <= 0 || len(tokens) == 0 {
		return nil
	}
	matches := make([]FileSearchMatch, 0, limit)
	start := 0
	for len(matches) < limit {
		offset, ok := firstTokenMatchOffsetFrom(text, tokens, start)
		if !ok {
			break
		}
		snippet := sliceFileReadRunes(text, maxInt(offset-80, 0), 260)
		matches = append(matches, FileSearchMatch{
			FileID:   f.ID,
			Filename: f.FileName,
			Offset:   offset,
			Snippet:  compactWhitespace(snippet.Content),
		})
		start = offset + 1
	}
	return matches
}

func firstTokenMatchOffsetFrom(text string, tokens []string, minOffset int) (int, bool) {
	lowerRunes := []rune(strings.ToLower(text))
	if minOffset > len(lowerRunes) {
		minOffset = len(lowerRunes)
	}
	searchText := string(lowerRunes[minOffset:])
	best := -1
	for _, token := range tokens {
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

func fileListItem(f *model.File) FileListItem {
	return FileListItem{
		FileID:        f.ID,
		Filename:      f.FileName,
		FileType:      f.FileType,
		Size:          f.FileSize,
		ExtractStatus: f.ExtractStatus,
		TokenEstimate: f.TokenEstimate,
	}
}

func compactWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func marshalFileWorkspaceOutput(out interface{}) (string, error) {
	b, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("failed to marshal file workspace output: %w", err)
	}
	return string(b), nil
}

var _ tool.InvokableTool = (*FileListTool)(nil)
var _ tool.InvokableTool = (*FileSearchTool)(nil)
