package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/effchat/internal/middleware"
	"github.com/huoguojun123/effchat/internal/service"
)

func ListSessionFoldersHandler(folderService *service.SessionFolderService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		folders, err := folderService.List(userID)
		if err != nil {
			writeServerError(c, http.StatusInternalServerError, "session_folder_list_failed", "failed to list session folders", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"folders": folders})
	}
}

func CreateSessionFolderHandler(folderService *service.SessionFolderService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		var req service.CreateSessionFolderRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}
		folder, err := folderService.Create(userID, &req)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusCreated, folder)
	}
}

func UpdateSessionFolderHandler(folderService *service.SessionFolderService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid folder id"})
			return
		}
		var req service.UpdateSessionFolderRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}
		folder, err := folderService.Update(id, userID, &req)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, folder)
	}
}

func DeleteSessionFolderHandler(folderService *service.SessionFolderService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid folder id"})
			return
		}
		if err := folderService.Delete(id, userID); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "session folder deleted"})
	}
}
