package tool

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huoguojun123/EffChat/internal/filepolicy"
	"github.com/huoguojun123/EffChat/internal/model"
)

type fakeFileReadStore struct {
	files map[int64]*model.File
	err   error
}

func TestFileToolDescriptionsGuideSearchThenReadWorkflow(t *testing.T) {
	listInfo, err := NewFileListTool(nil, 1, 2).Info(context.Background())
	if err != nil {
		t.Fatalf("file_list Info() error: %v", err)
	}
	searchInfo, err := NewFileSearchTool(nil, 1, 2).Info(context.Background())
	if err != nil {
		t.Fatalf("file_search Info() error: %v", err)
	}
	readInfo, err := NewFileReadTool(nil, 1, 2).Info(context.Background())
	if err != nil {
		t.Fatalf("file_read Info() error: %v", err)
	}
	for _, tc := range []struct {
		name string
		desc string
		want string
	}{
		{"file_list", listInfo.Desc, "File names and metadata are not evidence"},
		{"file_search", searchInfo.Desc, "important entity names"},
		{"file_read", readInfo.Desc, "Do not fabricate page numbers"},
	} {
		if !strings.Contains(tc.desc, tc.want) {
			t.Fatalf("%s description missing %q:\n%s", tc.name, tc.want, tc.desc)
		}
	}
}

func writeManagedSidecar(t *testing.T, name, content string) string {
	t.Helper()
	dir := filepath.Join(filepolicy.AttachmentExtractedRoot, "test-file-read", strings.ReplaceAll(t.Name(), "/", "_"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	return path
}

func TestFileReadToolReadsSidecarExtractedText(t *testing.T) {
	path := writeManagedSidecar(t, "extract.txt", "sidecar 正文内容")
	store := &fakeFileReadStore{files: map[int64]*model.File{
		17: {
			ID:                17,
			FileName:          "paper.pdf",
			FileType:          "application/pdf",
			ExtractStatus:     "ready",
			ExtractedTextPath: &path,
		},
	}}
	tool := NewFileReadTool(store, 1, 2)
	input, _ := json.Marshal(FileReadInput{FileID: 17})
	raw, err := tool.InvokableRun(context.Background(), string(input))
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}

	var out FileReadOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Content != "sidecar 正文内容" {
		t.Fatalf("content = %q, want sidecar 正文内容", out.Content)
	}
}

func (s *fakeFileReadStore) GetReadableFileForAgent(userID, sessionID, fileID int64) (*model.File, error) {
	if s.err != nil {
		return nil, s.err
	}
	f, ok := s.files[fileID]
	if !ok {
		return nil, errors.New("not found")
	}
	return f, nil
}

func (s *fakeFileReadStore) ListReadableFilesForAgent(userID, sessionID int64) ([]*model.File, error) {
	if s.err != nil {
		return nil, s.err
	}
	files := make([]*model.File, 0, len(s.files))
	for _, f := range s.files {
		files = append(files, f)
	}
	return files, nil
}

func TestFileReadToolReadsMatchingParagraphs(t *testing.T) {
	content := "项目目标：上线\n\n缓存策略：命中率需要排查\n\n无关段落：略"
	path := writeManagedSidecar(t, "notes.txt", content)
	store := &fakeFileReadStore{files: map[int64]*model.File{
		7: {
			ID:                7,
			FileName:          "notes.md",
			FileType:          "text/markdown",
			ExtractStatus:     "ready",
			ExtractedTextPath: &path,
		},
	}}
	tool := NewFileReadTool(store, 1, 2)
	input, _ := json.Marshal(FileReadInput{FileID: 7, Query: "缓存", MaxChars: 200})
	raw, err := tool.InvokableRun(context.Background(), string(input))
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}

	var out FileReadOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !out.Matched {
		t.Fatalf("matched = false, want true; output=%+v", out)
	}
	if !strings.Contains(out.Content, "缓存策略") {
		t.Fatalf("content = %q, want matched paragraph", out.Content)
	}
	if strings.Contains(out.Content, "无关段落") {
		t.Fatalf("content should only include matched paragraphs, got %q", out.Content)
	}
}

func TestFileReadToolClampsAndTruncates(t *testing.T) {
	path := writeManagedSidecar(t, "long.txt", strings.Repeat("甲", hardFileReadMaxChars+20))
	store := &fakeFileReadStore{files: map[int64]*model.File{
		8: {
			ID:                8,
			FileName:          "long.txt",
			FileType:          "text/plain",
			ExtractStatus:     "ready",
			ExtractedTextPath: &path,
		},
	}}
	tool := NewFileReadTool(store, 1, 2)
	input, _ := json.Marshal(FileReadInput{FileID: 8, MaxChars: hardFileReadMaxChars + 999})
	raw, err := tool.InvokableRun(context.Background(), string(input))
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}

	var out FileReadOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !out.Truncated {
		t.Fatalf("truncated = false, want true")
	}
	if got := len([]rune(out.Content)); got != hardFileReadMaxChars {
		t.Fatalf("content runes = %d, want %d", got, hardFileReadMaxChars)
	}
}

func TestFileReadToolContinuesFromNextOffset(t *testing.T) {
	content := "第一段内容。第二段内容。第三段内容。"
	path := writeManagedSidecar(t, "long.txt", content)
	store := &fakeFileReadStore{files: map[int64]*model.File{
		9: {
			ID:                9,
			FileName:          "long.txt",
			FileType:          "text/plain",
			ExtractStatus:     "ready",
			ExtractedTextPath: &path,
		},
	}}
	tool := NewFileReadTool(store, 1, 2)
	input, _ := json.Marshal(FileReadInput{FileID: 9, MaxChars: 5})
	raw, err := tool.InvokableRun(context.Background(), string(input))
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	var first FileReadOutput
	if err := json.Unmarshal([]byte(raw), &first); err != nil {
		t.Fatalf("unmarshal first: %v", err)
	}
	if first.NextOffset != 5 || !first.Truncated {
		t.Fatalf("first next_offset/truncated = %d/%v", first.NextOffset, first.Truncated)
	}

	input, _ = json.Marshal(FileReadInput{FileID: 9, Offset: first.NextOffset, MaxChars: 5})
	raw, err = tool.InvokableRun(context.Background(), string(input))
	if err != nil {
		t.Fatalf("InvokableRun second: %v", err)
	}
	var second FileReadOutput
	if err := json.Unmarshal([]byte(raw), &second); err != nil {
		t.Fatalf("unmarshal second: %v", err)
	}
	if second.StartOffset != first.NextOffset {
		t.Fatalf("second start_offset = %d, want %d", second.StartOffset, first.NextOffset)
	}
	if second.Content == first.Content {
		t.Fatalf("second content should continue instead of repeating beginning")
	}
}

func TestFileSearchToolFindsOffsets(t *testing.T) {
	content := strings.Repeat("背景介绍。\n", 20) + "Discussion: 后半篇关键结论。\n"
	path := writeManagedSidecar(t, "paper.txt", content)
	store := &fakeFileReadStore{files: map[int64]*model.File{
		10: {
			ID:                10,
			FileName:          "paper.pdf",
			FileType:          "application/pdf",
			ExtractStatus:     "ready",
			ExtractedTextPath: &path,
		},
	}}
	tool := NewFileSearchTool(store, 1, 2)
	input, _ := json.Marshal(FileSearchInput{Query: "Discussion", MaxResults: 3})
	raw, err := tool.InvokableRun(context.Background(), string(input))
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	var out FileSearchOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Results) != 1 {
		t.Fatalf("results len = %d, want 1; output=%+v", len(out.Results), out)
	}
	if out.Results[0].Offset <= 0 || !strings.Contains(out.Results[0].Snippet, "Discussion") {
		t.Fatalf("bad search match: %+v", out.Results[0])
	}
}

func TestFileSearchToolSearchesBeyondOldPrefixWindow(t *testing.T) {
	content := strings.Repeat("A", 512*1024+128) + " target-near-end"
	path := writeManagedSidecar(t, "large.txt", content)
	store := &fakeFileReadStore{files: map[int64]*model.File{
		11: {
			ID:                11,
			FileName:          "large.txt",
			FileType:          "text/plain",
			ExtractStatus:     "ready",
			ExtractedTextPath: &path,
		},
	}}
	tool := NewFileSearchTool(store, 1, 2)
	input, _ := json.Marshal(FileSearchInput{Query: "target-near-end", MaxResults: 3})
	raw, err := tool.InvokableRun(context.Background(), string(input))
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}

	var out FileSearchOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Results) == 0 {
		t.Fatalf("results len = 0, want at least one match; output=%+v", out)
	}
	if !strings.Contains(out.Results[0].Snippet, "target-near-end") {
		t.Fatalf("snippet = %q, want target-near-end", out.Results[0].Snippet)
	}
	if out.ScanLimited {
		t.Fatalf("scan_limited = true, want false for normal extracted document")
	}
}

func TestFileSearchToolLimitsVeryLargeSidecarScan(t *testing.T) {
	content := strings.Repeat("A", fileSearchMaxScanBytes+128) + " target-beyond-window"
	path := writeManagedSidecar(t, "huge.txt", content)
	store := &fakeFileReadStore{files: map[int64]*model.File{
		12: {
			ID:                12,
			FileName:          "huge.txt",
			FileType:          "text/plain",
			ExtractStatus:     "ready",
			ExtractedTextPath: &path,
		},
	}}
	tool := NewFileSearchTool(store, 1, 2)
	input, _ := json.Marshal(FileSearchInput{Query: "target-beyond-window", MaxResults: 3})
	raw, err := tool.InvokableRun(context.Background(), string(input))
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}

	var out FileSearchOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Results) == 0 {
		t.Fatalf("results len = 0, want tail sampled match; output=%+v", out)
	}
	if !strings.Contains(out.Results[0].Snippet, "target-beyond-window") {
		t.Fatalf("snippet = %q, want target-beyond-window", out.Results[0].Snippet)
	}
	if !out.ScanLimited {
		t.Fatalf("scan_limited = false, want true")
	}
	if len(out.ScannedRanges) < 3 {
		t.Fatalf("scanned_ranges len = %d, want beginning/middle/end sampled ranges; output=%+v", len(out.ScannedRanges), out)
	}
	if out.ScannedFiles != 1 {
		t.Fatalf("scanned_files = %d, want 1", out.ScannedFiles)
	}
	if !strings.Contains(out.Message, "sampled windows") {
		t.Fatalf("message should explain scan limit, got %q", out.Message)
	}
}

func TestFileReadToolUnreadableFileReturnsStructuredError(t *testing.T) {
	tool := NewFileReadTool(&fakeFileReadStore{err: errors.New("forbidden")}, 1, 2)
	input, _ := json.Marshal(FileReadInput{FileID: 99})
	raw, err := tool.InvokableRun(context.Background(), string(input))
	if err != nil {
		t.Fatalf("InvokableRun should not return Go error: %v", err)
	}

	var out FileReadOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Error == "" {
		t.Fatalf("expected structured error, got %+v", out)
	}
	if out.FileID != 99 {
		t.Fatalf("file_id = %d, want 99", out.FileID)
	}
}

func TestFileReadToolDoesNotExposeStoredExtractionCause(t *testing.T) {
	privateCause := "provider secret /srv/private/extractor"
	tool := NewFileReadTool(&fakeFileReadStore{files: map[int64]*model.File{
		99: {
			ID:            99,
			FileName:      "fixture.pdf",
			FileType:      "application/pdf",
			ExtractStatus: "failed",
			ExtractError:  &privateCause,
		},
	}}, 1, 2)
	input, _ := json.Marshal(FileReadInput{FileID: 99})
	raw, err := tool.InvokableRun(context.Background(), string(input))
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if strings.Contains(raw, "secret") || strings.Contains(raw, "/srv/private") {
		t.Fatalf("file_read exposed stored extraction cause: %s", raw)
	}
}

func TestFileListToolRepositoryFailureReturnsGoError(t *testing.T) {
	tool := NewFileListTool(&fakeFileReadStore{err: errors.New("postgres://fixture:secret@db.example/effchat")}, 1, 2)
	raw, err := tool.InvokableRun(context.Background(), `{}`)
	if err == nil || raw != "" || !strings.Contains(err.Error(), "list conversation files") {
		t.Fatalf("repository failure was not preserved for Tool governance: raw=%q err=%v", raw, err)
	}
}
