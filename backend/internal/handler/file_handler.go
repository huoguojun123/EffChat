package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/extractor"
	"github.com/huoguojun123/EffChat/internal/filepolicy"
	"github.com/huoguojun123/EffChat/internal/middleware"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
	"github.com/huoguojun123/EffChat/pkg/logger"
	_ "golang.org/x/image/webp"
)

const uploadDir = filepolicy.StorageRoot
const multipartOverheadLimit = int64(1 << 20)
const minMinerUStartTimeoutSeconds = 300
const maxUploadImagePixels = 40_000_000

type uploadFileHandlerOptions struct {
	sessionRepo     *repository.SessionRepository
	extractorClient *extractor.SidecarClient
	channelService  *service.ChannelService
	quotaService    *service.QuotaService
	ocrRecovery     *OCRRecoveryRunner
}

type UploadFileHandlerOption func(*uploadFileHandlerOptions)

func WithUploadSessionRepo(repo *repository.SessionRepository) UploadFileHandlerOption {
	return func(opts *uploadFileHandlerOptions) {
		opts.sessionRepo = repo
	}
}

func WithUploadExtractorClient(client *extractor.SidecarClient) UploadFileHandlerOption {
	return func(opts *uploadFileHandlerOptions) {
		opts.extractorClient = client
	}
}

func WithUploadChannelService(channelService *service.ChannelService) UploadFileHandlerOption {
	return func(opts *uploadFileHandlerOptions) {
		opts.channelService = channelService
	}
}

func WithUploadQuotaService(quotaService *service.QuotaService) UploadFileHandlerOption {
	return func(opts *uploadFileHandlerOptions) {
		opts.quotaService = quotaService
	}
}

func WithUploadOCRRecoveryRunner(runner *OCRRecoveryRunner) UploadFileHandlerOption {
	return func(opts *uploadFileHandlerOptions) {
		opts.ocrRecovery = runner
	}
}

// UploadLimitsHandler 返回当前登录用户上传前端预校验所需的全局限制。
//
// 后端仍是最终裁判；这个接口只负责让 ChatInput 不再硬编码“5 个 / 20MB”，
// 避免管理员改了系统配置后前端提示和真实上传行为漂移。
func UploadLimitsHandler(configRepo *repository.ConfigRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		limits, err := resolveUploadLimits(c.Request.Context(), configRepo)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "文件上传策略暂不可用，请稍后重试", "code": "file_policy_unavailable"})
			return
		}
		if limits.PolicyDegraded {
			log.Printf("[file_upload] upload policy degraded; using last-known-good limits")
		}
		c.JSON(http.StatusOK, limits)
	}
}

// UploadFileHandler 文件上传（multipart/form-data）
func UploadFileHandler(fileRepo *repository.FileRepository, configRepo *repository.ConfigRepository, optionFns ...UploadFileHandlerOption) gin.HandlerFunc {
	opts := uploadFileHandlerOptions{}
	for _, fn := range optionFns {
		if fn != nil {
			fn(&opts)
		}
	}
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		limits, err := resolveUploadLimits(c.Request.Context(), configRepo)
		if err != nil {
			writeServerError(c, http.StatusServiceUnavailable, "file_policy_unavailable", "文件上传策略暂不可用，请稍后重试", err)
			return
		}
		if limits.PolicyDegraded {
			log.Printf("[file_upload] upload policy degraded; using last-known-good limits")
		}
		extractPolicy, err := resolveAttachmentProcessingPolicy(c.Request.Context(), configRepo)
		if err != nil {
			writeServerError(c, http.StatusServiceUnavailable, "attachment_policy_unavailable", "附件处理策略暂不可用，请稍后重试", err)
			return
		}
		if extractPolicy.Degraded {
			log.Printf("[file_upload] attachment policy degraded; using last-known-good controls")
		}
		maxUploadSize := int64(limits.MaxFileSizeMB) << 20
		maxOutputBytes := int64(extractPolicy.MaxOutputMB) << 20
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadSize+multipartOverheadLimit)

		file, header, err := c.Request.FormFile("file")
		if err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				writePublicError(c, http.StatusRequestEntityTooLarge, "file_too_large", fmt.Sprintf("file too large (max %dMB)", limits.MaxFileSizeMB), false)
				return
			}
			writePublicError(c, http.StatusBadRequest, "file_required", "file is required", false)
			return
		}
		defer file.Close()

		sessionID, ok := parseRequiredSessionID(c)
		if !ok {
			return
		}
		if opts.sessionRepo != nil {
			if _, err := opts.sessionRepo.GetByIDContext(c.Request.Context(), sessionID, userID); err != nil {
				writeUploadSessionLookupError(c, err)
				return
			}
		}
		safeName := sanitizeUploadFilename(header.Filename)
		declaredType := header.Header.Get("Content-Type")
		if declaredType == "" {
			declaredType = mime.TypeByExtension(filepath.Ext(safeName))
		}
		if declaredType == "" {
			declaredType = "application/octet-stream"
		}

		if header.Size > maxUploadSize {
			writePublicError(c, http.StatusRequestEntityTooLarge, "file_too_large", fmt.Sprintf("file too large (max %dMB)", limits.MaxFileSizeMB), false)
			return
		}

		// 读取文件内容（用于 hash、嗅探、提取）。必须先读再校验：白名单不能只信
		// multipart 头声明的 Content-Type，否则伪装成 text/plain 的二进制即可绕过。
		content, err := io.ReadAll(io.LimitReader(file, maxUploadSize+1))
		if err != nil {
			writeServerError(c, http.StatusInternalServerError, "file_read_failed", "failed to read file", err)
			return
		}
		if int64(len(content)) > maxUploadSize {
			writePublicError(c, http.StatusRequestEntityTooLarge, "file_too_large", fmt.Sprintf("file too large (max %dMB)", limits.MaxFileSizeMB), false)
			return
		}

		// declared + 内容嗅探 reconcile，得出可信类型再过白名单。
		contentType, ok := extractor.ResolveUploadType(declaredType, content, safeName)
		if !ok {
			writePublicError(c, http.StatusBadRequest, "file_type_mismatch", "file content does not match its declared type", false)
			return
		}
		if !isAllowedContentType(contentType, limits.AllowedTypes) {
			writePublicError(c, http.StatusUnsupportedMediaType, "file_type_not_allowed", "file type not allowed", false)
			return
		}
		if strings.HasPrefix(contentType, "image/") {
			verifiedType, err := validateUploadImage(content, contentType)
			if err != nil {
				writeUploadImageValidationError(c, err)
				return
			}
			contentType = verifiedType
		}

		hash := fmt.Sprintf("%x", sha256.Sum256(content))

		if existing, err := fileRepo.FindActiveByHashInSession(userID, sessionID, hash, int64(len(content))); err == nil {
			c.JSON(http.StatusOK, existing)
			return
		} else if !errors.Is(err, repository.ErrNotFound) {
			writeServerError(c, http.StatusInternalServerError, "file_duplicate_check_failed", "failed to check duplicate file", err)
			return
		}

		if count, err := fileRepo.CountActiveBySession(userID, sessionID); err != nil {
			writeServerError(c, http.StatusInternalServerError, "session_file_count_failed", "failed to check session file count", err)
			return
		} else if count >= limits.MaxSessionFiles {
			writePublicError(c, http.StatusConflict, "session_file_limit_reached", fmt.Sprintf("session has too many active files (max %d)", limits.MaxSessionFiles), false)
			return
		}

		// 生成存储路径名。文档类解析成功后只保留解析文本；图片类才保留原始文件。
		userDir := uploadUserMonthDir(userID, time.Now())
		if err := os.MkdirAll(userDir, 0755); err != nil {
			writeServerError(c, http.StatusInternalServerError, "upload_directory_create_failed", "failed to create upload directory", err)
			return
		}

		storedName := fmt.Sprintf("%d_%s", time.Now().UnixNano(), safeName)

		sessionIDPtr := sessionID

		f := &model.File{
			UserID:        userID,
			SessionID:     &sessionIDPtr,
			FileName:      safeName,
			FileType:      contentType,
			FileSize:      int64(len(content)),
			FileHash:      &hash,
			ExtractStatus: "pending",
		}

		// 图片是唯一继续保留原始文件的附件类型：缩略图和 vision 模型都需要原图。
		// 文档/文本则不把源文件长期落盘，只有解析成功后才写入提取文本文件。
		isImage := strings.HasPrefix(contentType, "image/")
		if isImage {
			storedPath := filepath.Join(userDir, storedName)
			if err := filepolicy.WriteFile(storedPath, content, 0o644); err != nil {
				writeServerError(c, http.StatusInternalServerError, "file_write_failed", "failed to save file", err)
				return
			}
			f.FilePath = storedPath
			f.ExtractStatus = "ready"
		}

		// 文档类附件提取正文 + 输出大小校验。
		// token 估算只作为展示/提示，不再阻断论文类长文档上传；真正限制的是 sidecar
		// 文本字节数，避免 data 目录和预览接口被异常超大解析结果撑爆。
		// 普通文档仍同步提取；MinerU PDF 会先创建可轮询的 pending 记录，再由后台任务处理，
		// 避免浏览器或反向代理 60 秒超时导致“转圈后文件消失”。
		if !isImage {
			if !extractPolicy.Enabled {
				writePublicError(c, http.StatusConflict, "attachment_extract_disabled", "附件文本解析已关闭，无法保存文档附件", false)
				return
			}
			if isPDFUpload(contentType, safeName) {
				log.Printf("[file_ocr] pdf_upload_start user=%d session=%d file=%q bytes=%d strategy=mineru_async_first", userID, sessionID, safeName, len(content))
				if queuedFile, queueErr := queueMinerUOCR(c.Request.Context(), fileRepo, opts, f, userID, sessionID, content, contentType, safeName, storedName, ocrStagingUserMonthDir(userID, time.Now()), extractPolicy.TimeoutSeconds, maxOutputBytes); queuedFile != nil {
					c.JSON(http.StatusAccepted, queuedFile)
					return
				} else if queueErr != nil {
					writeOCRQueueError(c, queueErr)
					return
				}
				log.Printf("[file_ocr] local_fallback_start user=%d session=%d file=%q", userID, sessionID, safeName)
			}

			extractCtx, cancel := context.WithTimeout(c.Request.Context(), time.Duration(extractPolicy.TimeoutSeconds)*time.Second)
			defer cancel()
			res, err := extractor.ExtractWithSidecar(extractCtx, content, contentType, safeName, opts.extractorClient)
			if err != nil {
				if isPDFUpload(contentType, safeName) && shouldOfferMinerUOCR(err) {
					log.Printf("[file_ocr] local_fallback_empty user=%d session=%d file=%q err=%v", userID, sessionID, safeName, err)
					writePDFExtractionFailure(c)
					return
				}
				log.Printf("[file_upload] extract_failed user=%d session=%d file=%q content_type=%s err=%v", userID, sessionID, safeName, contentType, err)
				writeAttachmentExtractionError(c, err)
				return
			} else {
				if int64(len([]byte(res.Text))) > maxOutputBytes {
					writePublicError(c, http.StatusRequestEntityTooLarge, "attachment_extract_too_large", fmt.Sprintf("解析结果过大（上限 %dMB），请上传更小的文件", extractPolicy.MaxOutputMB), false)
					return
				}
				if strings.TrimSpace(res.Text) == "" {
					if isPDFUpload(contentType, safeName) {
						log.Printf("[file_ocr] local_fallback_empty user=%d session=%d file=%q reason=empty_text", userID, sessionID, safeName)
						writePDFExtractionFailure(c)
						return
					}
					writePublicError(c, http.StatusUnprocessableEntity, "attachment_no_readable_text", "未能从文件提取到文字，未保存附件；如果是扫描件或图片 PDF，请先 OCR 后重新上传", false)
					return
				}
				extractedPath, err := writeExtractedTextSidecar(userID, storedName, res.Text)
				if err != nil {
					writeServerError(c, http.StatusInternalServerError, "extracted_text_write_failed", "failed to save extracted text", err)
					return
				}
				f.FilePath = extractedPath
				f.ExtractedTextPath = &extractedPath
				f.TokenEstimate = res.TokenEstimate
				f.ExtractStatus = "ready"
				if res.Parser != "" {
					logger.Info("file extracted: user=%d session=%d file=%q parser=%s tokens=%d pages=%d tables=%d warnings=%d",
						userID, sessionID, safeName, res.Parser, res.TokenEstimate, res.PageCount, res.TableCount, len(res.Warnings))
					if isPDFUpload(contentType, safeName) {
						log.Printf("[file_ocr] local_fallback_success user=%d session=%d file=%q parser=%s tokens=%d pages=%d warnings=%d", userID, sessionID, safeName, res.Parser, res.TokenEstimate, res.PageCount, len(res.Warnings))
					}
				}
			}
		}

		if strings.TrimSpace(f.FilePath) == "" {
			writeServerError(c, http.StatusInternalServerError, "file_storage_incomplete", "文件未能保存，请重试", errors.New("upload completed without a managed file path"))
			return
		}

		if err := fileRepo.Create(f); err != nil {
			removeSavedFilePaths(f.FilePath, f.ExtractedTextPath)
			writeServerError(c, http.StatusInternalServerError, "file_metadata_create_failed", "failed to save file metadata", err)
			return
		}

		c.JSON(http.StatusCreated, f)
	}
}

func validateUploadImage(content []byte, declaredType string) (string, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(content))
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		return "", errUploadImageInvalid
	}
	actualType, ok := map[string]string{
		"gif":  "image/gif",
		"jpeg": "image/jpeg",
		"png":  "image/png",
		"webp": "image/webp",
	}[format]
	if !ok || !strings.EqualFold(declaredType, actualType) {
		return "", errUploadImageTypeMismatch
	}
	if int64(config.Width)*int64(config.Height) > maxUploadImagePixels {
		return "", errUploadImagePixelsExceeded
	}
	return actualType, nil
}

func parseRequiredSessionID(c *gin.Context) (int64, bool) {
	raw := strings.TrimSpace(c.PostForm("session_id"))
	if raw == "" {
		writePublicError(c, http.StatusBadRequest, "session_id_required", "session_id is required", false)
		return 0, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writePublicError(c, http.StatusBadRequest, "session_id_invalid", "invalid session_id", false)
		return 0, false
	}
	return id, true
}

func parseBoolQuery(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func writeExtractedTextSidecar(userID int64, storedName, text string) (string, error) {
	path := extractedTextSidecarPath(userID, storedName)
	if err := writeTextFile(path, text); err != nil {
		return "", err
	}
	return path, nil
}

func extractedTextSidecarPath(userID int64, storedName string) string {
	return filepath.Join(filepolicy.AttachmentExtractedRoot, fmt.Sprintf("%d", userID), time.Now().Format("2006-01"), storedName+".txt")
}

func uploadUserMonthDir(userID int64, at time.Time) string {
	return filepath.Join(filepolicy.AttachmentOriginalsRoot, fmt.Sprintf("%d", userID), at.Format("2006-01"))
}

func ocrStagingUserMonthDir(userID int64, at time.Time) string {
	return filepath.Join(filepolicy.AttachmentOCRRoot, fmt.Sprintf("%d", userID), at.Format("2006-01"))
}

func writeTextFile(path, text string) error {
	return filepolicy.WriteFile(path, []byte(text), 0o600)
}

// removeSavedFilePaths 清理已经写入磁盘、但 DB 元数据保存失败的附件文件。
//
// 文档类附件解析成功后，file_path 和 extracted_text_path 都指向同一份解析文本；
// 图片类附件只有 file_path。这里按路径去重后删除，避免把“同一份文本既是下载文件
// 又是 file_read 数据源”的设计细节泄漏到调用方。
func removeSavedFilePaths(primary string, extracted *string) {
	seen := make(map[string]struct{}, 2)
	for _, path := range []string{primary, valueOrEmpty(extracted)} {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		_ = filepolicy.Remove(path)
	}
}

func removeManagedFilePaths(primary string, extracted *string, extraPaths ...*string) error {
	seen := make(map[string]struct{}, 2+len(extraPaths))
	rawPaths := []string{primary, valueOrEmpty(extracted)}
	for _, extra := range extraPaths {
		rawPaths = append(rawPaths, valueOrEmpty(extra))
	}
	for _, raw := range rawPaths {
		path := strings.TrimSpace(raw)
		if path == "" {
			continue
		}
		cleanPath, err := managedUploadPath(path)
		if err != nil {
			return err
		}
		if _, ok := seen[cleanPath]; ok {
			continue
		}
		seen[cleanPath] = struct{}{}
		if err := filepolicy.Remove(cleanPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("failed to remove file %s: %w", cleanPath, err)
		}
	}
	return nil
}

func managedUploadPath(path string) (string, error) {
	return filepolicy.ManagedPath(path)
}

func valueOrEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func isAllowedContentType(contentType string, allowedTypes []string) bool {
	for _, item := range allowedTypes {
		if item == contentType {
			return true
		}
		if strings.HasSuffix(item, "/*") {
			prefix := strings.TrimSuffix(item, "*")
			if strings.HasPrefix(contentType, prefix) {
				return true
			}
		}
	}
	return false
}

func sanitizeUploadFilename(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "file"
	}
	return name
}

// DownloadFileHandler 文件下载
func DownloadFileHandler(fileRepo *repository.FileRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			writePublicError(c, http.StatusBadRequest, "file_id_invalid", "invalid file id", false)
			return
		}

		f, err := fileRepo.GetByID(id, userID)
		if err != nil {
			writeFileLookupError(c, err)
			return
		}

		path := f.FilePath
		filename := f.FileName
		contentType := f.FileType
		if !strings.HasPrefix(f.FileType, "image/") && f.ExtractStatus != "ready" {
			writePublicError(c, http.StatusConflict, "file_content_pending", "文件仍在解析中，暂不能下载；解析完成后请使用文本预览", true)
			return
		}
		if f.ExtractedTextPath != nil && strings.TrimSpace(*f.ExtractedTextPath) != "" && !strings.HasPrefix(f.FileType, "image/") {
			path = *f.ExtractedTextPath
			filename = f.FileName + ".txt"
			contentType = "text/plain; charset=utf-8"
		}
		path, err = filepolicy.ExistingPath(path)
		if err != nil {
			writeServerError(c, http.StatusInternalServerError, "file_download_unavailable", "file storage is unavailable", err)
			return
		}
		c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
		c.Header("Content-Type", contentType)
		c.File(path)
	}
}

// ListFilesHandler 文件列表
func ListFilesHandler(fileRepo *repository.FileRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		limit, offset := parsePagination(c)

		var files []*model.File
		var err error
		if rawSessionID := strings.TrimSpace(c.Query("session_id")); rawSessionID != "" {
			sessionID, parseErr := strconv.ParseInt(rawSessionID, 10, 64)
			if parseErr != nil || sessionID <= 0 {
				writePublicError(c, http.StatusBadRequest, "session_id_invalid", "invalid session_id", false)
				return
			}
			if parseBoolQuery(c.Query("referenced")) {
				files, err = fileRepo.ListReferencedBySession(userID, sessionID, limit+1, offset)
			} else if parseBoolQuery(c.Query("unreferenced")) {
				files, err = fileRepo.ListUnreferencedBySession(userID, sessionID, limit+1, offset)
			} else {
				files, err = fileRepo.ListBySession(userID, sessionID, limit+1, offset)
			}
		} else {
			files, err = fileRepo.ListByUser(userID, limit+1, offset)
		}
		if err != nil {
			writeServerError(c, http.StatusInternalServerError, "file_list_failed", "failed to list files", err)
			return
		}
		if files == nil {
			files = []*model.File{}
		}
		hasMore := len(files) > limit
		if hasMore {
			files = files[:limit]
		}

		c.JSON(http.StatusOK, gin.H{
			"files":       files,
			"total":       len(files),
			"has_more":    hasMore,
			"next_offset": offset + len(files),
		})
	}
}

// PreviewFileHandler 返回解析后的 sidecar 文本预览。
//
// 文档类附件已经不保存原始源文件，长期可读内容就是 extracted_text_path 指向的
// Markdown/text 文件；图片仍由缩略图/下载路径展示，不在这里读原图。
func PreviewFileHandler(fileRepo *repository.FileRepository, _ *service.ChannelService, _ *extractor.SidecarClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			writePublicError(c, http.StatusBadRequest, "file_id_invalid", "invalid file id", false)
			return
		}
		maxChars := parsePreviewMaxChars(c.Query("max_chars"))
		cursor := c.Query("cursor")

		f, err := fileRepo.GetByID(id, userID)
		if err != nil {
			writeFileLookupError(c, err)
			return
		}
		if strings.HasPrefix(f.FileType, "image/") {
			c.JSON(http.StatusOK, gin.H{"file": f, "content": "", "next_cursor": "", "has_more": false, "truncated": false, "is_image": true})
			return
		}
		if f.ExtractedTextPath == nil || strings.TrimSpace(*f.ExtractedTextPath) == "" {
			retryable := f.ExtractStatus == "pending" || f.ExtractStatus == "ocr_pending" || f.ExtractStatus == "ocr_running"
			c.JSON(http.StatusOK, gin.H{"file": f, "content": "", "next_cursor": "", "has_more": false, "truncated": false, "error": "no extracted text", "code": "file_text_unavailable", "retryable": retryable})
			return
		}
		path, pathErr := filepolicy.ExistingPath(*f.ExtractedTextPath)
		if pathErr != nil {
			writeServerError(c, http.StatusInternalServerError, "file_preview_failed", "failed to read extracted text", pathErr)
			return
		}
		chunk, err := service.ReadFilePreviewChunk(path, cursor, maxChars)
		if errors.Is(err, service.ErrInvalidPreviewCursor) {
			writePublicError(c, http.StatusBadRequest, "preview_cursor_invalid", "invalid preview cursor", false)
			return
		}
		if err != nil {
			writeServerError(c, http.StatusInternalServerError, "file_preview_failed", "failed to read extracted text", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"file":        f,
			"content":     chunk.Content,
			"next_cursor": chunk.NextCursor,
			"has_more":    chunk.HasMore,
			"truncated":   chunk.HasMore,
			"is_image":    false,
		})
	}
}

func RefreshOCRFileHandler(fileRepo *repository.FileRepository, channelService *service.ChannelService, extractorClient *extractor.SidecarClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			writePublicError(c, http.StatusBadRequest, "file_id_invalid", "invalid file id", false)
			return
		}
		f, err := fileRepo.GetByID(id, userID)
		if err != nil {
			writeFileLookupError(c, err)
			return
		}
		c.JSON(http.StatusOK, f)
	}
}

func RetryOCRFileHandler(fileRepo *repository.FileRepository, configRepo *repository.ConfigRepository, channelService *service.ChannelService, extractorClient *extractor.SidecarClient, runner *OCRRecoveryRunner) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 {
			writePublicError(c, http.StatusBadRequest, "file_id_invalid", "invalid file id", false)
			return
		}
		policy, err := resolveAttachmentProcessingPolicy(c.Request.Context(), configRepo)
		if err != nil {
			writeServerError(c, http.StatusServiceUnavailable, "attachment_policy_unavailable", "附件处理策略暂不可用，请稍后重试", err)
			return
		}
		if !policy.Enabled {
			writePublicError(c, http.StatusConflict, "attachment_extract_disabled", "附件文本解析已关闭，无法重试 OCR", false)
			return
		}
		if policy.Degraded {
			log.Printf("[file_ocr] retry policy degraded; using last-known-good controls file_id=%d", id)
		}
		if channelService == nil || extractorClient == nil || !extractorClient.Enabled() || runner == nil {
			writeServerError(c, http.StatusServiceUnavailable, "ocr_runtime_unavailable", "OCR runtime is temporarily unavailable", errors.New("OCR retry runtime dependency is unavailable"))
			return
		}
		ocrConfig, err := channelService.ResolveMinerUOCRConfigContext(c.Request.Context())
		if err != nil {
			writeServerError(c, http.StatusServiceUnavailable, "ocr_config_unavailable", "OCR configuration is temporarily unavailable", err)
			return
		}
		if !ocrConfig.Enabled {
			writePublicError(c, http.StatusConflict, "ocr_service_unavailable", "OCR 服务未启用，请联系管理员", false)
			return
		}
		file, err := fileRepo.RestartOCR(id, userID, time.Now(), time.Now().Add(-ocrSourceRetention))
		if err != nil {
			writeOCRRetryMutationError(c, err)
			return
		}
		if file.OCRSourcePath == nil || strings.TrimSpace(*file.OCRSourcePath) == "" {
			cause := errors.New("OCR source path is missing after restart")
			if err := markOCRSourceUnavailable(fileRepo, file.ID, userID, cause); err != nil {
				writeServerError(c, http.StatusInternalServerError, "ocr_source_state_update_failed", "failed to reconcile OCR source state", err)
				return
			}
			writeOCRSourceUnavailable(c)
			return
		}
		sourcePath, pathErr := managedUploadPath(*file.OCRSourcePath)
		if pathErr != nil {
			if err := markOCRSourceUnavailable(fileRepo, file.ID, userID, pathErr); err != nil {
				writeServerError(c, http.StatusInternalServerError, "ocr_source_state_update_failed", "failed to reconcile OCR source state", err)
				return
			}
			writeServerError(c, http.StatusInternalServerError, "ocr_source_path_invalid", "OCR source path is invalid", pathErr)
			return
		}
		if _, err := os.Stat(sourcePath); err != nil {
			if !os.IsNotExist(err) {
				writeServerError(c, http.StatusInternalServerError, "ocr_source_check_failed", "failed to verify OCR source", err)
				return
			}
			if reconcileErr := markOCRSourceUnavailable(fileRepo, file.ID, userID, err); reconcileErr != nil {
				writeServerError(c, http.StatusInternalServerError, "ocr_source_state_update_failed", "failed to reconcile OCR source state", reconcileErr)
				return
			}
			writeOCRSourceUnavailable(c)
			return
		}
		runner.Wake()
		c.JSON(http.StatusOK, file)
	}
}

func parsePreviewMaxChars(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return 16000
	}
	if n > 40000 {
		return 40000
	}
	return n
}

// DeleteFileHandler makes an attachment unavailable immediately and retains its managed bytes for cleanup.
func DeleteFileHandler(fileRepo *repository.FileRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 {
			writePublicError(c, http.StatusBadRequest, "file_id_invalid", "invalid file id", false)
			return
		}

		f, err := fileRepo.GetByID(id, userID)
		if err != nil {
			writeFileLookupError(c, err)
			return
		}
		if _, err := managedUploadPath(f.FilePath); err != nil {
			logger.Error("refuse deleting file with unsafe path: user=%d file=%d path=%q err=%v", userID, id, f.FilePath, err)
			writeServerError(c, http.StatusInternalServerError, "file_path_invalid", "file path is outside managed storage", err)
			return
		}
		if f.ExtractedTextPath != nil && strings.TrimSpace(*f.ExtractedTextPath) != "" {
			if _, err := managedUploadPath(*f.ExtractedTextPath); err != nil {
				logger.Error("refuse deleting file with unsafe extracted path: user=%d file=%d path=%q err=%v", userID, id, *f.ExtractedTextPath, err)
				writeServerError(c, http.StatusInternalServerError, "file_path_invalid", "file path is outside managed storage", err)
				return
			}
		}
		if f.OCRSourcePath != nil && strings.TrimSpace(*f.OCRSourcePath) != "" {
			if _, err := managedUploadPath(*f.OCRSourcePath); err != nil {
				logger.Error("refuse deleting file with unsafe OCR source path: user=%d file=%d path=%q err=%v", userID, id, *f.OCRSourcePath, err)
				writeServerError(c, http.StatusInternalServerError, "file_path_invalid", "file path is outside managed storage", err)
				return
			}
		}
		now := time.Now()
		if err := fileRepo.RequestDeletion(c.Request.Context(), id, userID, now, now.Add(ocrSourceRetention)); err != nil {
			logger.Error("failed to request file deletion: user=%d file=%d err=%v", userID, id, err)
			writeFileDeletionError(c, err)
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "file deleted", "cleanup_after": now.Add(ocrSourceRetention)})
	}
}

// CleanupOrphanFilesHandler 清理陈旧暂存文件和删除会话保留期已满的附件。
//
// 这是管理员手动维护入口，不做后台定时任务：小团队自托管场景下，显式按钮/脚本比
// 隐式 cron 更容易理解和排障。默认保留 24 小时：未发送附件按上传时间计算，
// 删除会话的附件按会话删除时间计算；仍在运行的 OCR 会等任务结束，避免迟到结果
// 在物理清理后再次写入。
func CleanupOrphanFilesHandler(fileRepo *repository.FileRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		olderThanHours, ok := parseCleanupBoundedPositiveInt(c, "older_than_hours", 24, 1, 24*30)
		if !ok {
			return
		}
		limit, ok := parseCleanupBoundedPositiveInt(c, "limit", 100, 1, 1000)
		if !ok {
			return
		}
		cutoff := time.Now().Add(-time.Duration(olderThanHours) * time.Hour)

		now := time.Now()
		referenced, err := fileRepo.CountStaleReferencedFiles(cutoff)
		if err != nil {
			writeServerError(c, http.StatusInternalServerError, "file_cleanup_reference_count_failed", "failed to count referenced files", err)
			return
		}
		expiredOCR, err := fileRepo.ExpireStaleOCROriginals(cutoff, now, limit)
		if err != nil {
			writeServerError(c, http.StatusInternalServerError, "ocr_source_expire_failed", "failed to expire OCR source files", err)
			return
		}
		claims, err := fileRepo.ClaimFilesForStorageCleanup(c.Request.Context(), cutoff, now, 2*time.Minute, limit)
		if err != nil {
			writeServerError(c, http.StatusInternalServerError, "file_cleanup_claim_failed", "failed to claim files for cleanup", err)
			return
		}

		removed := 0
		removedFileIDs := make(map[int64]struct{}, len(claims))
		failures := make([]fileCleanupFailure, 0)
		for _, claim := range claims {
			f := claim.File
			if err := removeManagedFilePaths(f.FilePath, f.ExtractedTextPath, f.OCRSourcePath); err != nil {
				logger.Error("failed to remove orphan file paths: file=%d path=%q err=%v", f.ID, f.FilePath, err)
				failures = append(failures, releaseOrCleanupFailure(c, fileRepo, f.ID, claim.Token, "file_cleanup_remove_failed", "failed to remove file paths"))
				continue
			}
			if err := fileRepo.FinalizeFileStorageRemoval(c.Request.Context(), f.ID, claim.Token); err != nil {
				logger.Error("failed to finalize orphan file cleanup: file=%d err=%v", f.ID, err)
				failures = append(failures, releaseOrCleanupFailure(c, fileRepo, f.ID, claim.Token, "file_cleanup_finalize_failed", "failed to finalize file cleanup"))
				continue
			}
			removed++
			removedFileIDs[f.ID] = struct{}{}
		}
		expiredRemoved := 0
		for _, f := range expiredOCR {
			if _, ok := removedFileIDs[f.ID]; ok {
				expiredRemoved++
				continue
			}
			if err := removeManagedFilePaths("", nil, f.OCRSourcePath); err != nil {
				logger.Error("failed to remove expired OCR source: file=%d err=%v", f.ID, err)
				failures = append(failures, newFileCleanupFailure(f.ID, "ocr_source_remove_failed", "failed to remove OCR source"))
				continue
			}
			if err := fileRepo.ClearOCRSourcePath(f.ID, f.UserID, valueOrEmpty(f.OCRSourcePath)); err != nil {
				logger.Error("failed to clear expired OCR source path: file=%d err=%v", f.ID, err)
				failures = append(failures, newFileCleanupFailure(f.ID, "ocr_source_finalize_failed", "failed to finalize OCR source cleanup"))
				continue
			}
			expiredRemoved++
		}

		payload := gin.H{
			"marked":              len(claims),
			"removed":             removed,
			"failed":              len(failures),
			"failures":            failures,
			"skipped_referenced":  referenced,
			"ocr_expired_count":   len(expiredOCR),
			"ocr_expired_removed": expiredRemoved,
			"older_than_hours":    olderThanHours,
		}
		if len(failures) > 0 && c.GetString("request_id") != "" {
			payload["request_id"] = c.GetString("request_id")
		}
		c.JSON(http.StatusOK, payload)
	}
}

func parseBoundedPositiveInt(raw string, fallback, min, max int) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return fallback
	}
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}
