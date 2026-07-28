package skill

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseTextSkillFrontmatter(t *testing.T) {
	raw := []byte("---\nname: Review Helper\ndescription: Helps review code\n---\n\nUse a review-first workflow.")
	got, err := ParseTextSkill("skills/review/SKILL.md", raw)
	if err != nil {
		t.Fatalf("ParseTextSkill error: %v", err)
	}
	if got.ID != "review-helper" {
		t.Fatalf("ID = %q, want review-helper", got.ID)
	}
	if got.Name != "Review Helper" {
		t.Fatalf("Name = %q", got.Name)
	}
	if got.Description != "Helps review code" {
		t.Fatalf("Description = %q", got.Description)
	}
	if got.Checksum == "" {
		t.Fatal("Checksum is empty")
	}
}

func TestImportZipRejectsUnsafePath(t *testing.T) {
	data := makeZip(t, map[string]string{
		"../SKILL.md":             "bad",
		"skills/good/SKILL.md":    "---\nname: Good Skill\n---\n\nGood content.",
		"node_modules/x/SKILL.md": "ignored",
	})
	got, report, err := ImportZip(data)
	if err != nil {
		t.Fatalf("ImportZip error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "good-skill" {
		t.Fatalf("skills = %#v", got)
	}
	if len(report.Skipped) == 0 {
		t.Fatal("expected skipped unsafe path")
	}
}

func TestImportZipScansMultipleSkills(t *testing.T) {
	data := makeZip(t, map[string]string{
		"repo/skills/review/SKILL.md": "---\nname: Review\n---\n\nReview content.",
		"repo/skills/plan/SKILL.md":   "---\nname: Plan\n---\n\nPlan content.",
		"repo/skills/plan/README.md":  "---\nname: Plan Readme\n---\n\nIgnored content.",
	})
	got, report, err := ImportZip(data)
	if err != nil {
		t.Fatalf("ImportZip error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("skills len = %d, want 2, report=%#v", len(got), report)
	}
	if got[0].ID != "plan" || got[1].ID != "review" {
		t.Fatalf("skills = %#v", got)
	}
	if len(report.Details) == 0 {
		t.Fatalf("expected import details, report=%#v", report)
	}
}

func TestImportZipSelectedPathsOnly(t *testing.T) {
	data := makeZip(t, map[string]string{
		"repo/skills/review/SKILL.md": "---\nname: Review\n---\n\nReview content.",
		"repo/skills/plan/SKILL.md":   "---\nname: Plan\n---\n\nPlan content.",
	})
	got, report, err := ImportZipSelected(data, []string{"repo/skills/review/SKILL.md"})
	if err != nil {
		t.Fatalf("ImportZipSelected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "review" {
		t.Fatalf("skills = %#v report=%#v", got, report)
	}
}

func TestImportZipKeepsFullSkillContent(t *testing.T) {
	body := strings.Repeat("A full skill paragraph.\n", 200)
	data := makeZip(t, map[string]string{
		"repo/skills/long/SKILL.md": "---\nname: Long Skill\n---\n\n" + body,
	})
	got, report, err := ImportZip(data)
	if err != nil {
		t.Fatalf("ImportZip error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("skills len = %d, want 1, report=%#v", len(got), report)
	}
	want := strings.TrimSpace("---\nname: Long Skill\n---\n\n" + body)
	if got[0].Content != want {
		t.Fatalf("content was changed or truncated: got %d chars, want %d", len(got[0].Content), len(want))
	}
}

func TestParseTextSkillRejectsBinary(t *testing.T) {
	if _, err := ParseTextSkill("SKILL.md", []byte{0xff, 0x00, 0x01}); err == nil {
		t.Fatal("expected binary content to be rejected")
	}
}

func TestScanDirUsesCandidateFilesOnly(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "README.md"), "not a skill")
	mustWrite(t, filepath.Join(dir, "AGENTS.md"), "---\nname: Agent Rules\n---\n\nFollow the project rules.")
	mustWrite(t, filepath.Join(dir, "GEMINI.md"), "---\nname: Gemini Rules\n---\n\nFollow the project rules.")
	mustWrite(t, filepath.Join(dir, ".cursor", "rules", "x.mdc"), "---\nname: Cursor Rule\n---\n\nFollow the project rules.")
	mustWrite(t, filepath.Join(dir, "skills", "note.md"), "---\nname: Note\n---\n\nNot a skill.")
	mustWrite(t, filepath.Join(dir, "skills", "real", "SKILL.md"), "---\nname: Real Skill\n---\n\nUse this skill.")
	mustWrite(t, filepath.Join(dir, "node_modules", "x", "SKILL.md"), "ignored")

	got, report, err := ScanDir(dir)
	if err != nil {
		t.Fatalf("ScanDir error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "real-skill" {
		t.Fatalf("skills = %#v report=%#v", got, report)
	}
}

func TestImportZipCollectsExplicitSkillReferences(t *testing.T) {
	data := makeZip(t, map[string]string{
		"repo/bazi/SKILL.md":                    "---\nname: Bazi\n---\n\nRead `references/wuxing-tables.md` and references/dayun-rules.md when needed.",
		"repo/bazi/references/wuxing-tables.md": "五行表",
		"repo/bazi/references/dayun-rules.md":   "大运规则",
		"repo/bazi/references/unused.md":        "不应被打包",
	})
	got, report, err := ImportZip(data)
	if err != nil {
		t.Fatalf("ImportZip error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("skills len = %d, want 1, report=%#v", len(got), report)
	}
	if got[0].PackageChecksum == "" {
		t.Fatal("package checksum is empty")
	}
	paths := make([]string, 0, len(got[0].Files))
	for _, file := range got[0].Files {
		paths = append(paths, file.Path)
	}
	wantPaths := []string{"repo/bazi/SKILL.md"}
	if strings.Join(paths, "|") != strings.Join(wantPaths, "|") {
		t.Fatalf("files = %#v, want %#v", paths, wantPaths)
	}
	if !containsParsedFile(got[0].AvailableFiles, "repo/bazi/references/wuxing-tables.md", "explicit", false) {
		t.Fatalf("explicit reference not available for selection: %#v", got[0].AvailableFiles)
	}
	if !containsParsedFile(got[0].AvailableFiles, "repo/bazi/references/unused.md", "candidate", false) {
		t.Fatalf("candidate reference not available for selection: %#v", got[0].AvailableFiles)
	}

	got, report, err = ImportZipSelectedFiles(data, []string{"repo/bazi/SKILL.md"}, map[string][]string{
		"repo/bazi/SKILL.md": {"references/wuxing-tables.md"},
	})
	if err != nil {
		t.Fatalf("ImportZipSelectedFiles error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("selected skills len = %d, report=%#v", len(got), report)
	}
	if !containsParsedFile(got[0].Files, "repo/bazi/references/wuxing-tables.md", "selected", false) {
		t.Fatalf("selected explicit reference not imported: %#v", got[0].Files)
	}
	if containsParsedPath(got[0].Files, "repo/bazi/references/dayun-rules.md") {
		t.Fatalf("unselected explicit reference should not be imported: %#v", got[0].Files)
	}
}

func TestImportZipReportsMissingSkillReference(t *testing.T) {
	data := makeZip(t, map[string]string{
		"repo/bazi/SKILL.md": "---\nname: Bazi\n---\n\nRead references/missing.md.",
	})
	got, report, err := ImportZip(data)
	if err != nil {
		t.Fatalf("ImportZip error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("skills len = %d, want 1", len(got))
	}
	if len(got[0].Files) != 1 {
		t.Fatalf("files len = %d, want entry only", len(got[0].Files))
	}
	if !containsParsedFile(got[0].AvailableFiles, "repo/bazi/references/missing.md", "explicit", false) {
		t.Fatalf("missing explicit dependency not shown as selectable warning: %#v", got[0].AvailableFiles)
	}

	got, report, err = ImportZipSelectedFiles(data, []string{"repo/bazi/SKILL.md"}, map[string][]string{
		"repo/bazi/SKILL.md": {"references/missing.md"},
	})
	if err != nil {
		t.Fatalf("ImportZipSelectedFiles error: %v", err)
	}
	if len(got) != 1 || len(got[0].Files) != 1 {
		t.Fatalf("selected missing dependency should keep entry only: skills=%#v report=%#v", got, report)
	}
	if !containsText(report.Skipped, "references/missing.md") {
		t.Fatalf("selected missing dependency should be reported: %#v", report.Skipped)
	}
}

func TestImportZipOptionalNearbyFilesRequireSelection(t *testing.T) {
	data := makeZip(t, map[string]string{
		"repo/skill/SKILL.md":       "---\nname: Optional\n---\n\nUse this skill.",
		"repo/skill/docs/extra.md":  "额外资料",
		"repo/skill/knowledge/a.md": "知识资料",
	})
	got, report, err := ImportZipSelectedFiles(data, []string{"repo/skill/SKILL.md"}, nil)
	if err != nil {
		t.Fatalf("ImportZipSelectedFiles error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("skills len = %d, report=%#v", len(got), report)
	}
	if len(got[0].Files) != 1 {
		t.Fatalf("default files = %#v, want entry only", got[0].Files)
	}
	if !containsParsedFile(got[0].AvailableFiles, "repo/skill/docs/extra.md", "candidate", false) {
		t.Fatalf("candidate file not available as optional: %#v", got[0].AvailableFiles)
	}

	got, _, err = ImportZipSelectedFiles(data, []string{"repo/skill/SKILL.md"}, map[string][]string{
		"repo/skill/SKILL.md": {"docs/extra.md"},
	})
	if err != nil {
		t.Fatalf("ImportZipSelectedFiles selected error: %v", err)
	}
	if !containsParsedFile(got[0].Files, "repo/skill/docs/extra.md", "selected", false) {
		t.Fatalf("selected nearby file not imported: %#v", got[0].Files)
	}
}

func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("Create zip entry: %v", err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("Write zip entry: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("Close zip: %v", err)
	}
	return buf.Bytes()
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func containsText(values []string, part string) bool {
	for _, value := range values {
		if strings.Contains(value, part) {
			return true
		}
	}
	return false
}

func containsParsedFile(files []ParsedSkillFile, path, reason string, selectedDefault bool) bool {
	for _, file := range files {
		if file.Path == path && file.Reason == reason && file.SelectedDefault == selectedDefault {
			return true
		}
	}
	return false
}

func containsParsedPath(files []ParsedSkillFile, path string) bool {
	for _, file := range files {
		if file.Path == path {
			return true
		}
	}
	return false
}
