package extractor

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestEstimateTokens(t *testing.T) {
	cases := []struct {
		name string
		in   string
		min  int
		max  int
	}{
		{"empty", "", 0, 0},
		{"english", "hello world this is a test", 5, 9},
		{"chinese", "你好世界这是测试", 8, 8},
		{"mixed", "hello 世界", 4, 6},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := EstimateTokens(c.in)
			if got < c.min || got > c.max {
				t.Errorf("EstimateTokens(%q)=%d, want in [%d,%d]", c.in, got, c.min, c.max)
			}
		})
	}
}

func TestExtractText(t *testing.T) {
	content := []byte("# Title\n\nsome markdown body 你好")
	r, err := Extract(content, "text/markdown", "note.md")
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}
	if !strings.Contains(r.Text, "markdown body") {
		t.Errorf("expected body preserved, got %q", r.Text)
	}
	if r.TokenEstimate <= 0 {
		t.Errorf("expected positive token estimate")
	}
}

func TestExtractTextByExtensionFallback(t *testing.T) {
	// 声明 MIME 不规范，但扩展名是 .go → 仍应按文本提取
	content := []byte("package main\nfunc main() {}")
	r, err := Extract(content, "application/octet-stream", "main.go")
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}
	if !strings.Contains(r.Text, "func main") {
		t.Errorf("expected code body, got %q", r.Text)
	}
}

func TestExtractUnsupported(t *testing.T) {
	// 二进制图片内容 + 图片 MIME → 不支持提取
	content := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	_, err := Extract(content, "image/png", "pic.png")
	if err != ErrUnsupported {
		t.Errorf("expected ErrUnsupported, got %v", err)
	}
}

func TestResolveUploadType(t *testing.T) {
	pngBytes := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D}
	cases := []struct {
		name     string
		declared string
		content  []byte
		filename string
		wantType string
		wantOK   bool
	}{
		{"real text passes", "text/plain", []byte("hello world\nsecond line"), "note.txt", "text/plain", true},
		{"real code passes via json declared", "application/json", []byte(`{"a":1,"b":"x"}`), "data.json", "application/json", true},
		{"binary disguised as text rejected", "text/plain", pngBytes, "evil.txt", "", false},
		{"binary disguised as json rejected", "application/json", pngBytes, "evil.json", "", false},
		{"image passes (not expected text)", "image/png", pngBytes, "pic.png", "image/png", true},
		{"docx passes (zip not text)", docxMIME, buildMinimalDocx(t, "hi"), "doc.docx", docxMIME, true},
		{"pdf declared passes", "application/pdf", []byte("%PDF-1.4 binary..."), "f.pdf", "application/pdf", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotType, gotOK := ResolveUploadType(c.declared, c.content, c.filename)
			if gotOK != c.wantOK {
				t.Fatalf("ResolveUploadType ok=%v, want %v", gotOK, c.wantOK)
			}
			if gotOK && gotType != c.wantType {
				t.Errorf("ResolveUploadType type=%q, want %q", gotType, c.wantType)
			}
		})
	}
}

func TestClassifyRejectsDisguisedBinaryAsText(t *testing.T) {
	// 伪装成 text/plain 的二进制内容：即便 declared/扩展名像文本，也不应按文本提取。
	pngBytes := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D}
	_, err := Extract(pngBytes, "text/plain", "evil.txt")
	if err != ErrUnsupported {
		t.Errorf("expected ErrUnsupported for disguised binary, got %v", err)
	}
}

func TestExtractDocx(t *testing.T) {
	docx := buildMinimalDocx(t, "Hello docx 世界")
	r, err := Extract(docx, docxMIME, "doc.docx")
	if err != nil {
		t.Fatalf("Extract docx failed: %v", err)
	}
	if !strings.Contains(r.Text, "Hello docx") {
		t.Errorf("expected docx text, got %q", r.Text)
	}
}

func TestExtractDocxPreservesParagraphBoundaries(t *testing.T) {
	docx := buildMinimalDocxParagraphs(t, []string{"第一段内容", "第二段内容"})
	r, err := Extract(docx, docxMIME, "doc.docx")
	if err != nil {
		t.Fatalf("Extract docx failed: %v", err)
	}
	if !strings.Contains(r.Text, "第一段内容\n\n第二段内容") {
		t.Fatalf("expected docx paragraphs separated by blank line, got %q", r.Text)
	}
}

func TestNormalizeText(t *testing.T) {
	in := "line1\r\n\r\n\r\n\r\nline2\x00\x07"
	out := normalizeText(in)
	if strings.Contains(out, "\x00") || strings.Contains(out, "\x07") {
		t.Errorf("control chars not stripped: %q", out)
	}
	if strings.Count(out, "\n\n\n") > 0 {
		t.Errorf("excess blank lines not collapsed: %q", out)
	}
}

func TestNormalizeExtractedTextMergesPDFSoftWraps(t *testing.T) {
	in := strings.Join([]string{
		"这是第一行",
		"继续这一句",
		"",
		"English soft",
		"wrapped inter-",
		"national line.",
		"",
		"- 列表一",
		"- 列表二",
		"",
		"| A | B |",
		"|---|---|",
		"| 1 | 2 |",
		"",
		"```go",
		"fmt.Println(\"x\")",
		"```",
	}, "\n")

	out := normalizeExtractedText(in, kindPDF)
	if !strings.Contains(out, "这是第一行继续这一句") {
		t.Fatalf("expected CJK soft lines merged without spaces, got %q", out)
	}
	if !strings.Contains(out, "English soft wrapped international line.") {
		t.Fatalf("expected English soft lines and hyphenation merged, got %q", out)
	}
	for _, want := range []string{
		"- 列表一\n- 列表二",
		"| A | B |\n|---|---|\n| 1 | 2 |",
		"```go\nfmt.Println(\"x\")\n```",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected structured block preserved %q, got %q", want, out)
		}
	}
}

func TestNormalizeExtractedTextDoesNotMergeCodeLikeText(t *testing.T) {
	in := "package main\nfunc main() {\n\tprintln(\"x\")\n}"
	out := normalizeExtractedText(in, kindText)
	if out != in {
		t.Fatalf("code-like text should keep line breaks, got %q", out)
	}
}

func TestExtractXlsx(t *testing.T) {
	xlsx := buildMinimalXlsx(t)
	r, err := Extract(xlsx, xlsxMIME, "data.xlsx")
	if err != nil {
		t.Fatalf("Extract xlsx failed: %v", err)
	}
	if !strings.Contains(r.Text, "姓名") || !strings.Contains(r.Text, "张三") {
		t.Errorf("expected cell values, got %q", r.Text)
	}
	if !strings.Contains(r.Text, "Sheet1") {
		t.Errorf("expected sheet title, got %q", r.Text)
	}
	if r.TokenEstimate <= 0 {
		t.Errorf("expected positive token estimate")
	}
}

func TestExtractXlsxByExtensionFallback(t *testing.T) {
	// 声明 MIME 为通用二进制，但扩展名 .xlsx → 仍按 xlsx 提取
	xlsx := buildMinimalXlsx(t)
	r, err := Extract(xlsx, "application/octet-stream", "report.xlsx")
	if err != nil {
		t.Fatalf("Extract xlsx by ext failed: %v", err)
	}
	if !strings.Contains(r.Text, "张三") {
		t.Errorf("expected cell values, got %q", r.Text)
	}
}

// buildMinimalXlsx 构造一个含表头与一行数据的最小 xlsx 用于测试。
func buildMinimalXlsx(t *testing.T) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	_ = f.SetCellValue("Sheet1", "A1", "姓名")
	_ = f.SetCellValue("Sheet1", "B1", "年龄")
	_ = f.SetCellValue("Sheet1", "A2", "张三")
	_ = f.SetCellValue("Sheet1", "B2", 30)
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// buildMinimalDocx 构造一个仅含 word/document.xml 的最小 docx（zip）用于测试。
func buildMinimalDocx(t *testing.T, text string) []byte {
	t.Helper()
	return buildMinimalDocxParagraphs(t, []string{text})
}

// buildMinimalDocxParagraphs 构造多个 Word 段落，用来验证段落边界不会被提取器铺平。
func buildMinimalDocxParagraphs(t *testing.T, paragraphs []string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	var body strings.Builder
	body.WriteString(`<?xml version="1.0"?><w:document xmlns:w="x"><w:body>`)
	for _, text := range paragraphs {
		body.WriteString(`<w:p><w:r><w:t>`)
		body.WriteString(text)
		body.WriteString(`</w:t></w:r></w:p>`)
	}
	body.WriteString(`</w:body></w:document>`)
	if _, err := w.Write([]byte(body.String())); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
