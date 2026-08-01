package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/avatar"
	"github.com/huoguojun123/EffChat/internal/repository"
)

func writeAvatarTooLarge(c *gin.Context) {
	writePublicError(c, http.StatusRequestEntityTooLarge, "avatar_too_large", "头像文件不能超过 10 MiB", false)
}

func writeAvatarProcessError(c *gin.Context, err error) {
	if errors.Is(err, avatar.ErrInvalidImage) || errors.Is(err, avatar.ErrImageTooLarge) || errors.Is(err, avatar.ErrCannotCompress) {
		writePublicError(c, http.StatusBadRequest, "avatar_invalid", err.Error(), false)
		return
	}
	writeServerError(c, http.StatusInternalServerError, "avatar_process_failed", "处理头像失败", err)
}

func writeAvatarAccountError(c *gin.Context, operation string, err error) {
	if errors.Is(err, repository.ErrNotFound) {
		writePublicError(c, http.StatusNotFound, "user_not_found", "user not found", false)
		return
	}
	message := "更新头像失败"
	if operation == "profile" {
		message = "读取用户资料失败"
	} else if operation == "delete" {
		message = "移除头像失败"
	}
	writeServerError(c, http.StatusInternalServerError, "avatar_"+operation+"_failed", message, err)
}
