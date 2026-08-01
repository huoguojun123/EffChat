package handler

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/extractor"
	"github.com/huoguojun123/EffChat/internal/repository"
)

var (
	errUploadImageInvalid        = errors.New("image content is invalid or unsupported")
	errUploadImageTypeMismatch   = errors.New("image content does not match its declared type")
	errUploadImagePixelsExceeded = errors.New("image dimensions exceed pixel limit")
	errOCRConfigLoad             = errors.New("OCR configuration load failed")
	errOCRUploadBufferWrite      = errors.New("OCR upload buffer write failed")
	errOCRMetadataCreate         = errors.New("OCR file metadata create failed")
)

// writeUploadSessionLookupError deliberately uses the same 404 for a missing
// session and another user's session. Repository outages remain retryable 5xx
// responses so authorization-safe lookup semantics do not hide operational failures.
func writeUploadSessionLookupError(c *gin.Context, err error) {
	if errors.Is(err, repository.ErrNotFound) {
		writePublicError(c, http.StatusNotFound, "session_not_found", "session not found", false)
		return
	}
	writeServerError(c, http.StatusInternalServerError, "session_load_failed", "failed to load session", err)
}

func writeUploadImageValidationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, errUploadImageTypeMismatch):
		writePublicError(c, http.StatusBadRequest, "file_type_mismatch", "image content does not match its declared type", false)
	case errors.Is(err, errUploadImagePixelsExceeded):
		writePublicError(c, http.StatusRequestEntityTooLarge, "image_dimensions_too_large", fmt.Sprintf("image dimensions exceed the %d pixel limit", maxUploadImagePixels), false)
	default:
		writePublicError(c, http.StatusUnprocessableEntity, "image_invalid", "image content is invalid or unsupported", false)
	}
}

// writeAttachmentExtractionError maps extractor-owned failure classes to the
// public upload contract. Raw parser, path, network, and upstream response text
// stays in request-ID logs and is never used as an API message.
func writeAttachmentExtractionError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, extractor.ErrUnsupported):
		writePublicError(c, http.StatusUnsupportedMediaType, "attachment_type_unsupported", "文件类型暂不支持解析，请转换为 PDF、Word、Excel、Markdown 或文本后重新上传", false)
	case errors.Is(err, extractor.ErrNoReadableText):
		writePublicError(c, http.StatusUnprocessableEntity, "attachment_no_readable_text", "未能从文件提取到文字，未保存附件；如果是扫描件或图片 PDF，请先 OCR 后重新上传", false)
	case errors.Is(err, extractor.ErrLimitExceeded):
		writePublicError(c, http.StatusRequestEntityTooLarge, "attachment_extract_too_large", "文件或解析结果超过处理上限，请上传更小的文件", false)
	case errors.Is(err, extractor.ErrUnprocessable):
		writePublicError(c, http.StatusUnprocessableEntity, "attachment_extract_failed", "文件解析失败，未保存附件，请重试或换一个可提取文字的文件", false)
	default:
		writeServerError(c, http.StatusServiceUnavailable, "attachment_extractor_unavailable", "文件解析服务暂不可用，请稍后重试", err)
	}
}

func writeOCRQueueError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, errOCRConfigLoad):
		writeServerError(c, http.StatusServiceUnavailable, "ocr_config_unavailable", "OCR configuration is temporarily unavailable", err)
	case errors.Is(err, errOCRUploadBufferWrite):
		writeServerError(c, http.StatusInternalServerError, "ocr_upload_buffer_write_failed", "failed to prepare OCR upload", err)
	case errors.Is(err, errOCRMetadataCreate):
		writeServerError(c, http.StatusInternalServerError, "ocr_metadata_create_failed", "failed to save OCR file metadata", err)
	default:
		writeServerError(c, http.StatusInternalServerError, "ocr_queue_failed", "failed to queue OCR extraction", err)
	}
}
