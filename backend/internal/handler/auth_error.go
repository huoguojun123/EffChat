package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
)

func writeAuthError(c *gin.Context, operation string, err error) {
	switch {
	case errors.Is(err, service.ErrUserRegistrationInvalid):
		message := strings.TrimPrefix(err.Error(), service.ErrUserRegistrationInvalid.Error()+": ")
		writePublicError(c, http.StatusBadRequest, "registration_invalid", message, false)
	case errors.Is(err, repository.ErrUserConflict):
		writePublicError(c, http.StatusConflict, "user_identity_conflict", "username or email already exists", false)
	case errors.Is(err, service.ErrAccountInactive):
		writePublicError(c, http.StatusUnauthorized, "account_inactive", "账号待审核或已停用", false)
	case errors.Is(err, service.ErrInvalidCredentials):
		writePublicError(c, http.StatusUnauthorized, "invalid_credentials", "账号或密码错误", false)
	default:
		message := "failed to " + operation
		writeServerError(c, http.StatusInternalServerError, "authentication_"+operation+"_failed", message, err)
	}
}

func writeAuthRateLimitError(c *gin.Context, retryAfter time.Duration) {
	c.Header("Retry-After", retryAfterSeconds(retryAfter))
	writePublicError(c, http.StatusTooManyRequests, "authentication_rate_limited", "too many authentication attempts", true)
}
