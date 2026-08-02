package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/service"
)

func writeSkillError(c *gin.Context, operation string, err error) {
	var publicErr *service.SkillError
	if errors.As(err, &publicErr) {
		switch publicErr.Kind {
		case service.SkillErrorInvalid:
			writePublicError(c, http.StatusBadRequest, "skill_invalid", publicErr.Message, false)
		case service.SkillErrorNotFound:
			writePublicError(c, http.StatusNotFound, "skill_not_found", publicErr.Message, false)
		case service.SkillErrorNotAuthorized:
			writePublicError(c, http.StatusForbidden, "skill_not_authorized", publicErr.Message, false)
		case service.SkillErrorConflict:
			writePublicError(c, http.StatusConflict, "skill_conflict", publicErr.Message, false)
		case service.SkillErrorSessionNotFound:
			writePublicError(c, http.StatusNotFound, "session_not_found", publicErr.Message, false)
		case service.SkillErrorSourceUnavailable:
			writeServerError(c, http.StatusBadGateway, "skill_source_unavailable", publicErr.Message, err)
		default:
			writeServerError(c, http.StatusInternalServerError, "skill_"+operation+"_failed", "failed to "+operation+" Skill", err)
		}
		return
	}
	writeServerError(c, http.StatusInternalServerError, "skill_"+operation+"_failed", "failed to "+operation+" Skill", err)
}
