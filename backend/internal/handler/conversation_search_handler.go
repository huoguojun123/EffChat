package handler

import (
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/effchat/internal/middleware"
	"github.com/huoguojun123/effchat/internal/repository"
)

func SearchConversationsHandler(searchRepo *repository.ConversationSearchRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		query := strings.TrimSpace(c.Query("q"))
		if count := utf8.RuneCountInString(query); count < 2 || count > 120 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "q must contain between 2 and 120 characters"})
			return
		}

		scope := repository.ConversationSearchScope(strings.TrimSpace(c.DefaultQuery("scope", "all")))
		var folderID *int64
		switch scope {
		case repository.ConversationSearchAll, repository.ConversationSearchUnfiled:
		case repository.ConversationSearchFolder:
			id, err := strconv.ParseInt(c.Query("folder_id"), 10, 64)
			if err != nil || id <= 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "folder_id is required for folder scope"})
				return
			}
			folderID = &id
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "scope must be all, unfiled, or folder"})
			return
		}

		limit := parseBoundedPositiveInt(c.Query("limit"), 30, 1, 50)
		results, err := searchRepo.Search(c.Request.Context(), middleware.GetUserID(c), query, scope, folderID, limit)
		if err != nil {
			writeServerError(c, http.StatusInternalServerError, "conversation_search_failed", "failed to search conversations", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"results": results})
	}
}
