package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huoguojun123/effchat/internal/filepolicy"
	"github.com/huoguojun123/effchat/internal/model"
)

func TestSkillReadPagesEntryAndReferenceFiles(t *testing.T) {
	root := t.TempDir()
	entry := writeTestSkillFile(t, root, "demo/abc/SKILL.md", strings.Repeat("入口说明\n", 200))
	ref := writeTestSkillFile(t, root, "demo/abc/references/table.md", strings.Repeat("参考资料\n", 200))
	tool := NewSkillReadTool([]SkillWorkspaceItem{{
		ID:   "demo",
		Name: "Demo",
		Files: []model.SkillFile{
			{RelativePath: "SKILL.md", StoragePath: entry, Kind: "entry", Size: 1},
			{RelativePath: "references/table.md", StoragePath: ref, Kind: "reference", Size: 1},
		},
	}})

	raw, err := tool.InvokableRun(context.Background(), `{"skill_id":"demo","path":"SKILL.md","max_chars":10}`)
	if err != nil {
		t.Fatalf("skill_read entry error: %v", err)
	}
	var entryOut SkillReadOutput
	if err := json.Unmarshal([]byte(raw), &entryOut); err != nil {
		t.Fatalf("unmarshal entry: %v", err)
	}
	if !strings.Contains(entryOut.Content, "入口说明") || !entryOut.Truncated || entryOut.NextOffset <= 0 {
		t.Fatalf("entry should be paged by the requested bound: %#v", entryOut)
	}

	raw, err = tool.InvokableRun(context.Background(), `{"skill_id":"demo","path":"references/table.md","max_chars":20}`)
	if err != nil {
		t.Fatalf("skill_read ref error: %v", err)
	}
	var refOut SkillReadOutput
	if err := json.Unmarshal([]byte(raw), &refOut); err != nil {
		t.Fatalf("unmarshal ref: %v", err)
	}
	if !refOut.Truncated || refOut.NextOffset <= 0 {
		t.Fatalf("reference should be paged: %#v", refOut)
	}
}

func TestSkillSearchOnlyUsesEnabledWorkspace(t *testing.T) {
	root := t.TempDir()
	entry := writeTestSkillFile(t, root, "demo/abc/SKILL.md", "入口\n")
	ref := writeTestSkillFile(t, root, "demo/abc/references/table.md", "五行 生克 表格")
	tool := NewSkillSearchTool([]SkillWorkspaceItem{{
		ID:   "demo",
		Name: "Demo",
		Files: []model.SkillFile{
			{RelativePath: "SKILL.md", StoragePath: entry, Kind: "entry", Size: 1},
			{RelativePath: "references/table.md", StoragePath: ref, Kind: "reference", Size: 1},
		},
	}})

	raw, err := tool.InvokableRun(context.Background(), `{"query":"生克"}`)
	if err != nil {
		t.Fatalf("skill_search error: %v", err)
	}
	var out SkillSearchOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal search: %v", err)
	}
	if len(out.Results) != 1 || out.Results[0].SkillID != "demo" || out.Results[0].Path != "references/table.md" {
		t.Fatalf("results = %#v", out.Results)
	}
}

func TestSkillListPaginatesSkillsAndAllFiles(t *testing.T) {
	files := make([]model.SkillFile, 0, 20)
	for i := 0; i < 20; i++ {
		files = append(files, model.SkillFile{RelativePath: "references/" + string(rune('a'+i)) + ".md", Kind: "reference"})
	}
	tool := NewSkillListTool([]SkillWorkspaceItem{{ID: "demo", Name: "Demo", Files: files}})

	raw, err := tool.InvokableRun(context.Background(), `{"max_items":1}`)
	if err != nil {
		t.Fatalf("skill_list error: %v", err)
	}
	var first SkillListOutput
	if err := json.Unmarshal([]byte(raw), &first); err != nil {
		t.Fatalf("unmarshal first page: %v", err)
	}
	if len(first.Skills) != 1 || !first.Skills[0].FilesTruncated || first.Skills[0].NextFileOffset == 0 {
		t.Fatalf("first page should expose a file continuation: %#v", first)
	}

	raw, err = tool.InvokableRun(context.Background(), `{"skill_id":"demo","offset":16,"max_items":4}`)
	if err != nil {
		t.Fatalf("skill_list continuation error: %v", err)
	}
	var second SkillListOutput
	if err := json.Unmarshal([]byte(raw), &second); err != nil {
		t.Fatalf("unmarshal continuation: %v", err)
	}
	if len(second.Skills) != 1 || len(second.Skills[0].Files) != 4 || second.HasMore {
		t.Fatalf("continuation should expose the remaining files: %#v", second)
	}
}

func TestSkillToolDescriptionsGuideEntryFileWorkflow(t *testing.T) {
	listInfo, err := NewSkillListTool(nil).Info(context.Background())
	if err != nil {
		t.Fatalf("skill_list Info() error: %v", err)
	}
	readInfo, err := NewSkillReadTool(nil).Info(context.Background())
	if err != nil {
		t.Fatalf("skill_read Info() error: %v", err)
	}
	searchInfo, err := NewSkillSearchTool(nil).Info(context.Background())
	if err != nil {
		t.Fatalf("skill_search Info() error: %v", err)
	}
	for _, tc := range []struct {
		name string
		desc string
		want string
	}{
		{"skill_list", listInfo.Desc, "bodies are not automatically in context"},
		{"skill_read", readInfo.Desc, "until truncated=false"},
		{"skill_search", searchInfo.Desc, "Do not use search as a substitute"},
	} {
		if !strings.Contains(tc.desc, tc.want) {
			t.Fatalf("%s description missing %q:\n%s", tc.name, tc.want, tc.desc)
		}
	}
}

func writeTestSkillFile(t *testing.T, root, rel, content string) string {
	t.Helper()
	path := filepath.Join(root, "storage", "skills", filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	old, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("Abs cwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(old)
	})
	if err := os.Chdir(root); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	return filepath.Join(filepolicy.SkillRoot, filepath.FromSlash(rel))
}
