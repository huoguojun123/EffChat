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

func writeUserGroupError(c *gin.Context, operation string, err error) {
	switch {
	case errors.Is(err, service.ErrUserGroupInvalid):
		message := strings.TrimPrefix(err.Error(), service.ErrUserGroupInvalid.Error()+": ")
		writePublicError(c, http.StatusBadRequest, "user_group_invalid", message, false)
	case errors.Is(err, repository.ErrNotFound):
		writePublicError(c, http.StatusNotFound, "user_group_not_found", "user group not found", false)
	case errors.Is(err, repository.ErrUserGroupConflict):
		writePublicError(c, http.StatusConflict, "user_group_conflict", "user group name already exists", false)
	case errors.Is(err, repository.ErrDefaultUserGroupRequired):
		writePublicError(c, http.StatusConflict, "user_group_default_required", "another default user group is required", false)
	default:
		writeServerError(c, http.StatusInternalServerError, "user_group_"+operation+"_failed", "failed to "+operation+" user group", err)
	}
}

func userGroupID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		writePublicError(c, http.StatusBadRequest, "user_group_id_invalid", "invalid user group id", false)
		return 0, false
	}
	return id, true
}
