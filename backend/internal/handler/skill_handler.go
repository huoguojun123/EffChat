package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/middleware"
	"github.com/huoguojun123/EffChat/internal/service"
	skillparser "github.com/huoguojun123/EffChat/internal/skill"
)

func ListSkillsHandler(skillService *service.SkillService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		skills, err := skillService.ListForUser(userID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"skills": skills})
	}
}

func UpdateSessionSkillsHandler(skillService *service.SkillService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		sessionID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session id"})
			return
		}
		var req service.SessionSkillsRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}
		skills, err := skillService.UpdateSessionSkills(sessionID, userID, req.Skills)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"skills_enabled": skills})
	}
}

func ListAdminSkillsHandler(skillService *service.SkillService) gin.HandlerFunc {
	return func(c *gin.Context) {
		skills, err := skillService.ListAdmin()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list skills"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"skills": skills})
	}
}

func CreateSkillHandler(skillService *service.SkillService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		var req service.SkillInput
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}
		skill, err := skillService.CreateManual(userID, &req)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusCreated, skill)
	}
}

func UpdateSkillHandler(skillService *service.SkillService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req service.SkillUpdateInput
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}
		skill, err := skillService.Update(c.Param("id"), &req)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, skill)
	}
}

func DeleteSkillHandler(skillService *service.SkillService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := skillService.Delete(c.Param("id")); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "skill deleted"})
	}
}

func ListSkillFilesHandler(skillService *service.SkillService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		files, err := skillService.ListFilesForUser(userID, c.Param("id"))
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"files": files})
	}
}

func ReadSkillFileHandler(skillService *service.SkillService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		content, file, err := skillService.ReadFileForUser(userID, c.Param("id"), c.Query("path"))
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"file": file, "content": content})
	}
}

func ListAdminSkillFilesHandler(skillService *service.SkillService) gin.HandlerFunc {
	return func(c *gin.Context) {
		files, err := skillService.ListFilesAdmin(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"files": files})
	}
}

func ReadAdminSkillFileHandler(skillService *service.SkillService) gin.HandlerFunc {
	return func(c *gin.Context) {
		content, file, err := skillService.ReadFileAdmin(c.Param("id"), c.Query("path"))
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"file": file, "content": content})
	}
}

func ListSkillImportRecordsHandler(skillService *service.SkillService) gin.HandlerFunc {
	return func(c *gin.Context) {
		records, err := skillService.ListImportRecords(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"records": records})
	}
}

func PreviewSkillGitUpdateHandler(skillService *service.SkillService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req service.SkillUpdateGitPreviewRequest
		if c.Request.ContentLength > 0 {
			if err := c.ShouldBindJSON(&req); err != nil {
				writeInvalidJSON(c)
				return
			}
		}
		result, err := skillService.PreviewGitUpdate(c.Request.Context(), c.Param("id"), &req)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

func ApplySkillGitUpdateHandler(skillService *service.SkillService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		var req service.SkillUpdateApplyRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}
		result, err := skillService.UpdateGit(c.Request.Context(), userID, c.Param("id"), &req)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

func PreviewSkillZipUpdateHandler(skillService *service.SkillService) gin.HandlerFunc {
	return func(c *gin.Context) {
		data, ok := readZipUpload(c)
		if !ok {
			return
		}
		result, err := skillService.PreviewZipUpdate(c.Param("id"), data)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

func ApplySkillZipUpdateHandler(skillService *service.SkillService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		data, ok := readZipUpload(c)
		if !ok {
			return
		}
		sourcePath := c.PostForm("source_path")
		if sourcePath == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "source_path is required"})
			return
		}
		selectedFiles, ok := selectedFileListFromForm(c)
		if !ok {
			return
		}
		result, err := skillService.UpdateZip(userID, c.Param("id"), data, &service.SkillUpdateApplyRequest{
			SourcePath:    sourcePath,
			SelectedFiles: selectedFiles,
		})
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

func ImportSkillsFromGitHandler(skillService *service.SkillService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		var req service.SkillGitImportRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}
		result, err := skillService.ImportGit(c.Request.Context(), userID, &req)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

func PreviewSkillsFromGitHandler(skillService *service.SkillService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req service.SkillGitPreviewRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}
		result, err := skillService.PreviewGit(c.Request.Context(), &req)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

func ImportSkillsFromZipHandler(skillService *service.SkillService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		data, ok := readZipUpload(c)
		if !ok {
			return
		}
		selectedPaths, ok := selectedPathsFromForm(c)
		if !ok {
			return
		}
		selectedFiles, ok := selectedFilesFromForm(c)
		if !ok {
			return
		}
		result, err := skillService.ImportZip(userID, data, selectedPaths, selectedFiles)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

func PreviewSkillsFromZipHandler(skillService *service.SkillService) gin.HandlerFunc {
	return func(c *gin.Context) {
		data, ok := readZipUpload(c)
		if !ok {
			return
		}
		result, err := skillService.PreviewZip(data)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

func readZipUpload(c *gin.Context) ([]byte, bool) {
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "zip file is required"})
		return nil, false
	}
	if file.Size <= 0 || file.Size > skillparser.MaxArchiveBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "zip file exceeds size limit"})
		return nil, false
	}
	src, err := file.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to open zip file"})
		return nil, false
	}
	defer src.Close()
	data, err := io.ReadAll(io.LimitReader(src, skillparser.MaxArchiveBytes+1))
	if err != nil || int64(len(data)) > skillparser.MaxArchiveBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read zip file"})
		return nil, false
	}
	return data, true
}

func selectedPathsFromForm(c *gin.Context) ([]string, bool) {
	raw, exists := c.GetPostForm("selected_paths")
	if !exists {
		return nil, true
	}
	var selected []string
	if err := json.Unmarshal([]byte(raw), &selected); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid selected_paths"})
		return nil, false
	}
	return selected, true
}

func selectedFilesFromForm(c *gin.Context) (map[string][]string, bool) {
	raw, exists := c.GetPostForm("selected_files")
	if !exists {
		return nil, true
	}
	var selected map[string][]string
	if err := json.Unmarshal([]byte(raw), &selected); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid selected_files"})
		return nil, false
	}
	return selected, true
}

func selectedFileListFromForm(c *gin.Context) ([]string, bool) {
	raw, exists := c.GetPostForm("selected_files")
	if !exists {
		return nil, true
	}
	var selected []string
	if err := json.Unmarshal([]byte(raw), &selected); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid selected_files"})
		return nil, false
	}
	return selected, true
}

func UpdateUserSkillsHandler(skillService *service.SkillService) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusGone, gin.H{"error": "per-user skill permissions are deprecated; use skill min_group_level instead"})
	}
}
