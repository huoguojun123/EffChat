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
			writeSkillError(c, "list", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"skills": skills})
	}
}

func UpdateSessionSkillsHandler(skillService *service.SkillService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		sessionID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || sessionID <= 0 {
			writePublicError(c, http.StatusBadRequest, "session_id_invalid", "invalid session id", false)
			return
		}
		var req service.SessionSkillsRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}
		skills, err := skillService.UpdateSessionSkills(sessionID, userID, req.Skills)
		if err != nil {
			writeSkillError(c, "update_session", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"skills_enabled": skills})
	}
}

func ListAdminSkillsHandler(skillService *service.SkillService) gin.HandlerFunc {
	return func(c *gin.Context) {
		skills, err := skillService.ListAdmin()
		if err != nil {
			writeSkillError(c, "list", err)
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
			writeSkillError(c, "create", err)
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
			writeSkillError(c, "update", err)
			return
		}
		c.JSON(http.StatusOK, skill)
	}
}

func DeleteSkillHandler(skillService *service.SkillService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := skillService.Delete(c.Param("id")); err != nil {
			writeSkillError(c, "delete", err)
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
			writeSkillError(c, "list_files", err)
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
			writeSkillError(c, "read_file", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"file": file, "content": content})
	}
}

func ListAdminSkillFilesHandler(skillService *service.SkillService) gin.HandlerFunc {
	return func(c *gin.Context) {
		files, err := skillService.ListFilesAdmin(c.Param("id"))
		if err != nil {
			writeSkillError(c, "list_files", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"files": files})
	}
}

func ReadAdminSkillFileHandler(skillService *service.SkillService) gin.HandlerFunc {
	return func(c *gin.Context) {
		content, file, err := skillService.ReadFileAdmin(c.Param("id"), c.Query("path"))
		if err != nil {
			writeSkillError(c, "read_file", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"file": file, "content": content})
	}
}

func ListSkillImportRecordsHandler(skillService *service.SkillService) gin.HandlerFunc {
	return func(c *gin.Context) {
		records, err := skillService.ListImportRecords(c.Param("id"))
		if err != nil {
			writeSkillError(c, "list_import_records", err)
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
			writeSkillError(c, "preview_update", err)
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
			writeSkillError(c, "update", err)
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
			writeSkillError(c, "preview_update", err)
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
			writePublicError(c, http.StatusBadRequest, "skill_invalid", "source_path is required", false)
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
			writeSkillError(c, "update", err)
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
			writeSkillError(c, "import", err)
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
			writeSkillError(c, "preview_import", err)
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
			writeSkillError(c, "import", err)
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
			writeSkillError(c, "preview_import", err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

func readZipUpload(c *gin.Context) ([]byte, bool) {
	file, err := c.FormFile("file")
	if err != nil {
		writePublicError(c, http.StatusBadRequest, "skill_archive_required", "zip file is required", false)
		return nil, false
	}
	if file.Size <= 0 {
		writePublicError(c, http.StatusBadRequest, "skill_archive_empty", "zip file is empty", false)
		return nil, false
	}
	if file.Size > skillparser.MaxArchiveBytes {
		writePublicError(c, http.StatusRequestEntityTooLarge, "skill_archive_too_large", "zip file exceeds size limit", false)
		return nil, false
	}
	src, err := file.Open()
	if err != nil {
		writeServerError(c, http.StatusInternalServerError, "skill_archive_open_failed", "failed to open zip file", err)
		return nil, false
	}
	defer src.Close()
	data, err := io.ReadAll(io.LimitReader(src, skillparser.MaxArchiveBytes+1))
	if err != nil {
		writeServerError(c, http.StatusInternalServerError, "skill_archive_read_failed", "failed to read zip file", err)
		return nil, false
	}
	if int64(len(data)) > skillparser.MaxArchiveBytes {
		writePublicError(c, http.StatusRequestEntityTooLarge, "skill_archive_too_large", "zip file exceeds size limit", false)
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
		writePublicError(c, http.StatusBadRequest, "skill_selection_invalid", "invalid selected_paths", false)
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
		writePublicError(c, http.StatusBadRequest, "skill_selection_invalid", "invalid selected_files", false)
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
		writePublicError(c, http.StatusBadRequest, "skill_selection_invalid", "invalid selected_files", false)
		return nil, false
	}
	return selected, true
}

func UpdateUserSkillsHandler(skillService *service.SkillService) gin.HandlerFunc {
	return func(c *gin.Context) {
		writePublicError(c, http.StatusGone, "skill_user_permissions_deprecated", "per-user skill permissions are deprecated; use skill min_group_level instead", false)
	}
}
