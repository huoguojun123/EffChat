package service

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/huoguojun123/EffChat/internal/model"
	skillparser "github.com/huoguojun123/EffChat/internal/skill"
)

type fixedSkillResolver struct {
	ips []net.IPAddr
}

func (r fixedSkillResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return r.ips, nil
}

func TestValidateGitURLRejectsBlockedNetworkTargets(t *testing.T) {
	resolver := fixedSkillResolver{ips: []net.IPAddr{{IP: net.ParseIP("198.18.0.1")}}}
	if err := validateGitURLWithResolver(context.Background(), resolver, "https://git.example/skill.git"); err == nil {
		t.Fatal("expected benchmark network git URL to be rejected")
	}
	for _, raw := range []string{"http://127.0.0.1/repo.git", "https://user:secret@git.example/repo.git", "ssh://git.example/repo.git"} {
		if err := validateGitURLWithResolver(context.Background(), resolver, raw); err == nil {
			t.Fatalf("expected %q to be rejected", raw)
		}
	}
}

func TestValidateGitURLAllowsTrustedHostBehindFakeDNS(t *testing.T) {
	resolver := fixedSkillResolver{ips: []net.IPAddr{{IP: net.ParseIP("198.18.0.27")}}}
	if err := validateGitURLWithResolver(context.Background(), resolver, "https://github.com/obra/superpowers.git"); err != nil {
		t.Fatalf("trusted GitHub URL should not be rejected by local fake DNS: %v", err)
	}
	if err := validateGitURLWithResolver(context.Background(), resolver, "https://mirror.github.com/obra/superpowers.git"); err == nil {
		t.Fatal("untrusted lookalike host should retain DNS validation")
	}
	if err := validateGitURLWithResolver(context.Background(), resolver, "https://github.com:8443/obra/superpowers.git"); err == nil {
		t.Fatal("trusted host must not allow an arbitrary port")
	}
}

func TestCleanupExpiredSkillPackagesKeepsActiveAndRecentPackages(t *testing.T) {
	root := t.TempDir()
	oldInactive := filepath.Join(root, "demo", "old")
	oldActive := filepath.Join(root, "demo", "active")
	recentInactive := filepath.Join(root, "demo", "recent")
	for _, path := range []string{oldInactive, oldActive, recentInactive} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	old := now.Add(-skillPackageGracePeriod - time.Minute)
	for _, path := range []string{oldInactive, oldActive} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	cleanupExpiredSkillPackageRoots(root, map[string]struct{}{oldActive: {}}, now)
	if _, err := os.Stat(oldInactive); !os.IsNotExist(err) {
		t.Fatalf("expired inactive package still exists: %v", err)
	}
	for _, path := range []string{oldActive, recentInactive} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("package %q should be retained: %v", path, err)
		}
	}
}

func TestScheduleSkillPackageCleanupCoalescesPendingSweeps(t *testing.T) {
	service := &SkillService{}
	service.scheduleSkillPackageCleanup()
	first := service.cleanupTimer
	firstGeneration := service.cleanupGeneration
	if first == nil {
		t.Fatal("cleanup timer was not scheduled")
	}
	t.Cleanup(func() { first.Stop() })

	service.scheduleSkillPackageCleanup()
	second := service.cleanupTimer
	if second == nil || second == first {
		t.Fatal("cleanup timer was not rescheduled for the latest package change")
	}
	t.Cleanup(func() { second.Stop() })

	service.runScheduledSkillPackageCleanup(firstGeneration)
	service.cleanupMu.Lock()
	pending := service.cleanupTimer
	service.cleanupMu.Unlock()
	if pending != second {
		t.Fatal("a stale cleanup callback cleared the latest timer")
	}
}

func TestWriteSkillPackageReusesCompleteChecksumDirectory(t *testing.T) {
	previousRoot := skillUploadDir
	skillUploadDir = t.TempDir()
	t.Cleanup(func() { skillUploadDir = previousRoot })
	parsed := []skillparser.ParsedSkillFile{{
		Path:     "SKILL.md",
		Content:  "---\nname: Demo\n---\n",
		Kind:     "entry",
		Checksum: "entry",
	}}
	files, root, err := writeSkillPackage("demo", "same-package", parsed)
	if err != nil {
		t.Fatalf("first write: %v", err)
	}
	marker := filepath.Join(root, "run-snapshot-marker")
	if err := os.WriteFile(marker, []byte("retain"), 0o600); err != nil {
		t.Fatal(err)
	}
	reusedFiles, reusedRoot, err := writeSkillPackage("demo", "same-package", parsed)
	if err != nil {
		t.Fatalf("second write: %v", err)
	}
	if reusedRoot != root || len(reusedFiles) != len(files) {
		t.Fatalf("package was not reused: root=%q files=%#v", reusedRoot, reusedFiles)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("same checksum package was rewritten: %v", err)
	}
}

func TestParseGitRemoteRefsAndDefault(t *testing.T) {
	raw := "ref: refs/heads/main\tHEAD\nabc123\tHEAD\nabc123\trefs/heads/main\ndef456\trefs/heads/dev\n"

	branches := parseGitRemoteRefs(raw)
	if len(branches) != 2 || branches[0] != "dev" || branches[1] != "main" {
		t.Fatalf("branches = %#v", branches)
	}
	if got := parseGitDefaultRef(raw); got != "main" {
		t.Fatalf("default = %q, want main", got)
	}
	if got := selectGitRef("", "main", branches); got != "main" {
		t.Fatalf("selected = %q, want main", got)
	}
	if got := selectGitRef("dev", "main", branches); got != "dev" {
		t.Fatalf("selected requested = %q, want dev", got)
	}
}

func TestDedupeParsedSkillsUsesLastSkillForSameID(t *testing.T) {
	report := skillparser.ImportReport{}
	parsed := []skillparser.ParsedSkill{
		{ID: "review", Name: "Review", Content: "old", SourcePath: "a/SKILL.md"},
		{ID: "review", Name: "Review", Content: "new", SourcePath: "b/SKILL.md"},
		{ID: "plan", Name: "Plan", Content: "plan", SourcePath: "c/SKILL.md"},
	}

	got := dedupeParsedSkills(parsed, &report)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].ID != "review" || got[0].Content != "new" {
		t.Fatalf("review skill = %#v", got[0])
	}
	if len(report.Deduped) != 1 {
		t.Fatalf("deduped = %#v", report.Deduped)
	}
}

func TestSkillSparseCheckoutPatternsStayTextOnly(t *testing.T) {
	patterns := skillSparseCheckoutPatterns()
	required := []string{"/SKILL.md", "/*/SKILL.md", "/*/*/SKILL.md"}
	for _, want := range required {
		if !contains(patterns, want) {
			t.Fatalf("patterns missing %q: %#v", want, patterns)
		}
	}
	for _, forbidden := range []string{"/**", "*", "/AGENTS.md", "/GEMINI.md", "/skills/**", "/.cursor/rules/**"} {
		if contains(patterns, forbidden) {
			t.Fatalf("patterns include broad checkout %q: %#v", forbidden, patterns)
		}
	}
}

func TestSelectedSkillFileSparseCheckoutPatternsOnlyUsesAdminSelection(t *testing.T) {
	parsed := []skillparser.ParsedSkill{{
		SourcePath: "repo/bazi/SKILL.md",
		AvailableFiles: []skillparser.ParsedSkillFile{
			{Path: "repo/bazi/SKILL.md", Kind: "entry", Reason: "entry", SelectedDefault: true},
			{Path: "repo/bazi/references/explicit.md", Kind: "reference", Reason: "explicit", SelectedDefault: false},
			{Path: "repo/bazi/references/default-old.md", Kind: "reference", Reason: "candidate", SelectedDefault: true},
			{Path: "repo/bazi/references/selected.md", Kind: "reference", Reason: "selected", SelectedDefault: false},
		},
	}}

	got := selectedSkillFileSparseCheckoutPatterns(parsed)
	if len(got) != 1 || got[0] != "/repo/bazi/references/selected.md" {
		t.Fatalf("patterns = %#v, want selected reference only", got)
	}
}

func TestParseManualSkillAllowsAdminSelectedReference(t *testing.T) {
	parsed, err := parseManualSkill(&SkillInput{
		ID:           "manual-demo",
		Name:         "Manual Demo",
		EntryContent: "---\nname: Manual Demo\n---\n\nUse this skill.",
		Files: []SkillFileInput{{
			Path:    "references/extra.md",
			Content: "管理员额外选择的参考资料",
		}},
	})
	if err != nil {
		t.Fatalf("parseManualSkill error: %v", err)
	}
	if len(parsed.Files) != 2 {
		t.Fatalf("files len = %d, want entry + selected reference", len(parsed.Files))
	}
	found := false
	for _, file := range parsed.Files {
		if file.Path == "references/extra.md" {
			found = true
		}
	}
	if !found {
		t.Fatalf("selected manual reference missing: %#v", parsed.Files)
	}
}

func TestParseManualSkillReportsMissingReferencedFile(t *testing.T) {
	_, err := parseManualSkill(&SkillInput{
		ID:           "manual-demo",
		Name:         "Manual Demo",
		EntryContent: "---\nname: Manual Demo\n---\n\nRead references/missing.md first.",
	})
	if err == nil {
		t.Fatal("expected missing referenced file error")
	}
}

func TestMatchExistingSkillPrefersIDThenName(t *testing.T) {
	existing := []*model.Skill{
		{ID: "bazi", Name: "Bazi"},
		{ID: "other", Name: "Same Name"},
	}
	if skill, matchType, action := matchExistingSkill(skillparser.ParsedSkill{ID: "bazi", Name: "Renamed"}, existing); skill == nil || skill.ID != "bazi" || matchType != "id" || action != "update" {
		t.Fatalf("id match = %#v %q %q", skill, matchType, action)
	}
	if skill, matchType, action := matchExistingSkill(skillparser.ParsedSkill{ID: "new-id", Name: "same name"}, existing); skill == nil || skill.ID != "other" || matchType != "name" || action != "review" {
		t.Fatalf("name match = %#v %q %q", skill, matchType, action)
	}
	if skill, _, action := matchExistingSkill(skillparser.ParsedSkill{ID: "fresh", Name: "Fresh"}, existing); skill != nil || action != "create" {
		t.Fatalf("new match = %#v action=%q", skill, action)
	}
}

func TestCompareSkillFilesDefaultsOnlySamePathReferences(t *testing.T) {
	current := []model.SkillFile{
		{RelativePath: "SKILL.md", Kind: "entry", Checksum: "old-entry", Size: 10},
		{RelativePath: "references/a.md", Kind: "reference", Checksum: "same", Size: 20},
		{RelativePath: "references/b.md", Kind: "reference", Checksum: "old", Size: 30},
		{RelativePath: "references/missing.md", Kind: "reference", Checksum: "gone", Size: 40},
	}
	candidate := []SkillFileResponse{
		{Path: "SKILL.md", Kind: "entry", Checksum: "new-entry", Size: 11},
		{Path: "references/a.md", Kind: "reference", Checksum: "same", Size: 20},
		{Path: "references/b.md", Kind: "reference", Checksum: "new", Size: 31},
		{Path: "references/c.md", Kind: "reference", Checksum: "new-file", Size: 12},
	}
	selected := defaultSelectedReferencePaths(current, candidate)
	if len(selected) != 2 || selected[0] != "references/a.md" || selected[1] != "references/b.md" {
		t.Fatalf("selected = %#v", selected)
	}
	changes := compareSkillFiles(current, candidate)
	status := map[string]string{}
	for _, change := range changes {
		status[change.Path] = change.Status
	}
	want := map[string]string{
		"SKILL.md":              "entry",
		"references/a.md":       "unchanged",
		"references/b.md":       "modified",
		"references/c.md":       "added",
		"references/missing.md": "missing",
	}
	for path, expected := range want {
		if status[path] != expected {
			t.Fatalf("status[%s] = %q, want %q; changes=%#v", path, status[path], expected, changes)
		}
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
