package extractor

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
	"github.com/xuri/excelize/v2"
)

// extractPDF 用纯 Go 库提取 PDF 文本（无系统依赖）。
func extractPDF(content []byte) (string, error) {
	r, err := pdf.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return "", fmt.Errorf("invalid pdf: %w", err)
	}
	var buf bytes.Buffer
	reader, err := r.GetPlainText()
	if err != nil {
		return "", fmt.Errorf("pdf text extraction failed: %w", err)
	}
	if _, err := io.Copy(&buf, reader); err != nil {
		return "", fmt.Errorf("pdf read failed: %w", err)
	}
	return buf.String(), nil
}

var docxTextRe = regexp.MustCompile(`<w:t[^>]*>([^<]*)</w:t>`)
var docxParagraphRe = regexp.MustCompile(`(?s)<w:p\b[^>]*>(.*?)</w:p>`)

// extractDocx 解析 docx（zip + word/document.xml），抽取段落文本。
func extractDocx(content []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return "", fmt.Errorf("invalid docx: %w", err)
	}
	var docXML []byte
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			rc, err := f.Open()
			if err != nil {
				return "", fmt.Errorf("open docx document.xml: %w", err)
			}
			docXML, err = io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return "", fmt.Errorf("read docx document.xml: %w", err)
			}
			break
		}
	}
	if docXML == nil {
		return "", fmt.Errorf("docx missing word/document.xml")
	}
	// Word 文档里的一个段落通常由多个 run 组成，不能直接把所有 <w:t> 平铺；
	// 否则段落边界会丢失，后续清洗也无法判断哪里是真段落、哪里只是视觉换行。
	// 这里先按 <w:p> 切段，每段内拼接所有文本 run，段落之间用空行分隔。
	paragraphMatches := docxParagraphRe.FindAllStringSubmatch(string(docXML), -1)
	paragraphs := make([]string, 0, len(paragraphMatches))
	for _, paragraph := range paragraphMatches {
		textMatches := docxTextRe.FindAllStringSubmatch(paragraph[1], -1)
		var sb strings.Builder
		for _, m := range textMatches {
			sb.WriteString(unescapeXML(m[1]))
		}
		if text := strings.TrimSpace(sb.String()); text != "" {
			paragraphs = append(paragraphs, text)
		}
	}
	if len(paragraphs) > 0 {
		return strings.Join(paragraphs, "\n\n"), nil
	}

	// 极少数 docx 变体没有标准段落标签时，退回到旧的全文本抽取方式。
	matches := docxTextRe.FindAllStringSubmatch(string(docXML), -1)
	var sb strings.Builder
	for _, m := range matches {
		sb.WriteString(unescapeXML(m[1]))
	}
	return sb.String(), nil
}

func unescapeXML(s string) string {
	r := strings.NewReplacer(
		"&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&apos;", "'",
	)
	return r.Replace(s)
}

// extractXlsx 解析 xlsx：逐 sheet 读出单元格，行内用 \t、行间用换行拼成文本。
// 空 sheet 跳过；多 sheet 用 "# <sheet名>" 小标题分隔，便于模型定位。
func extractXlsx(content []byte) (string, error) {
	f, err := excelize.OpenReader(bytes.NewReader(content))
	if err != nil {
		return "", fmt.Errorf("invalid xlsx: %w", err)
	}
	defer f.Close()

	var sb strings.Builder
	for _, sheet := range f.GetSheetList() {
		rows, err := f.GetRows(sheet)
		if err != nil {
			return "", fmt.Errorf("read sheet %q: %w", sheet, err)
		}
		if len(rows) == 0 {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString("# ")
		sb.WriteString(sheet)
		sb.WriteString("\n")
		for _, row := range rows {
			sb.WriteString(strings.Join(row, "\t"))
			sb.WriteString("\n")
		}
	}
	return sb.String(), nil
}

// normalizeExtractedText 是所有提取器的统一出口。
//
// 第一层 normalizeText 只做安全清洗，不改变换行语义；第二层 soft-wrap 合并只用于
// PDF/DOCX 这类“提取器容易把同一段落按视觉行切开”的来源。代码、CSV/TSV、xlsx
// 不走 soft-wrap 合并，避免把本来有结构含义的换行揉坏。
func normalizeExtractedText(s string, kind fileKind) string {
	s = normalizeText(s)
	switch kind {
	case kindPDF, kindDocx:
		return mergeSoftWrappedProse(s)
	default:
		return s
	}
}

// normalizeText 清理提取文本：统一换行、去除控制字符、压缩多余空行、trim。
func normalizeText(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	// 去除除 \n \t 外的控制字符
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	// 去掉每行行尾空白，但保留行首缩进，避免破坏代码或缩进列表。
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRightFunc(line, unicode.IsSpace)
	}
	s = strings.Join(lines, "\n")
	// 压缩 3+ 连续空行为 2 个
	multiNL := regexp.MustCompile(`\n{3,}`)
	s = multiNL.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// mergeSoftWrappedProse 合并 PDF/DOCX 中常见的“视觉换行”。
//
// PDF 提取库经常把同一段正文按页面宽度拆成很多行；如果原样落库，file_read 返回的
// 内容会有密集回车，阅读体验和关键词段落匹配都变差。这里采用保守策略：只合并普通
// prose 行；标题、列表、表格、引用、代码围栏、带 tab 的结构化行全部原样保留。
func mergeSoftWrappedProse(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	paragraph := make([]string, 0, 8)
	inFence := false

	flushParagraph := func() {
		if len(paragraph) == 0 {
			return
		}
		out = append(out, joinProseLines(paragraph))
		paragraph = paragraph[:0]
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			flushParagraph()
			if len(out) == 0 || out[len(out)-1] != "" {
				out = append(out, "")
			}
			continue
		}

		if isFenceLine(trimmed) {
			flushParagraph()
			out = append(out, line)
			inFence = !inFence
			continue
		}
		if inFence || isStructuredTextLine(line) {
			flushParagraph()
			out = append(out, line)
			continue
		}

		paragraph = append(paragraph, trimmed)
	}
	flushParagraph()

	joined := strings.Join(out, "\n")
	multiNL := regexp.MustCompile(`\n{3,}`)
	return strings.TrimSpace(multiNL.ReplaceAllString(joined, "\n\n"))
}

func isFenceLine(trimmed string) bool {
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
}

func isStructuredTextLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	if strings.Contains(line, "\t") {
		return true
	}
	if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ">") {
		return true
	}
	if isListLine(trimmed) || isMarkdownTableLine(trimmed) || isHorizontalRule(trimmed) {
		return true
	}
	return false
}

func isListLine(trimmed string) bool {
	if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "+ ") {
		return true
	}
	for i, r := range trimmed {
		if !unicode.IsDigit(r) {
			if i == 0 {
				return false
			}
			rest := trimmed[i:]
			return strings.HasPrefix(rest, ". ") || strings.HasPrefix(rest, ") ")
		}
	}
	return false
}

func isMarkdownTableLine(trimmed string) bool {
	if strings.Count(trimmed, "|") < 2 {
		return false
	}
	return strings.Contains(trimmed, "|")
}

func isHorizontalRule(trimmed string) bool {
	if len(trimmed) < 3 {
		return false
	}
	for _, r := range trimmed {
		if r != '-' && r != '*' && r != '_' && !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func joinProseLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	out := strings.TrimSpace(lines[0])
	for _, line := range lines[1:] {
		out = joinProseLine(out, strings.TrimSpace(line))
	}
	return out
}

func joinProseLine(left, right string) string {
	if left == "" {
		return right
	}
	if right == "" {
		return left
	}
	leftRunes := []rune(left)
	rightRunes := []rune(right)
	last := leftRunes[len(leftRunes)-1]
	first := rightRunes[0]

	// 英文 PDF 常见断词：inter-\nnational 应还原为 international。
	if last == '-' && len(leftRunes) > 1 && isASCIIWord(runeAtEnd(leftRunes, 1)) && isASCIIWord(first) {
		return string(leftRunes[:len(leftRunes)-1]) + right
	}
	if isCJK(last) && isCJK(first) {
		return left + right
	}
	if isClosingPunctuation(first) {
		return left + right
	}
	if isOpeningPunctuation(last) {
		return left + right
	}
	return left + " " + right
}

func runeAtEnd(rs []rune, offset int) rune {
	return rs[len(rs)-1-offset]
}

func isASCIIWord(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isClosingPunctuation(r rune) bool {
	return strings.ContainsRune(",.;:!?，。！？、；：)]}》”’", r)
}

func isOpeningPunctuation(r rune) bool {
	return strings.ContainsRune("([{《“‘", r)
}

// EstimateTokens 轻量 token 估算：CJK 字符按 1 token/字，其余按 ~4 字符/token。
// 不追求精确，仅用于上传上限校验与上下文预算。
func EstimateTokens(s string) int {
	var cjk, other int
	for _, r := range s {
		if isCJK(r) {
			cjk++
		} else {
			other++
		}
	}
	return cjk + (other+3)/4
}

func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}
