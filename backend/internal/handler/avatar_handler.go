package handler

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/huoguojun123/EffChat/internal/avatar"
	"github.com/huoguojun123/EffChat/internal/middleware"
	"github.com/huoguojun123/EffChat/internal/service"
)

const avatarURLPrefix = "/api/v1/avatars/"

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
	oldUser, err := h.authService.GetProfile(userID)
	if err != nil {
		_ = os.Remove(storedPath)
		writeAvatarAccountError(c, "profile", err)
		return
	}
	avatarURL := avatarURLPrefix + filename
	user, err := h.authService.UpdateAvatar(userID, &avatarURL)
	if err != nil {
		_ = os.Remove(storedPath)
		writeAvatarAccountError(c, "update", err)
		return
	}
	h.removeManaged(oldUser.AvatarURL)
	c.JSON(http.StatusOK, user)
}

func (h *AvatarHandler) Delete(c *gin.Context) {
	userID := middleware.GetUserID(c)
	oldUser, err := h.authService.GetProfile(userID)
	if err != nil {
		writeAvatarAccountError(c, "profile", err)
		return
	}
	user, err := h.authService.UpdateAvatar(userID, nil)
	if err != nil {
		writeAvatarAccountError(c, "delete", err)
		return
	}
	h.removeManaged(oldUser.AvatarURL)
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

func (h *AvatarHandler) removeManaged(avatarURL *string) {
	if avatarURL == nil || !strings.HasPrefix(*avatarURL, avatarURLPrefix) {
		return
	}
	filename := strings.TrimPrefix(*avatarURL, avatarURLPrefix)
	if validAvatarFilename(filename) {
		_ = os.Remove(filepath.Join(h.storageDir, filename))
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
