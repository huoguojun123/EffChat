package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
)

func writeUserProfileError(c *gin.Context, operation string, err error) {
	switch {
	case errors.Is(err, service.ErrUserProfileInvalid):
		message := strings.TrimPrefix(err.Error(), service.ErrUserProfileInvalid.Error()+": ")
		writePublicError(c, http.StatusBadRequest, "user_profile_invalid", message, false)
	case errors.Is(err, service.ErrIncorrectOldPassword):
		writePublicError(c, http.StatusBadRequest, "old_password_incorrect", "incorrect old password", false)
	case errors.Is(err, repository.ErrNotFound):
		writePublicError(c, http.StatusNotFound, "user_profile_not_found", "user not found", false)
	case errors.Is(err, repository.ErrUserConflict):
		writePublicError(c, http.StatusConflict, "user_profile_conflict", "email already exists", false)
	default:
		code := "user_profile_" + operation + "_failed"
		message := "failed to " + strings.ReplaceAll(operation, "_", " ")
		if operation == "load" {
			message = "failed to load user profile"
		} else if operation == "update" {
			message = "failed to update user profile"
		}
		writeServerError(c, http.StatusInternalServerError, code, message, err)
	}
}
