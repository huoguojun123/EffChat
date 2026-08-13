package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/huoguojun123/EffChat/internal/avatar"
	"github.com/huoguojun123/EffChat/internal/middleware"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
	"github.com/huoguojun123/EffChat/pkg/logger"
)

const avatarURLPrefix = "/api/v1/avatars/"
const avatarCleanupTimeout = 2 * time.Second

type AvatarHandler struct {
	authService *service.AuthService
	storageDir  string
}

func NewAvatarHandler(authService *service.AuthService, storageDir string) *AvatarHandler {
	return &AvatarHandler{authService: authService, storageDir: storageDir}
}

func (h *AvatarHandler) Upload(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, avatar.MaxInputBytes+multipartOverheadLimit)
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeAvatarTooLarge(c)
			return
		}
		writePublicError(c, http.StatusBadRequest, "avatar_file_required", "请选择头像图片", false)
		return
	}
	defer file.Close()

	if header.Size <= 0 {
		writePublicError(c, http.StatusBadRequest, "avatar_file_required", "请选择头像图片", false)
		return
	}
	if header.Size > avatar.MaxInputBytes {
		writeAvatarTooLarge(c)
		return
	}
	content, err := io.ReadAll(io.LimitReader(file, avatar.MaxInputBytes+1))
	if err != nil {
		writeServerError(c, http.StatusInternalServerError, "avatar_read_failed", "读取头像失败", err)
		return
	}
	if len(content) > avatar.MaxInputBytes {
		writeAvatarTooLarge(c)
		return
	}

	processed, err := avatar.Process(content)
	if err != nil {
		writeAvatarProcessError(c, err)
		return
	}
	if err := os.MkdirAll(h.storageDir, 0755); err != nil {
		writeServerError(c, http.StatusInternalServerError, "avatar_storage_unavailable", "无法创建头像目录", err)
		return
	}

	filename := uuid.NewString() + "." + processed.Ext
	storedPath := filepath.Join(h.storageDir, filename)
	if err := os.WriteFile(storedPath, processed.Data, 0644); err != nil {
		writeServerError(c, http.StatusInternalServerError, "avatar_store_failed", "保存头像失败", err)
		return
	}

	userID := middleware.GetUserID(c)
	avatarURL := avatarURLPrefix + filename
	user, replacedURL, err := h.authService.SwapAvatarContext(c.Request.Context(), userID, &avatarURL)
	if err != nil {
		// All pre-commit failures are rolled back, so this request still owns its
		// staged file. A commit error is different: the database may own either
		// URL, and both candidates must be reference-checked before deletion.
		if errors.Is(err, repository.ErrUserCommitUnknown) {
			h.removeManagedIfUnreferenced(&avatarURL)
			h.removeManagedIfUnreferenced(replacedURL)
		} else {
			_ = os.Remove(storedPath)
		}
		writeAvatarAccountError(c, "update", err)
		return
	}
	h.removeManagedIfUnreferenced(replacedURL)
	c.JSON(http.StatusOK, user)
}

func (h *AvatarHandler) Delete(c *gin.Context) {
	userID := middleware.GetUserID(c)
	user, replacedURL, err := h.authService.SwapAvatarContext(c.Request.Context(), userID, nil)
	if err != nil {
		if errors.Is(err, repository.ErrUserCommitUnknown) {
			h.removeManagedIfUnreferenced(replacedURL)
		}
		writeAvatarAccountError(c, "delete", err)
		return
	}
	h.removeManagedIfUnreferenced(replacedURL)
	c.JSON(http.StatusOK, user)
}

func (h *AvatarHandler) Serve(c *gin.Context) {
	filename := c.Param("filename")
	if !validAvatarFilename(filename) {
		c.Status(http.StatusNotFound)
		return
	}
	path := filepath.Join(h.storageDir, filename)
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			c.Status(http.StatusNotFound)
			return
		}
		writeServerError(c, http.StatusInternalServerError, "avatar_read_failed", "读取头像失败", err)
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		writeServerError(c, http.StatusInternalServerError, "avatar_read_failed", "读取头像失败", err)
		return
	}
	if !info.Mode().IsRegular() || info.Size() > avatar.MaxOutputBytes {
		c.Status(http.StatusNotFound)
		return
	}
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Header("X-Content-Type-Options", "nosniff")
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".jpg":
		c.DataFromReader(http.StatusOK, info.Size(), "image/jpeg", file, nil)
	case ".png":
		c.DataFromReader(http.StatusOK, info.Size(), "image/png", file, nil)
	case ".gif":
		c.DataFromReader(http.StatusOK, info.Size(), "image/gif", file, nil)
	default:
		c.Status(http.StatusNotFound)
	}
}

func (h *AvatarHandler) removeManagedIfUnreferenced(avatarURL *string) {
	if avatarURL == nil || !strings.HasPrefix(*avatarURL, avatarURLPrefix) {
		return
	}
	filename := strings.TrimPrefix(*avatarURL, avatarURLPrefix)
	if !validAvatarFilename(filename) {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), avatarCleanupTimeout)
	defer cancel()
	referenced, err := h.authService.IsAvatarURLReferencedContext(ctx, *avatarURL)
	if err != nil {
		logger.Error("check avatar reference before cleanup failed: url=%q err=%v", *avatarURL, err)
		return
	}
	if referenced {
		return
	}
	if err := os.Remove(filepath.Join(h.storageDir, filename)); err != nil && !os.IsNotExist(err) {
		logger.Error("remove unreferenced avatar failed: url=%q err=%v", *avatarURL, err)
	}
}

func validAvatarFilename(filename string) bool {
	if filename == "" || filepath.Base(filename) != filename {
		return false
	}
	ext := strings.ToLower(filepath.Ext(filename))
	if ext != ".jpg" && ext != ".png" && ext != ".gif" {
		return false
	}
	_, err := uuid.Parse(strings.TrimSuffix(filename, ext))
	return err == nil
}
