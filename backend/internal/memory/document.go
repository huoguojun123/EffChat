package memory

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	MaxChars      = 4000
	SoftMaxChars  = 3200
	MaxChangeList = 20
)

type Limits struct {
	MaxChars     int
	SoftMaxChars int
}

func DefaultLimits() Limits {
	return NormalizeLimits(MaxChars, SoftMaxChars)
}

func NormalizeLimits(maxChars, softMaxChars int) Limits {
	if maxChars <= 0 {
		maxChars = MaxChars
	}
	if softMaxChars <= 0 {
		softMaxChars = maxChars * 4 / 5
	}
	if softMaxChars <= 0 {
		softMaxChars = maxChars
	}
	if softMaxChars > maxChars {
		softMaxChars = maxChars
	}
	return Limits{MaxChars: maxChars, SoftMaxChars: softMaxChars}
}

type SectionDef struct {
	Key   string `json:"key"`
	Title string `json:"title"`
}

var SectionDefs = []SectionDef{
	{Key: "user_background", Title: "User Background"},
	{Key: "user_preferences", Title: "User Preferences"},
	{Key: "project_context", Title: "Project Context"},
	{Key: "current_progress", Title: "Current Progress"},
	{Key: "decisions", Title: "Decisions"},
	{Key: "do_not_remember", Title: "Do Not Remember"},
}

type Section struct {
	Key   string   `json:"key"`
	Title string   `json:"title"`
	Items []string `json:"items"`
}

type Document struct {
	Sections []Section `json:"sections"`
	Legacy   bool      `json:"legacy,omitempty"`
}

type Stats struct {
	Chars       int  `json:"chars"`
	MaxChars    int  `json:"max_chars"`
	SoftMax     int  `json:"soft_max_chars"`
	NearLimit   bool `json:"near_limit"`
	HardLimited bool `json:"hard_limited"`
	ItemCount   int  `json:"item_count"`
}

var sectionHeaderRE = regexp.MustCompile(`^#{1,3}\s+(.+?)\s*$`)
var doNotRememberSensitiveValueRE = regexp.MustCompile(`(?i)(sk-[a-z0-9_-]{8,}|[a-z0-9][a-z0-9_-]{19,}|[0-9]{4,})`)

const doNotRememberSecretPlaceholder = "不要保存临时验证码、密码、API key、token 或其他秘密值。"

const SensitiveValuePlaceholder = "[REDACTED SECRET]"

var ErrSensitiveValue = errors.New("memory contains a secret value; remove passwords, tokens, API keys, authorization credentials, or private keys before saving")

type sensitiveValuePattern struct {
	replacement string
	re          *regexp.Regexp
}

// sensitiveValuePatterns intentionally recognizes only credential-shaped values. Memory is
// durable and repeatedly sent upstream, so known tokens and explicit credential assignments are
// blocked, while UUIDs, commit hashes, project numbers, and other opaque identifiers remain valid.
var sensitiveValuePatterns = []sensitiveValuePattern{
	{
		re:          regexp.MustCompile(`(?s)-----BEGIN (?:[A-Z0-9 ]+ )?PRIVATE KEY-----.*?-----END (?:[A-Z0-9 ]+ )?PRIVATE KEY-----`),
		replacement: SensitiveValuePlaceholder,
	},
	{
		re:          regexp.MustCompile(`(?i)\b((?:postgres(?:ql)?|mysql|mongodb(?:\+srv)?|redis|amqp)://[^/\s:@]+:)([^@\s/]+)(@)`),
		replacement: `${1}` + SensitiveValuePlaceholder + `${3}`,
	},
	{
		re:          regexp.MustCompile(`(?i)(\bauthorization\s*:\s*bearer\s+)([^\s,;]+)`),
		replacement: `${1}` + SensitiveValuePlaceholder,
	},
	{
		re:          regexp.MustCompile(`(?im)(^|[\s([{,;])(password|passwd|pwd|api[\s_-]*key|client[\s_-]*secret|access[\s_-]*token|refresh[\s_-]*token|auth[\s_-]*token|private[\s_-]*key|密码|口令|令牌|api\s*密钥)(\s*[:=：]\s*)(["']?)([^\s"'\x60,;]+)(["']?)`),
		replacement: `${1}${2}${3}` + SensitiveValuePlaceholder,
	},
	{
		re:          regexp.MustCompile(`(?i)\b(?:sk-[a-z0-9][a-z0-9_-]{11,}|github_pat_[a-z0-9_]{20,}|gh[opusr]_[a-z0-9]{20,}|(?:AKIA|ASIA)[A-Z0-9]{16}|AIza[0-9A-Za-z_-]{35}|xox[baprs]-[a-z0-9-]{10,}|(?:sk|rk)_live_[a-z0-9]{12,}|eyJ[a-z0-9_-]{8,}\.[a-z0-9_-]{8,}\.[a-z0-9_-]{8,})\b`),
		replacement: SensitiveValuePlaceholder,
	},
}

func EmptyDocument() Document {
	sections := make([]Section, 0, len(SectionDefs))
	for _, def := range SectionDefs {
		sections = append(sections, Section{Key: def.Key, Title: def.Title, Items: []string{}})
	}
	return Document{Sections: sections}
}

func Parse(content string) (Document, error) {
	content = strings.TrimSpace(content)
	doc := EmptyDocument()
	if content == "" {
		return doc, nil
	}

	byTitle := map[string]int{}
	byKey := map[string]int{}
	for i, section := range doc.Sections {
		byTitle[strings.ToLower(section.Title)] = i
		byKey[section.Key] = i
	}

	lines := strings.Split(content, "\n")
	current := -1
	seenHeader := false
	legacy := make([]string, 0)
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if match := sectionHeaderRE.FindStringSubmatch(line); match != nil {
			title := strings.TrimSpace(match[1])
			if idx, ok := byTitle[strings.ToLower(title)]; ok {
				current = idx
				seenHeader = true
				continue
			}
			if idx, ok := byKey[NormalizeSectionKey(title)]; ok {
				current = idx
				seenHeader = true
				continue
			}
			item := cleanItem(line)
			if current >= 0 {
				doc.Sections[current].Items = append(doc.Sections[current].Items, item)
			} else {
				legacy = append(legacy, item)
			}
			continue
		}
		item := cleanItem(line)
		if item == "" {
			continue
		}
		if current >= 0 {
			doc.Sections[current].Items = append(doc.Sections[current].Items, item)
		} else {
			legacy = append(legacy, item)
		}
	}
	if len(legacy) > 0 {
		idx := byKey["current_progress"]
		doc.Sections[idx].Items = append(legacy, doc.Sections[idx].Items...)
	}
	doc.Legacy = !seenHeader && len(legacy) > 0
	return doc, nil
}

func Normalize(content string) (string, Document, error) {
	return NormalizeWithLimits(content, DefaultLimits())
}

func NormalizeWithLimits(content string, limits Limits) (string, Document, error) {
	if err := rejectUnknownSections(content); err != nil {
		return "", Document{}, err
	}
	doc, err := Parse(content)
	if err != nil {
		return "", Document{}, err
	}
	if err := rejectSensitiveValues(doc); err != nil {
		return "", Document{}, err
	}
	normalized := Serialize(doc)
	if err := ValidateContentWithLimits(normalized, limits); err != nil {
		return "", Document{}, err
	}
	normalizedDoc, _ := Parse(normalized)
	return normalized, normalizedDoc, nil
}

func rejectUnknownSections(content string) error {
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		match := sectionHeaderRE.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		title := strings.TrimSpace(match[1])
		if NormalizeSectionKey(title) == "" {
			return fmt.Errorf("unknown memory section %q", title)
		}
	}
	return nil
}

func Serialize(doc Document) string {
	byKey := map[string]Section{}
	for _, section := range doc.Sections {
		key := NormalizeSectionKey(firstNonEmpty(section.Key, section.Title))
		if key == "" {
			continue
		}
		cleaned := make([]string, 0, len(section.Items))
		if key == "do_not_remember" && containsSensitiveValue(strings.Join(section.Items, "\n")) {
			section.Items = []string{doNotRememberSecretPlaceholder}
		}
		for _, item := range section.Items {
			if item = cleanItem(item); item != "" {
				if key == "do_not_remember" {
					item = sanitizeDoNotRememberItem(item)
				}
				cleaned = append(cleaned, item)
			}
		}
		section.Items = cleaned
		byKey[key] = section
	}

	var b strings.Builder
	for _, def := range SectionDefs {
		section := byKey[def.Key]
		if len(section.Items) == 0 {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("## ")
		b.WriteString(def.Title)
		b.WriteString("\n")
		for _, item := range section.Items {
			b.WriteString("- ")
			b.WriteString(item)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func rejectSensitiveValues(doc Document) error {
	for _, section := range doc.Sections {
		if NormalizeSectionKey(firstNonEmpty(section.Key, section.Title)) == "do_not_remember" {
			continue
		}
		if containsSensitiveValue(strings.Join(section.Items, "\n")) {
			return ErrSensitiveValue
		}
	}
	return nil
}

func containsSensitiveValue(content string) bool {
	for _, pattern := range sensitiveValuePatterns {
		if pattern.re.MatchString(content) {
			return true
		}
	}
	return false
}

// RedactSensitiveValues is the fail-safe boundary for legacy memory already stored before the
// write guard existed. It preserves ordinary context but never sends recognized credential values
// to model prompts or copies them into durable change history.
func RedactSensitiveValues(content string) string {
	for _, pattern := range sensitiveValuePatterns {
		content = pattern.re.ReplaceAllString(content, pattern.replacement)
	}
	return content
}

func StatsFor(content string) Stats {
	return StatsForWithLimits(content, DefaultLimits())
}

func StatsForWithLimits(content string, limits Limits) Stats {
	limits = NormalizeLimits(limits.MaxChars, limits.SoftMaxChars)
	doc, err := Parse(content)
	count := 0
	if err == nil {
		for _, section := range doc.Sections {
			count += len(section.Items)
		}
	}
	chars := utf8.RuneCountInString(strings.TrimSpace(content))
	return Stats{
		Chars:       chars,
		MaxChars:    limits.MaxChars,
		SoftMax:     limits.SoftMaxChars,
		NearLimit:   chars >= limits.SoftMaxChars,
		HardLimited: chars >= limits.MaxChars,
		ItemCount:   count,
	}
}

func ValidateContent(content string) error {
	return ValidateContentWithLimits(content, DefaultLimits())
}

func ValidateContentWithLimits(content string, limits Limits) error {
	limits = NormalizeLimits(limits.MaxChars, limits.SoftMaxChars)
	if utf8.RuneCountInString(strings.TrimSpace(content)) > limits.MaxChars {
		return fmt.Errorf("memory content too long; limit %d characters", limits.MaxChars)
	}
	return nil
}

func NormalizeSectionKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	for _, def := range SectionDefs {
		if value == def.Key || value == strings.ToLower(strings.ReplaceAll(def.Title, " ", "_")) {
			return def.Key
		}
	}
	return ""
}

func SectionTitle(key string) string {
	key = NormalizeSectionKey(key)
	for _, def := range SectionDefs {
		if def.Key == key {
			return def.Title
		}
	}
	return ""
}

func Summary(before, after string) string {
	beforeStats := StatsFor(before)
	afterStats := StatsFor(after)
	switch {
	case strings.TrimSpace(before) == "" && strings.TrimSpace(after) == "":
		return "会话记忆未变化"
	case strings.TrimSpace(after) == "":
		return "已清空会话记忆"
	case strings.TrimSpace(before) == "":
		return fmt.Sprintf("已创建 %d 条会话记忆", afterStats.ItemCount)
	default:
		return fmt.Sprintf("已更新会话记忆：%d 条变为 %d 条", beforeStats.ItemCount, afterStats.ItemCount)
	}
}

func cleanItem(line string) string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "- ")
	line = strings.TrimPrefix(line, "* ")
	line = strings.TrimSpace(line)
	switch strings.ToLower(line) {
	case "none", "(none)", "n/a", "(n/a)", "无", "无条目", "暂无":
		return ""
	default:
		return line
	}
}

func sanitizeDoNotRememberItem(item string) string {
	if doNotRememberSensitiveValueRE.MatchString(item) {
		return doNotRememberSecretPlaceholder
	}
	return item
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
