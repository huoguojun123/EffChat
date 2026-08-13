package handler

import (
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/middleware"
	"github.com/huoguojun123/EffChat/internal/repository"
)

func SearchConversationsHandler(searchRepo *repository.ConversationSearchRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		query := strings.TrimSpace(c.Query("q"))
		if count := utf8.RuneCountInString(query); count < 2 || count > 120 {
			writePublicError(c, http.StatusBadRequest, "conversation_search_query_invalid", "q must contain between 2 and 120 characters", false)
			return
		}

		scope := repository.ConversationSearchScope(strings.TrimSpace(c.DefaultQuery("scope", "all")))
		var folderID *int64
		switch scope {
		case repository.ConversationSearchAll, repository.ConversationSearchUnfiled:
		case repository.ConversationSearchFolder:
			id, err := strconv.ParseInt(c.Query("folder_id"), 10, 64)
			if err != nil || id <= 0 {
				writePublicError(c, http.StatusBadRequest, "conversation_search_query_invalid", "folder_id must be a positive integer for folder scope", false)
				return
			}
			folderID = &id
		default:
			writePublicError(c, http.StatusBadRequest, "conversation_search_query_invalid", "scope must be all, unfiled, or folder", false)
			return
		}

		limit := 30
		if raw := c.Query("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 || parsed > 50 {
				writePublicError(c, http.StatusBadRequest, "conversation_search_query_invalid", "limit must be between 1 and 50", false)
				return
			}
			limit = parsed
		}
		results, err := searchRepo.Search(c.Request.Context(), middleware.GetUserID(c), query, scope, folderID, limit)
		if err != nil {
			writeServerError(c, http.StatusInternalServerError, "conversation_search_failed", "failed to search conversations", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"results": results})
	}
}
