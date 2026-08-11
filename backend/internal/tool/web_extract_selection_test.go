package tool

import (
	"fmt"
	"strings"
	"testing"
)

func TestSelectBasicContentUsesGoalInsteadOfOnlyDocumentPrefix(t *testing.T) {
	content := strings.Join([]string{
		"Fixture manual",
		"开头是安装步骤和常规导航说明。",
		"这一节只讨论视觉主题和键盘快捷键。",
		"Quota 恢复必须确保 reservation exactly once 释放，然后才能重试运行。",
		"Closing appendix with unrelated acknowledgements.",
	}, "\n\n")
	selected, truncated := selectBasicContent(content, "配额 quota 恢复时如何释放 reservation", 120)
	if !truncated || len([]rune(selected)) > 120 {
		t.Fatalf("truncated=%t runes=%d content=%q", truncated, len([]rune(selected)), selected)
	}
	if !strings.Contains(selected, "Quota 恢复") {
		t.Fatalf("goal-relevant paragraph missing: %q", selected)
	}
	if strings.Contains(selected, "开头是安装步骤") {
		t.Fatalf("document prefix should not displace the goal match and its neighbor: %q", selected)
	}
}

func TestSelectBasicContentWithoutQuerySamplesAcrossDocument(t *testing.T) {
	blocks := make([]string, 12)
	for index := range blocks {
		blocks[index] = fmt.Sprintf("section-%02d %s", index, strings.Repeat("x", 24))
	}
	selected, truncated := selectBasicContent(strings.Join(blocks, "\n\n"), "", 120)
	if !truncated || len([]rune(selected)) > 120 {
		t.Fatalf("truncated=%t runes=%d content=%q", truncated, len([]rune(selected)), selected)
	}
	for _, want := range []string{"section-00", "section-03", "section-06", "section-09"} {
		if !strings.Contains(selected, want) {
			t.Fatalf("distributed fallback missing %q: %q", want, selected)
		}
	}
}

func TestSelectBasicContentKeepsOversizedTopHit(t *testing.T) {
	content := strings.Join([]string{
		"short unrelated introduction",
		strings.Repeat("quota recovery reservation exactly once ", 20),
		"short unrelated appendix",
	}, "\n\n")
	selected, truncated := selectBasicContent(content, "quota recovery reservation", 90)
	if !truncated || len([]rune(selected)) > 90 || !strings.Contains(selected, "quota recovery") {
		t.Fatalf("selected=%q truncated=%t", selected, truncated)
	}
	if strings.Contains(selected, "unrelated introduction") && !strings.Contains(selected, "exactly once") {
		t.Fatalf("neighbor displaced the oversized top hit: %q", selected)
	}
}

func TestSelectBasicContentKeepsRankedHitsBeforeNeighbors(t *testing.T) {
	content := strings.Join([]string{
		"neighbor context with no matching terms",
		"quota alpha primary ranked passage",
		"another unrelated neighbor context",
		"ordinary document spacer",
		"quota beta second ranked passage",
	}, "\n\n")
	selected, truncated := selectBasicContent(content, "quota", 76)
	if !truncated || len([]rune(selected)) > 76 {
		t.Fatalf("selected=%q truncated=%t", selected, truncated)
	}
	for _, want := range []string{"quota alpha", "quota beta"} {
		if !strings.Contains(selected, want) {
			t.Fatalf("ranked passage %q was displaced by a neighbor: %q", want, selected)
		}
	}
}

func TestFinalizeBasicLongContentRefinesBM25Candidate(t *testing.T) {
	logs := captureToolLog(t)
	mock := &mockSummarizer{summary: "focused summary"}
	tool := NewWebExtractTool(WebExtractConfig{MaxContent: 80, Summarizer: mock, SummaryEnabled: true})
	tool.rawContentLimit = 180
	content := strings.Join([]string{
		strings.Repeat("opening material ", 20),
		strings.Repeat("unrelated themes ", 20),
		"quota recovery requires reservation exactly once release before retry",
		strings.Repeat("appendix material ", 20),
	}, "\n\n")

	output, err := tool.finalizeContent(t.Context(), WebExtractOutput{
		OK: true, Source: "basic", Title: "Fixture", Content: content,
	}, "quota recovery reservation", extractDetailSummary)
	if err != nil {
		t.Fatalf("finalizeContent() error = %v", err)
	}
	if !mock.called || !output.Summarized || output.Content != mock.summary {
		t.Fatalf("output=%#v summarizer_called=%t", output, mock.called)
	}
	if output.Truncated {
		t.Fatalf("candidate selection must not mark a complete successful summary truncated: %#v", output)
	}
	if len([]rune(mock.gotContent)) > tool.rawContentLimit || !strings.Contains(mock.gotContent, "quota recovery") {
		t.Fatalf("refinement input was not a bounded relevant candidate: %q", mock.gotContent)
	}
	requireToolLogContains(t, logs.String(), "basic_refine_called", "input_chars=")
}

func TestFinalizeBasicSuccessfulSummaryPreservesSourceTruncation(t *testing.T) {
	mock := &mockSummarizer{summary: "focused summary"}
	tool := NewWebExtractTool(WebExtractConfig{MaxContent: 80, Summarizer: mock, SummaryEnabled: true})
	output, err := tool.finalizeContent(t.Context(), WebExtractOutput{
		OK: true, Source: "basic", Title: "Fixture", Content: strings.Repeat("quota recovery source ", 20), Truncated: true,
	}, "quota recovery", extractDetailSummary)
	if err != nil {
		t.Fatalf("finalizeContent() error = %v", err)
	}
	if !output.Summarized || !output.Truncated || !output.Degraded || output.DegradationReason != RefinementSourceTruncated {
		t.Fatalf("output=%#v, want summarized source-truncated result", output)
	}
}

func TestFinalizeBasicLongContentUsesLocalFallbackWhenRefinementFails(t *testing.T) {
	mock := &mockSummarizer{err: errSummarize}
	tool := NewWebExtractTool(WebExtractConfig{MaxContent: 100, Summarizer: mock, SummaryEnabled: true})
	content := strings.Join([]string{
		strings.Repeat("opening material ", 12),
		"unrelated themes and keyboard shortcuts",
		"配额恢复必须确保 reservation exactly once 释放，然后才能重试运行。",
		"unrelated appendix",
	}, "\n\n")

	output, err := tool.finalizeContent(t.Context(), WebExtractOutput{
		OK: true, Source: "basic", Title: "配额恢复 reservation", Content: content,
	}, "", extractDetailSummary)
	if err != nil {
		t.Fatalf("finalizeContent() error = %v", err)
	}
	if !output.RefinementAttempted || !output.Degraded || output.DegradationReason != RefinementFailed {
		t.Fatalf("output=%#v, want explicit refinement failure", output)
	}
	if !strings.Contains(output.Content, "配额恢复") || len([]rune(output.Content)) > 100 || !output.Truncated {
		t.Fatalf("local fallback lost the relevant bounded source: %#v", output)
	}
}

func TestFinalizeBasicLongContentDisabledReturnsCleanLocalSelection(t *testing.T) {
	mock := &mockSummarizer{summary: "must not run"}
	tool := NewWebExtractTool(WebExtractConfig{MaxContent: 100, Summarizer: mock, SummaryEnabled: false})
	content := strings.Join([]string{
		strings.Repeat("opening material ", 12),
		"配额恢复必须确保 reservation exactly once 释放，然后才能重试运行。",
		"unrelated appendix",
	}, "\n\n")

	output, err := tool.finalizeContent(t.Context(), WebExtractOutput{
		OK: true, Source: "basic", Title: "Fixture", Content: content,
	}, "配额恢复 reservation", extractDetailSummary)
	if err != nil {
		t.Fatalf("finalizeContent() error = %v", err)
	}
	if mock.called || output.RefinementAttempted || output.Summarized || output.Degraded || output.DegradationReason != "" {
		t.Fatalf("disabled Basic refinement must remain a clean local policy result: %#v", output)
	}
	if !output.Truncated || len([]rune(output.Content)) > 100 || !strings.Contains(output.Content, "配额恢复") {
		t.Fatalf("disabled Basic refinement lost the bounded relevant source: %#v", output)
	}
}

func TestFinalizeBasicLongContentUnavailableFallsBackWithReason(t *testing.T) {
	tool := NewWebExtractTool(WebExtractConfig{MaxContent: 100, SummaryEnabled: true})
	content := strings.Join([]string{
		strings.Repeat("opening material ", 12),
		"quota recovery releases the reservation exactly once",
		"unrelated appendix",
	}, "\n\n")

	output, err := tool.finalizeContent(t.Context(), WebExtractOutput{
		OK: true, Source: "basic", Title: "Fixture", Content: content,
	}, "quota recovery reservation", extractDetailSummary)
	if err != nil {
		t.Fatalf("finalizeContent() error = %v", err)
	}
	if output.RefinementAttempted || !output.Degraded || output.DegradationReason != RefinementUnavailable {
		t.Fatalf("output=%#v, want unavailable local fallback", output)
	}
	if !output.Truncated || len([]rune(output.Content)) > 100 || !strings.Contains(output.Content, "quota recovery") {
		t.Fatalf("unavailable Basic refinement lost the bounded relevant source: %#v", output)
	}
}

func TestFinalizeBasicSourceUsesOriginalRelevantPassagesWithoutModel(t *testing.T) {
	mock := &mockSummarizer{summary: "must not run"}
	tool := NewWebExtractTool(WebExtractConfig{Summarizer: mock, SummaryEnabled: true})
	content := strings.Join([]string{
		strings.Repeat("opening source text ", 800),
		strings.Repeat("middle source text ", 800),
		"The exact source states that quota recovery releases the reservation exactly once.",
		strings.Repeat("appendix source text ", 800),
	}, "\n\n")

	output, err := tool.finalizeContent(t.Context(), WebExtractOutput{
		OK: true, Source: "basic", Title: "Fixture", Content: content,
	}, "quota recovery reservation", extractDetailSource)
	if err != nil {
		t.Fatalf("finalizeContent() error = %v", err)
	}
	if mock.called || output.Summarized || !output.Truncated {
		t.Fatalf("source mode output=%#v summarizer_called=%t", output, mock.called)
	}
	if !strings.Contains(output.Content, "quota recovery") || len([]rune(output.Content)) > sourceContentLimit {
		t.Fatalf("source mode lost relevant original passage: chars=%d", len([]rune(output.Content)))
	}
}
