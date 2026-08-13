package memory

import (
	"errors"
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

func TestNormalizeRejectsHighConfidenceSecretsOutsideDoNotRemember(t *testing.T) {
	tests := []string{
		"password=fixture-password-42",
		"api_key: sk-test-secret-value",
		"Authorization: Bearer fixture-bearer-token-123",
		"github_pat_11AA22BB33CC44DD55EE66FF77GG88HH99",
		"-----BEGIN PRIVATE KEY-----\nZmFrZS1wcml2YXRlLWtleQ==\n-----END PRIVATE KEY-----",
	}
	for _, secret := range tests {
		content := "## Project Context\n- " + secret
		_, _, err := Normalize(content)
		if !errors.Is(err, ErrSensitiveValue) {
			t.Fatalf("Normalize(%q) error = %v, want ErrSensitiveValue", secret, err)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaked rejected secret %q: %v", secret, err)
		}
	}
}

func TestNormalizeAllowsOrdinaryIdentifiers(t *testing.T) {
	content := strings.Join([]string{
		"## Project Context",
		"- Release ticket EC-2026-041 remains open.",
		"- Trace ID 550e8400-e29b-41d4-a716-446655440000.",
		"- Commit 0123456789abcdef0123456789abcdef01234567 passed.",
		"- Greenhouse door reference 7741 is a public location code.",
	}, "\n")
	if _, _, err := Normalize(content); err != nil {
		t.Fatalf("ordinary identifiers were rejected: %v", err)
	}
}

func TestRedactSensitiveValuesProtectsLegacyPromptContent(t *testing.T) {
	content := strings.Join([]string{
		"## Decisions",
		"- password=fixture-password-42",
		"- Authorization: Bearer fixture-bearer-token-123",
		"- Keep project EC-2026-041.",
	}, "\n")
	redacted := RedactSensitiveValues(content)
	for _, leaked := range []string{"fixture-password-42", "fixture-bearer-token-123"} {
		if strings.Contains(redacted, leaked) {
			t.Fatalf("redacted memory leaked %q: %s", leaked, redacted)
		}
	}
	if !strings.Contains(redacted, "EC-2026-041") || !strings.Contains(redacted, SensitiveValuePlaceholder) {
		t.Fatalf("redaction damaged ordinary content or omitted placeholder: %s", redacted)
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
