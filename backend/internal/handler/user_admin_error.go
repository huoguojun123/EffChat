package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
)

func writeAdminUserError(c *gin.Context, operation string, err error) {
	switch {
	case errors.Is(err, service.ErrUserAdminInvalid):
		message := strings.TrimPrefix(err.Error(), service.ErrUserAdminInvalid.Error()+": ")
		writePublicError(c, http.StatusBadRequest, "admin_user_invalid", message, false)
	case errors.Is(err, repository.ErrNotFound):
		writePublicError(c, http.StatusNotFound, "admin_user_not_found", "user not found", false)
	case errors.Is(err, repository.ErrUserGroupMissing):
		writePublicError(c, http.StatusNotFound, "user_group_not_found", "user group not found", false)
	case errors.Is(err, repository.ErrUserConflict):
		writePublicError(c, http.StatusConflict, "admin_user_conflict", "username or email already exists", false)
	case errors.Is(err, repository.ErrLastActiveAdmin):
		writePublicError(c, http.StatusConflict, "last_active_admin_required", "at least one active administrator is required", false)
	case errors.Is(err, repository.ErrProtectedSuperAdmin):
		writePublicError(c, http.StatusConflict, "super_admin_protected", "the first registered super administrator cannot be demoted or disabled", false)
	default:
		message := "failed to " + strings.ReplaceAll(operation, "_", " ") + " user"
		switch operation {
		case "reset_password":
			message = "failed to reset user password"
		case "set_group":
			message = "failed to set user group"
		}
		writeServerError(c, http.StatusInternalServerError, "admin_user_"+operation+"_failed", message, err)
	}
}

func adminUserID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		writePublicError(c, http.StatusBadRequest, "admin_user_id_invalid", "invalid user id", false)
		return 0, false
	}
	return id, true
}

func adminUserPagination(c *gin.Context) (int, int, bool) {
	limit, offset := 50, 0
	if raw := c.Query("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 || value > 100 {
			writePublicError(c, http.StatusBadRequest, "admin_user_pagination_invalid", "limit must be between 1 and 100", false)
			return 0, 0, false
		}
		limit = value
	}
	if raw := c.Query("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			writePublicError(c, http.StatusBadRequest, "admin_user_pagination_invalid", "offset must be zero or greater", false)
			return 0, 0, false
		}
		offset = value
	}
	return limit, offset, true
}
