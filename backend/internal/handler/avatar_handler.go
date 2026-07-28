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
	"github.com/huoguojun123/effchat/internal/avatar"
	"github.com/huoguojun123/effchat/internal/middleware"
	"github.com/huoguojun123/effchat/internal/service"
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
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "头像文件不能超过 10 MiB"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "请选择头像图片"})
		return
	}
	defer file.Close()

	if header.Size <= 0 || header.Size > avatar.MaxInputBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "头像文件不能超过 10 MiB"})
		return
	}
	content, err := io.ReadAll(io.LimitReader(file, avatar.MaxInputBytes+1))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "读取头像失败"})
		return
	}
	if len(content) > avatar.MaxInputBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "头像文件不能超过 10 MiB"})
		return
	}

	processed, err := avatar.Process(content)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := os.MkdirAll(h.storageDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "无法创建头像目录"})
		return
	}

	filename := uuid.NewString() + "." + processed.Ext
	storedPath := filepath.Join(h.storageDir, filename)
	if err := os.WriteFile(storedPath, processed.Data, 0644); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存头像失败"})
		return
	}

	userID := middleware.GetUserID(c)
	oldUser, err := h.authService.GetProfile(userID)
	if err != nil {
		_ = os.Remove(storedPath)
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	avatarURL := avatarURLPrefix + filename
	user, err := h.authService.UpdateAvatar(userID, &avatarURL)
	if err != nil {
		_ = os.Remove(storedPath)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新头像失败"})
		return
	}
	h.removeManaged(oldUser.AvatarURL)
	c.JSON(http.StatusOK, user)
}

func (h *AvatarHandler) Delete(c *gin.Context) {
	userID := middleware.GetUserID(c)
	oldUser, err := h.authService.GetProfile(userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	user, err := h.authService.UpdateAvatar(userID, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "移除头像失败"})
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
		c.Status(http.StatusNotFound)
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > avatar.MaxOutputBytes {
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
