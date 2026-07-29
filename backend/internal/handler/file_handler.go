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

type uploadLimits struct {
	MaxFileSizeMB   int      `json:"max_file_size_mb"`
	MaxSessionFiles int      `json:"max_session_files"`
	AllowedTypes    []string `json:"allowed_types"`
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

func resolveUploadLimits(configRepo *repository.ConfigRepository) uploadLimits {
	limits := uploadLimits{
		MaxFileSizeMB:   20,
		MaxSessionFiles: 50,
		AllowedTypes:    append([]string(nil), repository.DefaultUploadAllowedTypes...),
	}
	if configRepo != nil {
		limits.MaxFileSizeMB = configRepo.GetInt("file_upload_max_size_mb", limits.MaxFileSizeMB)
		limits.MaxSessionFiles = configRepo.GetInt("file_upload_max_session_files", limits.MaxSessionFiles)
		limits.AllowedTypes = configRepo.GetStringSlice("file_upload_allowed_types", limits.AllowedTypes)
	}
	if limits.MaxFileSizeMB <= 0 {
		limits.MaxFileSizeMB = 20
	}
	if limits.MaxSessionFiles <= 0 {
		limits.MaxSessionFiles = 50
	}
	if len(limits.AllowedTypes) == 0 {
		limits.AllowedTypes = append([]string(nil), repository.DefaultUploadAllowedTypes...)
	}
	limits.AllowedTypes = normalizeUploadAllowedTypes(limits.AllowedTypes)
	return limits
}

func normalizeUploadAllowedTypes(allowedTypes []string) []string {
	normalized := make([]string, 0, len(allowedTypes)+3)
	for _, contentType := range allowedTypes {
		if contentType == "image/*" {
			normalized = append(normalized, "image/png", "image/jpeg", "image/gif", "image/webp")
			continue
		}
		normalized = append(normalized, contentType)
	}
	return normalized
}

// UploadLimitsHandler 返回当前登录用户上传前端预校验所需的全局限制。
//
// 后端仍是最终裁判；这个接口只负责让 ChatInput 不再硬编码“5 个 / 20MB”，
// 避免管理员改了系统配置后前端提示和真实上传行为漂移。
func UploadLimitsHandler(configRepo *repository.ConfigRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, resolveUploadLimits(configRepo))
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
		limits := resolveUploadLimits(configRepo)
		extractTimeoutSeconds := 60
		maxOutputMB := 5
		if configRepo != nil {
			extractTimeoutSeconds = configRepo.GetInt("attachment_extract_timeout_seconds", extractTimeoutSeconds)
			maxOutputMB = configRepo.GetInt("attachment_max_output_mb", maxOutputMB)
		}
		if extractTimeoutSeconds <= 0 {
			extractTimeoutSeconds = 60
		}
		if maxOutputMB <= 0 {
			maxOutputMB = 5
		}
		maxUploadSize := int64(limits.MaxFileSizeMB) << 20
		maxOutputBytes := int64(maxOutputMB) << 20
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadSize+multipartOverheadLimit)

		file, header, err := c.Request.FormFile("file")
		if err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": fmt.Sprintf("file too large (max %dMB)", limits.MaxFileSizeMB)})
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": "file is required"})
			return
		}
		defer file.Close()

		sessionID, ok := parseRequiredSessionID(c)
		if !ok {
			return
		}
		if opts.sessionRepo != nil {
			if _, err := opts.sessionRepo.GetByID(sessionID, userID); err != nil {
				c.JSON(http.StatusForbidden, gin.H{"error": "session not found or access denied"})
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
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("file too large (max %dMB)", limits.MaxFileSizeMB)})
			return
		}

		// 读取文件内容（用于 hash、嗅探、提取）。必须先读再校验：白名单不能只信
		// multipart 头声明的 Content-Type，否则伪装成 text/plain 的二进制即可绕过。
		content, err := io.ReadAll(io.LimitReader(file, maxUploadSize+1))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read file"})
			return
		}
		if int64(len(content)) > maxUploadSize {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("file too large (max %dMB)", limits.MaxFileSizeMB)})
			return
		}

		// declared + 内容嗅探 reconcile，得出可信类型再过白名单。
		contentType, ok := extractor.ResolveUploadType(declaredType, content, safeName)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "file content does not match its declared type"})
			return
		}
		if !isAllowedContentType(contentType, limits.AllowedTypes) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "file type not allowed"})
			return
		}
		if strings.HasPrefix(contentType, "image/") {
			verifiedType, err := validateUploadImage(content, contentType)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			contentType = verifiedType
		}

		hash := fmt.Sprintf("%x", sha256.Sum256(content))

		if existing, err := fileRepo.FindActiveByHashInSession(userID, sessionID, hash, int64(len(content))); err == nil {
			c.JSON(http.StatusOK, existing)
			return
		} else if !errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check duplicate file"})
			return
		}

		if count, err := fileRepo.CountActiveBySession(userID, sessionID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check session file count"})
			return
		} else if count >= limits.MaxSessionFiles {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("session has too many active files (max %d)", limits.MaxSessionFiles)})
			return
		}

		// 生成存储路径名。文档类解析成功后只保留解析文本；图片类才保留原始文件。
		userDir := uploadUserMonthDir(userID, time.Now())
		if err := os.MkdirAll(userDir, 0755); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create upload directory"})
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
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save file"})
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
		extractEnabled := true
		if configRepo != nil {
			extractEnabled = configRepo.GetBool("attachment_extract_enabled", true)
		}
		if !isImage {
			if !extractEnabled {
				c.JSON(http.StatusBadRequest, gin.H{"error": "附件文本解析已关闭，无法保存文档附件"})
				return
			}
			if isPDFUpload(contentType, safeName) {
				log.Printf("[file_ocr] pdf_upload_start user=%d session=%d file=%q bytes=%d strategy=mineru_async_first", userID, sessionID, safeName, len(content))
				if queuedFile, status, body := queueMinerUOCR(c.Request.Context(), fileRepo, opts, f, userID, sessionID, content, contentType, safeName, storedName, ocrStagingUserMonthDir(userID, time.Now()), extractTimeoutSeconds, maxOutputBytes); queuedFile != nil {
					c.JSON(status, queuedFile)
					return
				} else if body != nil {
					c.JSON(status, body)
					return
				}
				log.Printf("[file_ocr] local_fallback_start user=%d session=%d file=%q", userID, sessionID, safeName)
			}

			extractCtx, cancel := context.WithTimeout(c.Request.Context(), time.Duration(extractTimeoutSeconds)*time.Second)
			defer cancel()
			res, err := extractor.ExtractWithSidecar(extractCtx, content, contentType, safeName, opts.extractorClient)
			if err == extractor.ErrUnsupported {
				c.JSON(http.StatusBadRequest, gin.H{"error": "文件类型暂不支持解析，请转换为 PDF、Word、Excel、Markdown 或文本后重新上传"})
				return
			} else if err != nil {
				if isPDFUpload(contentType, safeName) && shouldOfferMinerUOCR(err) {
					log.Printf("[file_ocr] local_fallback_empty user=%d session=%d file=%q err=%v", userID, sessionID, safeName, err)
					writePDFExtractionFailure(c, 0, nil)
					return
				}
				log.Printf("[file_upload] extract_failed user=%d session=%d file=%q content_type=%s err=%v", userID, sessionID, safeName, contentType, err)
				c.JSON(http.StatusBadRequest, gin.H{"error": "文件解析失败，未保存附件，请重试或换一个可提取文字的文件"})
				return
			} else {
				if int64(len([]byte(res.Text))) > maxOutputBytes {
					c.JSON(http.StatusBadRequest, gin.H{
						"error": fmt.Sprintf("解析结果过大（上限 %dMB），请上传更小的文件", maxOutputMB),
					})
					return
				}
				if strings.TrimSpace(res.Text) == "" {
					if isPDFUpload(contentType, safeName) {
						log.Printf("[file_ocr] local_fallback_empty user=%d session=%d file=%q reason=empty_text", userID, sessionID, safeName)
						writePDFExtractionFailure(c, 0, nil)
						return
					}
					c.JSON(http.StatusBadRequest, gin.H{"error": "未能从文件提取到文字，未保存附件；如果是扫描件或图片 PDF，请先 OCR 后重新上传"})
					return
				}
				extractedPath, err := writeExtractedTextSidecar(userID, storedName, res.Text)
				if err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save extracted text"})
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
			c.JSON(http.StatusBadRequest, gin.H{"error": "文件未能保存，请重试"})
			return
		}

		if err := fileRepo.Create(f); err != nil {
			removeSavedFilePaths(f.FilePath, f.ExtractedTextPath)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save file metadata"})
			return
		}

		c.JSON(http.StatusCreated, f)
	}
}

func validateUploadImage(content []byte, declaredType string) (string, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(content))
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		return "", fmt.Errorf("image content is invalid or unsupported")
	}
	actualType, ok := map[string]string{
		"gif":  "image/gif",
		"jpeg": "image/jpeg",
		"png":  "image/png",
		"webp": "image/webp",
	}[format]
	if !ok || !strings.EqualFold(declaredType, actualType) {
		return "", fmt.Errorf("image content does not match its declared type")
	}
	if int64(config.Width)*int64(config.Height) > maxUploadImagePixels {
		return "", fmt.Errorf("image dimensions exceed the %d pixel limit", maxUploadImagePixels)
	}
	return actualType, nil
}

func parseRequiredSessionID(c *gin.Context) (int64, bool) {
	raw := strings.TrimSpace(c.PostForm("session_id"))
	if raw == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session_id is required"})
		return 0, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session_id"})
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
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid file id"})
			return
		}

		f, err := fileRepo.GetByID(id, userID)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
			return
		}

		path := f.FilePath
		filename := f.FileName
		contentType := f.FileType
		if !strings.HasPrefix(f.FileType, "image/") && f.ExtractStatus != "ready" {
			c.JSON(http.StatusConflict, gin.H{"error": "文件仍在解析中，暂不能下载；解析完成后请使用文本预览"})
			return
		}
		if f.ExtractedTextPath != nil && strings.TrimSpace(*f.ExtractedTextPath) != "" && !strings.HasPrefix(f.FileType, "image/") {
			path = *f.ExtractedTextPath
			filename = f.FileName + ".txt"
			contentType = "text/plain; charset=utf-8"
		}
		c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
		c.Header("Content-Type", contentType)
		path, err = filepolicy.ExistingPath(path)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "file storage is unavailable"})
			return
		}
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
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session_id"})
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list files"})
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
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid file id"})
			return
		}
		maxChars := parsePreviewMaxChars(c.Query("max_chars"))
		cursor := c.Query("cursor")

		f, err := fileRepo.GetByID(id, userID)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
			return
		}
		if strings.HasPrefix(f.FileType, "image/") {
			c.JSON(http.StatusOK, gin.H{"file": f, "content": "", "next_cursor": "", "has_more": false, "truncated": false, "is_image": true})
			return
		}
		if f.ExtractedTextPath == nil || strings.TrimSpace(*f.ExtractedTextPath) == "" {
			c.JSON(http.StatusOK, gin.H{"file": f, "content": "", "next_cursor": "", "has_more": false, "truncated": false, "error": "no extracted text"})
			return
		}
		path, pathErr := filepolicy.ExistingPath(*f.ExtractedTextPath)
		if pathErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read extracted text"})
			return
		}
		chunk, err := service.ReadFilePreviewChunk(path, cursor, maxChars)
		if errors.Is(err, service.ErrInvalidPreviewCursor) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid preview cursor"})
			return
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read extracted text"})
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
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid file id"})
			return
		}
		f, err := fileRepo.GetByID(id, userID)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
			return
		}
		c.JSON(http.StatusOK, f)
	}
}

func RetryOCRFileHandler(fileRepo *repository.FileRepository, channelService *service.ChannelService, extractorClient *extractor.SidecarClient, runner *OCRRecoveryRunner) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid file id"})
			return
		}
		if channelService == nil || extractorClient == nil || !extractorClient.Enabled() || !channelService.ResolveMinerUOCRConfig().Enabled || runner == nil {
			c.JSON(http.StatusConflict, gin.H{"error": "OCR 服务未启用，请联系管理员", "code": "ocr_service_unavailable"})
			return
		}
		file, err := fileRepo.RestartOCR(id, userID, time.Now(), time.Now().Add(-ocrSourceRetention))
		if errors.Is(err, repository.ErrOCRSourceUnavailable) {
			c.JSON(http.StatusConflict, gin.H{"error": "OCR 原文件已过期或不存在，无法重试", "code": "ocr_source_unavailable"})
			return
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to restart OCR"})
			return
		}
		if file.OCRSourcePath == nil || strings.TrimSpace(*file.OCRSourcePath) == "" {
			c.JSON(http.StatusConflict, gin.H{"error": "OCR 原文件已过期或不存在，无法重试", "code": "ocr_source_unavailable"})
			return
		}
		sourcePath, pathErr := managedUploadPath(*file.OCRSourcePath)
		if pathErr != nil {
			_ = fileRepo.FailOCR(file.ID, userID, "ocr_source_missing", "OCR 原文件不存在，无法继续解析")
			c.JSON(http.StatusConflict, gin.H{"error": "OCR 原文件已过期或不存在，无法重试", "code": "ocr_source_unavailable"})
			return
		}
		if _, err := os.Stat(sourcePath); err != nil {
			_ = fileRepo.FailOCR(file.ID, userID, "ocr_source_missing", "OCR 原文件不存在，无法继续解析")
			c.JSON(http.StatusConflict, gin.H{"error": "OCR 原文件已过期或不存在，无法重试", "code": "ocr_source_unavailable"})
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
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid file id"})
			return
		}

		f, err := fileRepo.GetByID(id, userID)
		if err != nil {
			status := http.StatusBadRequest
			message := "failed to load file"
			if errors.Is(err, repository.ErrNotFound) {
				status = http.StatusNotFound
				message = "file not found"
			} else {
				logger.Error("failed to load file before delete: user=%d file=%d err=%v", userID, id, err)
			}
			c.JSON(status, gin.H{"error": message})
			return
		}
		if _, err := managedUploadPath(f.FilePath); err != nil {
			logger.Error("refuse deleting file with unsafe path: user=%d file=%d path=%q err=%v", userID, id, f.FilePath, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "file path is outside managed storage"})
			return
		}
		if f.ExtractedTextPath != nil && strings.TrimSpace(*f.ExtractedTextPath) != "" {
			if _, err := managedUploadPath(*f.ExtractedTextPath); err != nil {
				logger.Error("refuse deleting file with unsafe extracted path: user=%d file=%d path=%q err=%v", userID, id, *f.ExtractedTextPath, err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "file path is outside managed storage"})
				return
			}
		}
		if f.OCRSourcePath != nil && strings.TrimSpace(*f.OCRSourcePath) != "" {
			if _, err := managedUploadPath(*f.OCRSourcePath); err != nil {
				logger.Error("refuse deleting file with unsafe OCR source path: user=%d file=%d path=%q err=%v", userID, id, *f.OCRSourcePath, err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "file path is outside managed storage"})
				return
			}
		}
		now := time.Now()
		if err := fileRepo.RequestDeletion(c.Request.Context(), id, userID, now, now.Add(ocrSourceRetention)); err != nil {
			logger.Error("failed to request file deletion: user=%d file=%d err=%v", userID, id, err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "failed to delete file"})
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
		olderThanHours := parseBoundedPositiveInt(c.Query("older_than_hours"), 24, 1, 24*30)
		limit := parseBoundedPositiveInt(c.Query("limit"), 100, 1, 1000)
		cutoff := time.Now().Add(-time.Duration(olderThanHours) * time.Hour)

		now := time.Now()
		expiredOCR, err := fileRepo.ExpireStaleOCROriginals(cutoff, now, limit)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to expire OCR source files"})
			return
		}
		claims, err := fileRepo.ClaimFilesForStorageCleanup(c.Request.Context(), cutoff, now, 2*time.Minute, limit)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to claim files for cleanup"})
			return
		}
		referenced, err := fileRepo.CountStaleReferencedFiles(cutoff)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to count referenced files"})
			return
		}

		removed := 0
		removedFileIDs := make(map[int64]struct{}, len(claims))
		failures := make([]gin.H, 0)
		for _, claim := range claims {
			f := claim.File
			if err := removeManagedFilePaths(f.FilePath, f.ExtractedTextPath, f.OCRSourcePath); err != nil {
				logger.Error("failed to remove orphan file paths: file=%d path=%q err=%v", f.ID, f.FilePath, err)
				_ = fileRepo.ReleaseFileStorageCleanupClaim(c.Request.Context(), f.ID, claim.Token)
				failures = append(failures, gin.H{"file_id": f.ID, "error": "failed to remove file paths"})
				continue
			}
			if err := fileRepo.FinalizeFileStorageRemoval(c.Request.Context(), f.ID, claim.Token); err != nil {
				logger.Error("failed to finalize orphan file cleanup: file=%d err=%v", f.ID, err)
				_ = fileRepo.ReleaseFileStorageCleanupClaim(c.Request.Context(), f.ID, claim.Token)
				failures = append(failures, gin.H{"file_id": f.ID, "error": "failed to finalize file cleanup"})
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
				failures = append(failures, gin.H{"file_id": f.ID, "error": "failed to remove OCR source"})
				continue
			}
			if err := fileRepo.ClearOCRSourcePath(f.ID, f.UserID, valueOrEmpty(f.OCRSourcePath)); err != nil {
				logger.Error("failed to clear expired OCR source path: file=%d err=%v", f.ID, err)
				failures = append(failures, gin.H{"file_id": f.ID, "error": "failed to finalize OCR source cleanup"})
				continue
			}
			expiredRemoved++
		}

		c.JSON(http.StatusOK, gin.H{
			"marked":              len(claims),
			"removed":             removed,
			"failed":              len(failures),
			"failures":            failures,
			"skipped_referenced":  referenced,
			"ocr_expired_count":   len(expiredOCR),
			"ocr_expired_removed": expiredRemoved,
			"older_than_hours":    olderThanHours,
		})
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
