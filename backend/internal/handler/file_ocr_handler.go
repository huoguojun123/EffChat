package handler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/extractor"
	"github.com/huoguojun123/EffChat/internal/filepolicy"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
)

func queueMinerUOCR(fileRepo *repository.FileRepository, opts uploadFileHandlerOptions, f *model.File, userID, sessionID int64, content []byte, _ string, safeName, storedName, sourceDir string, _ int, _ int64) (*model.File, error) {
	if opts.channelService == nil || opts.extractorClient == nil || !opts.extractorClient.Enabled() || opts.ocrRecovery == nil {
		return nil, nil
	}
	if !opts.channelService.ResolveMinerUOCRConfig().Enabled {
		return nil, nil
	}
	sourcePath := filepath.Join(sourceDir, storedName)
	if err := filepolicy.WriteFile(sourcePath, content, 0o600); err != nil {
		return nil, fmt.Errorf("%w: %v", errOCRUploadBufferWrite, err)
	}
	provider := "mineru"
	extractedPath := extractedTextSidecarPath(userID, storedName)
	f.FilePath = extractedPath
	f.ExtractedTextPath = &extractedPath
	f.ExtractStatus = "ocr_pending"
	f.OCRProvider = &provider
	f.OCRSourcePath = &sourcePath
	now := time.Now()
	f.OCRNextRetryAt = &now
	if err := fileRepo.Create(f); err != nil {
		_ = filepolicy.Remove(sourcePath)
		log.Printf("[file_ocr] metadata_create_failed user=%d session=%d file=%q err=%v", userID, sessionID, safeName, err)
		return nil, fmt.Errorf("%w: %v", errOCRMetadataCreate, err)
	}

	opts.ocrRecovery.Wake()
	log.Printf("[file_ocr] queued user=%d session=%d file_id=%d file=%q", userID, sessionID, f.ID, safeName)
	return f, nil
}

func writePDFExtractionFailure(c *gin.Context) {
	writePublicError(c, http.StatusUnprocessableEntity, "attachment_no_readable_text", "未能从 PDF 提取到文字；如需更高质量解析，请先在管理员后台启用 MinerU 精准 OCR 并配置 Token", false)
}

func isPDFUpload(contentType, filename string) bool {
	return contentType == "application/pdf" || strings.HasSuffix(strings.ToLower(filename), ".pdf")
}

func shouldOfferMinerUOCR(err error) bool {
	return errors.Is(err, extractor.ErrNoReadableText)
}

func humanizeOCRError(err error) string {
	if err == nil {
		return "OCR 解析失败"
	}
	text := err.Error()
	switch {
	case strings.Contains(text, "ocr_file_too_large"):
		return "PDF 超过 MinerU 精准解析 200MB 限制"
	case strings.Contains(text, "ocr_page_limit_exceeded"):
		return "PDF 超过 MinerU 精准解析 200 页限制"
	default:
		return "OCR 解析启动失败，请稍后重试或删除文件后重新上传"
	}
}

func minerUFailedTaskMessage(errorType string) string {
	switch strings.ToLower(strings.TrimSpace(errorType)) {
	case "ocr_file_too_large", "file_too_large":
		return "PDF 超过 MinerU 精准解析 200MB 限制"
	case "ocr_page_limit_exceeded", "page_limit_exceeded":
		return "PDF 超过 MinerU 精准解析 200 页限制"
	default:
		return "MinerU OCR 解析失败，请重试或删除文件"
	}
}

func combineOCRMessages(primary, secondary string) string {
	primary = strings.TrimSpace(primary)
	secondary = strings.TrimSpace(secondary)
	if primary == "" {
		return secondary
	}
	if secondary == "" || primary == secondary {
		return primary
	}
	return primary + "；" + secondary
}

func isTransientOCRPollError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "context deadline exceeded") ||
		strings.Contains(text, "client timeout") ||
		strings.Contains(text, "timeout awaiting response headers") ||
		strings.Contains(text, "connection reset by peer") ||
		strings.Contains(text, "temporary failure")
}

func isPermanentOCRSubmitError(err error) bool {
	if err == nil || isTransientOCRPollError(err) {
		return false
	}
	status := ocrUpstreamStatus(err)
	if status >= 400 && status < 500 && status != http.StatusRequestTimeout && status != http.StatusTooManyRequests {
		return true
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "not configured") ||
		strings.Contains(text, "unauthorized") ||
		strings.Contains(text, "forbidden") ||
		strings.Contains(text, "invalid api") ||
		strings.Contains(text, "invalid request") ||
		strings.Contains(text, "unsupported file")
}

func isUncertainOCRSubmissionError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "connection reset") ||
		strings.Contains(text, "unexpected eof") ||
		strings.Contains(text, "decode mineru start response") ||
		strings.Contains(text, "mineru start returned no task id")
}

func ocrUpstreamStatus(err error) int {
	if err == nil {
		return 0
	}
	for _, marker := range []string{" returned ", " status "} {
		index := strings.Index(strings.ToLower(err.Error()), marker)
		if index < 0 {
			continue
		}
		fields := strings.Fields(err.Error()[index+len(marker):])
		if len(fields) == 0 {
			continue
		}
		status, parseErr := strconv.Atoi(strings.Trim(fields[0], ":"))
		if parseErr == nil {
			return status
		}
	}
	return 0
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minerUStartTimeout(configuredSeconds int) time.Duration {
	if configuredSeconds < minMinerUStartTimeoutSeconds {
		configuredSeconds = minMinerUStartTimeoutSeconds
	}
	return time.Duration(configuredSeconds) * time.Second
}
