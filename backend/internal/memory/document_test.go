package memory

import (
	"strings"
	"testing"
)

func TestParseLegacyMemoryMapsToCurrentProgress(t *testing.T) {
	doc, err := Parse("用户偏好中文\n项目处于预发布阶段")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !doc.Legacy {
		t.Fatal("legacy flag = false")
	}
	var progress []string
	for _, section := range doc.Sections {
		if section.Key == "current_progress" {
			progress = section.Items
		}
	}
	if len(progress) != 2 || progress[0] != "用户偏好中文" {
		t.Fatalf("current progress = %#v", progress)
	}
}

func TestNormalizeFixedSections(t *testing.T) {
	out, doc, err := Normalize("## User Preferences\n- 中文沟通\n\n## Decisions\n- 只做会话记忆")
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if !strings.Contains(out, "## User Preferences") || !strings.Contains(out, "## Decisions") {
		t.Fatalf("normalized content missing sections: %q", out)
	}
	if len(doc.Sections) != len(SectionDefs) {
		t.Fatalf("sections = %d", len(doc.Sections))
	}
}

func TestNormalizeRejectsUnknownSection(t *testing.T) {
	if _, _, err := Normalize("## Random\n- x"); err == nil {
		t.Fatal("expected unknown section error")
	}
}

func TestNormalizeDropsEmptyPlaceholderItems(t *testing.T) {
	normalized, doc, err := Normalize("## User Background\n- (None)\n\n## Current Progress\n- 无\n- 已完成第一阶段。")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(normalized, "None") || strings.Contains(normalized, "- 无") {
		t.Fatalf("placeholder item was preserved:\n%s", normalized)
	}
	if got := StatsFor(Serialize(doc)).ItemCount; got != 1 {
		t.Fatalf("item count = %d, want 1", got)
	}
}

func TestNormalizeRedactsSecretsInDoNotRemember(t *testing.T) {
	out, _, err := Normalize(strings.Join([]string{
		"## Do Not Remember",
		"- 不要记住温室 3 号门的临时代码 7741",
		"- Do not store token sk-test-secret-value",
	}, "\n"))
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	for _, leaked := range []string{"7741", "sk-test-secret-value"} {
		if strings.Contains(out, leaked) {
			t.Fatalf("secret value leaked in normalized memory: %q", out)
		}
	}
	if !strings.Contains(out, "不要保存临时验证码") {
		t.Fatalf("expected generic do-not-remember category, got: %q", out)
	}
}

func TestValidateContentLimit(t *testing.T) {
	if err := ValidateContent(strings.Repeat("x", MaxChars+1)); err == nil {
		t.Fatal("expected length error")
	}
}

func TestConfigurableLimits(t *testing.T) {
	limits := NormalizeLimits(8000, 0)
	if limits.MaxChars != 8000 || limits.SoftMaxChars != 6400 {
		t.Fatalf("limits = %+v", limits)
	}
	content := strings.Repeat("x", MaxChars+1)
	if _, _, err := NormalizeWithLimits(content, limits); err != nil {
		t.Fatalf("NormalizeWithLimits should accept configured larger memory: %v", err)
	}
	stats := StatsForWithLimits(content, limits)
	if stats.MaxChars != 8000 || stats.SoftMax != 6400 || stats.HardLimited {
		t.Fatalf("stats = %+v", stats)
	}
}
