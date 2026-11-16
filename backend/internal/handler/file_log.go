package handler

import (
	"fmt"

	"github.com/gin-gonic/gin"
)

// File errors can embed user filenames, storage paths or signed upstream URLs.
// Preserve the existing public/request-ID contract but log only the Go type;
// the stable operation code carries the diagnostic meaning, not err.Error().
func writeFileServerError(c *gin.Context, status int, code, message string, err error) {
	writeServerError(c, status, code, message, fmt.Errorf("file operation error_type=%T", err))
}

func fileLogParser(parser string) string {
	// The sidecar result is not a logging trust boundary. Unknown parser labels
	// remain usable by extraction, but cannot become arbitrary log content.
	switch parser {
	case "go-text", "go-csv", "go-docx", "go-xlsx", "go-pdf", "pdfplumber", "python-docx", "python-pptx", "openpyxl", "python-csv", "pdf_oxide.extract_markdown", "pdf_oxide.extract_text", "pdf_oxide.to_markdown":
		return parser
	default:
		return "other"
	}
}
