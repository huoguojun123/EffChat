// Package extractor 从上传文件中提取纯文本正文，供发送消息时注入模型上下文。
//
// 设计要点：
//   - 按真实内容嗅探 MIME（http.DetectContentType），不信任客户端声明，防伪造绕过。
//   - 第一阶段支持：纯文本/markdown/csv/代码、PDF、docx。
//   - token 估算用轻量启发式（中文按字、英文按 ~4 字符/token），不引入重型 tokenizer。
package extractor

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// Result 提取结果。
type Result struct {
	Text           string   // 提取的纯文本正文
	TokenEstimate  int      // 估算 token 数
	SniffedMIME    string   // 按内容嗅探出的真实 MIME（前 512 字节）
	Parser         string   // 实际解析器名称，主要用于日志/排障
	PageCount      int      // PDF/PPTX 等页数或 slide 数，无法得知时为 0
	ParagraphCount int      // 粗略段落数，无法得知时为 0
	TableCount     int      // 表格数量，无法得知时为 0
	Warnings       []string // 解析器给出的非致命告警
}

// ErrUnsupported 表示文件类型不在提取白名单内。
var ErrUnsupported = fmt.Errorf("unsupported file type for extraction")

// Extract 根据声明的 MIME 与文件内容提取正文。
// declaredMIME 来自上传校验（已过白名单），filename 用于扩展名兜底判断。
func Extract(content []byte, declaredMIME, filename string) (*Result, error) {
	sniffed := sniffMIME(content, filename)
	kind := classify(declaredMIME, sniffed, filename)

	var text string
	var err error
	switch kind {
	case kindText:
		text = string(content)
	case kindPDF:
		text, err = extractPDF(content)
	case kindDocx:
		text, err = extractDocx(content)
	case kindXlsx:
		text, err = extractXlsx(content)
	default:
		return nil, ErrUnsupported
	}
	if err != nil {
		return nil, err
	}

	text = normalizeExtractedText(text, kind)
	return &Result{
		Text:          text,
		TokenEstimate: EstimateTokens(text),
		SniffedMIME:   sniffed,
		Parser:        kind.String(),
	}, nil
}

func ExtractWithSidecar(ctx context.Context, content []byte, declaredMIME, filename string, sidecar *SidecarClient) (*Result, error) {
	if ShouldUseSidecar(declaredMIME, filename) {
		if sidecar == nil || !sidecar.Enabled() {
			return nil, fmt.Errorf("文档解析服务未启用")
		}
		return sidecar.Extract(ctx, content, declaredMIME, filename)
	}
	return Extract(content, declaredMIME, filename)
}

// ResolveUploadType 在上传白名单校验前，用真实内容嗅探复核客户端声明的 MIME，
// 返回用于白名单匹配的可信类型；当声明为文本但内容明显是二进制时返回 ok=false（拒绝）。
//
// 这是「按真实内容嗅探，不信任客户端声明」的落点：仅看 multipart 头会让伪装成
// text/plain 的二进制绕过白名单并被当正文提取。docx/xlsx 嗅探为 application/zip、
// pdf 为 application/pdf 属正常，不在此拒绝，交由白名单与扩展名判定。
func ResolveUploadType(declared string, content []byte, filename string) (string, bool) {
	lower := strings.ToLower(filename)
	// 期望为文本的声明：text/*、application/json、application/xml、或常见文本/代码扩展名。
	expectsText := strings.HasPrefix(declared, "text/") || looksLikeText(declared, lower)
	if expectsText {
		// 必须内容确实像文本（按真实字节嗅探），否则视为伪装文本的二进制，拒绝。
		if strings.HasPrefix(sniffMIME(content, filename), "text/") {
			return declared, true
		}
		return "", false
	}
	return declared, true
}

type fileKind int

const (
	kindUnknown fileKind = iota
	kindText
	kindPDF
	kindDocx
	kindXlsx
	kindPptx
	kindCSV
)

const docxMIME = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
const xlsxMIME = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
const pptxMIME = "application/vnd.openxmlformats-officedocument.presentationml.presentation"

// classify 综合声明 MIME、嗅探 MIME、扩展名判定文件类别。
// 文本判定以内容嗅探为准（http.DetectContentType 对含二进制字节的内容不会返回 text/*），
// 仅靠 declared/扩展名不足以归为 kindText，防止伪装成 text/plain 的二进制被当正文提取。
func classify(declared, sniffed, filename string) fileKind {
	lower := strings.ToLower(filename)
	switch {
	case declared == "application/pdf" || sniffed == "application/pdf" || strings.HasSuffix(lower, ".pdf"):
		return kindPDF
	case declared == docxMIME || strings.HasSuffix(lower, ".docx"):
		return kindDocx
	case declared == xlsxMIME || strings.HasSuffix(lower, ".xlsx"):
		return kindXlsx
	case declared == pptxMIME || strings.HasSuffix(lower, ".pptx"):
		return kindPptx
	case declared == "text/csv" || strings.HasSuffix(lower, ".csv"):
		return kindCSV
	case strings.HasPrefix(sniffed, "text/"):
		// 内容确为文本：无论 declared 是 text/* 还是 application/json|xml 都按文本提取。
		return kindText
	}
	return kindUnknown
}

func (k fileKind) String() string {
	switch k {
	case kindText:
		return "go-text"
	case kindPDF:
		return "go-pdf"
	case kindDocx:
		return "go-docx"
	case kindXlsx:
		return "go-xlsx"
	case kindPptx:
		return "python-pptx"
	case kindCSV:
		return "python-csv"
	default:
		return "unknown"
	}
}

// ShouldUseSidecar 判断是否必须交给 Python sidecar。
//
// 这里刻意不把纯文本、Markdown 和常见代码文件交给 Python：这些文件本质上已经是
// 可读文本，Go 本地清洗即可，没必要让 sidecar 成为上传所有小文本的瓶颈。PDF、
// Office 和 CSV 是结构化文档，Python 解析生态明显更成熟，统一走 sidecar。
func ShouldUseSidecar(declaredMIME, filename string) bool {
	lower := strings.ToLower(filename)
	switch {
	case declaredMIME == "application/pdf" || strings.HasSuffix(lower, ".pdf"):
		return true
	case declaredMIME == docxMIME || declaredMIME == xlsxMIME || declaredMIME == pptxMIME:
		return true
	case strings.HasSuffix(lower, ".docx") || strings.HasSuffix(lower, ".xlsx") || strings.HasSuffix(lower, ".pptx"):
		return true
	case declaredMIME == "text/csv" || strings.HasSuffix(lower, ".csv"):
		return true
	default:
		return false
	}
}

// looksLikeText 兜底：常见可读文本/代码扩展名即便 MIME 不规范也按文本处理。
func looksLikeText(declared, lowerName string) bool {
	textExts := []string{
		".txt", ".md", ".markdown", ".csv", ".tsv", ".json", ".yaml", ".yml",
		".xml", ".html", ".htm", ".log", ".ini", ".toml", ".go", ".py", ".js",
		".ts", ".tsx", ".jsx", ".java", ".c", ".cpp", ".h", ".rs", ".rb", ".sh",
		".sql", ".css",
	}
	for _, ext := range textExts {
		if strings.HasSuffix(lowerName, ext) {
			return true
		}
	}
	return declared == "application/json" || declared == "application/xml"
}

// sniffMIME 用前 512 字节按内容嗅探真实 MIME。
func sniffMIME(content []byte, filename string) string {
	n := 512
	if len(content) < n {
		n = len(content)
	}
	return http.DetectContentType(content[:n])
}
