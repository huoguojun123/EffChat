package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
)

func writeUserProfileLoadError(c *gin.Context, err error) {
	if errors.Is(err, repository.ErrNotFound) {
		writePublicError(c, http.StatusUnauthorized, "account_unavailable", "account is unavailable", false)
		return
	}
	writeServerError(c, http.StatusInternalServerError, "user_profile_load_failed", "failed to load user profile", err)
}

func writeMessageCreationError(c *gin.Context, err error) {
	if errors.Is(err, service.ErrSessionNotFound) {
		writePublicError(c, http.StatusNotFound, "session_not_found", "session not found", false)
		return
	}
	status, code, message, retryable := messageCreationFailure(err)
	if status >= http.StatusInternalServerError {
		writeServerError(c, status, code, message, err)
		return
	}
	writePublicError(c, status, code, message, retryable)
}

func writeRetryContextError(c *gin.Context, edited bool, err error) {
	switch {
	case errors.Is(err, service.ErrSessionNotFound):
		writePublicError(c, http.StatusNotFound, "session_not_found", "session not found", false)
	case errors.Is(err, service.ErrMessageAlreadyAnswered):
		writePublicError(c, http.StatusConflict, "message_already_answered", "助手已开始输出，不能再修改这条消息", false)
	case errors.Is(err, service.ErrMessageUnchanged):
		writePublicError(c, http.StatusBadRequest, "message_unchanged", "内容没有变化，请直接使用重试", false)
	case errors.Is(err, service.ErrRetryTargetStale):
		code := "retry_target_stale"
		message := "会话已有新消息，请刷新后重试最后一条"
		if edited {
			code = "edit_target_stale"
			message = "这条消息已不是会话末尾，请刷新后再编辑"
		}
		writePublicError(c, http.StatusConflict, code, message, false)
	case errors.Is(err, repository.ErrAttachmentUnavailable):
		writePublicError(c, http.StatusConflict, "attachment_unavailable", "原附件已删除，无法完整重试", false)
	case errors.Is(err, service.ErrInvalidMessageInput):
		writePublicError(c, http.StatusBadRequest, "message_input_invalid", "修改后的消息内容无效", false)
	case errors.Is(err, service.ErrMessageTooLarge):
		writePublicError(c, http.StatusRequestEntityTooLarge, "message_too_large", "消息过长，请缩短后重试", false)
	default:
		writeServerError(c, http.StatusInternalServerError, "retry_context_load_failed", "重试上下文加载失败，请重试", err)
	}
}

func writeCompactionUndoError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrSessionNotFound):
		writePublicError(c, http.StatusNotFound, "session_not_found", "session not found", false)
	case errors.Is(err, service.ErrCompactionNotFound):
		writePublicError(c, http.StatusNotFound, "compaction_checkpoint_not_found", "no compaction checkpoint to undo", false)
	case errors.Is(err, service.ErrCompactionUndoDenied):
		writePublicError(c, http.StatusConflict, "compaction_undo_not_allowed", "only the latest manual compaction can be undone", false)
	case errors.Is(err, service.ErrCompactionUndoStale):
		writePublicError(c, http.StatusConflict, "compaction_undo_stale", "cannot undo compaction after new messages", false)
	default:
		writeServerError(c, http.StatusInternalServerError, "compaction_undo_failed", "failed to undo compaction", err)
	}
}

func writeStreamUnavailable(c *gin.Context, err error) {
	writeServerError(c, http.StatusInternalServerError, "stream_unavailable", "streaming not supported", err)
}
