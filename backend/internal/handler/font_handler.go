package handler

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/filepolicy"
	"github.com/huoguojun123/EffChat/internal/middleware"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
)

const fontUploadDir = filepolicy.FontRoot
const maxFontUploadBytes = int64(30 << 20)

func ListAdminFontsHandler(fontRepo *repository.FontRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		fonts, err := fontRepo.List()
		if err != nil {
			writeFontError(c, "list", err)
			return
		}
		for _, font := range fonts {
			attachFontURL(font)
		}
		selectedID, err := fontRepo.GetSelectedID()
		if err != nil {
			writeFontError(c, "selection_load", err)
			return
		}
		selection, err := fontRepo.GetSelectedIDs()
		if err != nil {
			writeFontError(c, "selection_load", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"fonts": fonts, "selected_font_id": selectedID, "selected_font_ids": selection})
	}
}

func UploadFontHandler(fontRepo *repository.FontRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxFontUploadBytes+multipartOverheadLimit)

		file, header, err := c.Request.FormFile("file")
		if err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				writePublicError(c, http.StatusRequestEntityTooLarge, "font_file_too_large", "font file too large", false)
				return
			}
			writePublicError(c, http.StatusBadRequest, "font_file_required", "font file is required", false)
			return
		}
		defer file.Close()

		safeName := sanitizeUploadFilename(header.Filename)
		if header.Size <= 0 {
			writePublicError(c, http.StatusBadRequest, "font_file_empty", "font file is empty", false)
			return
		}
		if header.Size > maxFontUploadBytes {
			writePublicError(c, http.StatusRequestEntityTooLarge, "font_file_too_large", "font file too large", false)
			return
		}

		content, err := io.ReadAll(io.LimitReader(file, maxFontUploadBytes+1))
		if err != nil {
			writeServerError(c, http.StatusInternalServerError, "font_file_read_failed", "failed to read font file", err)
			return
		}
		if int64(len(content)) > maxFontUploadBytes {
			writePublicError(c, http.StatusRequestEntityTooLarge, "font_file_too_large", "font file too large", false)
			return
		}

		mimeType, err := validateFontContent(safeName, content)
		if err != nil {
			writePublicError(c, http.StatusBadRequest, "font_file_invalid", err.Error(), false)
			return
		}

		hash := fmt.Sprintf("%x", sha256.Sum256(content))
		if err := os.MkdirAll(fontUploadDir, 0755); err != nil {
			writeServerError(c, http.StatusInternalServerError, "font_storage_prepare_failed", "failed to prepare font storage", err)
			return
		}

		storedName := fmt.Sprintf("%d_%s_%s", time.Now().UnixNano(), hash[:12], safeName)
		storedPath := filepath.Join(fontUploadDir, storedName)
		if err := os.WriteFile(storedPath, content, 0644); err != nil {
			writeServerError(c, http.StatusInternalServerError, "font_file_save_failed", "failed to save font file", err)
			return
		}

		displayName := strings.TrimSpace(c.PostForm("display_name"))
		if displayName == "" {
			displayName = strings.TrimSuffix(safeName, filepath.Ext(safeName))
		}
		familyName := strings.TrimSpace(c.PostForm("family_name"))
		if familyName == "" {
			familyName = displayName
		}
		weight := parseFontWeight(c.PostForm("weight"))
		style := parseFontStyle(c.PostForm("style"))

		font := &model.FontAsset{
			DisplayName: displayName,
			FamilyName:  familyName,
			FileName:    safeName,
			FilePath:    storedPath,
			MimeType:    mimeType,
			FileSize:    int64(len(content)),
			Checksum:    hash,
			Weight:      weight,
			Style:       style,
			Enabled:     true,
			CreatedBy:   &userID,
		}
		if err := fontRepo.Create(font); err != nil {
			_ = os.Remove(storedPath)
			writeFontError(c, "create", err)
			return
		}

		if c.PostForm("make_current") == "true" {
			if err := fontRepo.SetSelectedID(&font.ID); err != nil {
				writeFontError(c, "selection_update", err)
				return
			}
		}

		attachFontURL(font)
		c.JSON(http.StatusCreated, font)
	}
}

func UpdateFontHandler(fontRepo *repository.FontRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 {
			writePublicError(c, http.StatusBadRequest, "font_id_invalid", "invalid font id", false)
			return
		}
		var req struct {
			DisplayName *string `json:"display_name"`
			FamilyName  *string `json:"family_name"`
			Weight      *int    `json:"weight"`
			Style       *string `json:"style"`
			Enabled     *bool   `json:"enabled"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}
		if _, err := fontRepo.Get(id); err != nil {
			writeFontError(c, "load", err)
			return
		}
		var patch repository.FontPatch
		if req.DisplayName != nil {
			value := strings.TrimSpace(*req.DisplayName)
			patch.DisplayName = &value
		}
		if req.FamilyName != nil {
			value := strings.TrimSpace(*req.FamilyName)
			patch.FamilyName = &value
		}
		if req.Weight != nil {
			value := normalizeFontWeight(*req.Weight)
			patch.Weight = &value
		}
		if req.Style != nil {
			value := parseFontStyle(*req.Style)
			patch.Style = &value
		}
		patch.Enabled = req.Enabled
		font, selection, err := fontRepo.PatchContext(c.Request.Context(), id, patch)
		if err != nil {
			writeFontError(c, "update", err)
			return
		}
		attachFontURL(font)
		c.JSON(http.StatusOK, gin.H{"font": font, "selected_font_ids": selection})
	}
}

func SelectFontHandler(fontRepo *repository.FontRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			FontID *int64 `json:"font_id"`
			Slot   string `json:"slot"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}
		if req.FontID != nil && *req.FontID <= 0 {
			writePublicError(c, http.StatusBadRequest, "font_id_invalid", "invalid font id", false)
			return
		}
		if req.Slot == "" {
			if err := fontRepo.SetSelectedID(req.FontID); err != nil {
				writeFontError(c, "selection_update", err)
				return
			}
			c.JSON(http.StatusOK, gin.H{"selected_font_id": req.FontID})
			return
		}
		var slot repository.ChatFontSlot
		switch req.Slot {
		case "chinese":
			slot = repository.ChatFontSlotChinese
		case "latin":
			slot = repository.ChatFontSlotLatin
		case "code":
			slot = repository.ChatFontSlotCode
		default:
			writePublicError(c, http.StatusBadRequest, "font_slot_invalid", "invalid font slot", false)
			return
		}
		selection, err := fontRepo.SetSelectedSlot(slot, req.FontID)
		if err != nil {
			writeFontError(c, "selection_update", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"selected_font_id": selection.Chinese, "selected_font_ids": selection})
	}
}

func DeleteFontHandler(fontRepo *repository.FontRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 {
			writePublicError(c, http.StatusBadRequest, "font_id_invalid", "invalid font id", false)
			return
		}
		font, err := fontRepo.Get(id)
		if err != nil {
			writeFontError(c, "load", err)
			return
		}
		selection, err := fontRepo.Delete(id)
		if err != nil {
			writeFontError(c, "delete", err)
			return
		}
		_ = os.Remove(font.FilePath)
		c.JSON(http.StatusOK, gin.H{"message": "font deleted", "selected_font_ids": selection})
	}
}

func DownloadFontFileHandler(fontRepo *repository.FontRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 {
			writePublicError(c, http.StatusBadRequest, "font_id_invalid", "invalid font id", false)
			return
		}
		font, err := fontRepo.Get(id)
		if err != nil {
			writeFontError(c, "load", err)
			return
		}
		if !font.Enabled {
			writePublicError(c, http.StatusNotFound, "font_not_found", "font not found", false)
			return
		}
		file, err := os.Open(font.FilePath)
		if err != nil {
			writeServerError(c, http.StatusInternalServerError, "font_file_open_failed", "font file is unavailable", err)
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			writeServerError(c, http.StatusInternalServerError, "font_file_stat_failed", "font file is unavailable", err)
			return
		}
		c.Header("Content-Type", font.MimeType)
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
		http.ServeContent(c.Writer, c.Request, font.FileName, info.ModTime(), file)
	}
}

func attachFontURL(font *model.FontAsset) {
	if font != nil {
		font.FileURL = fmt.Sprintf("/api/v1/fonts/%d/file", font.ID)
	}
}

func validateFontContent(filename string, content []byte) (string, error) {
	if len(content) < 4 {
		return "", fmt.Errorf("invalid font file")
	}
	ext := strings.ToLower(filepath.Ext(filename))
	magic := string(content[:4])
	switch ext {
	case ".woff2":
		if magic != "wOF2" {
			return "", fmt.Errorf("font content does not match .woff2")
		}
		return "font/woff2", nil
	case ".woff":
		if magic != "wOFF" {
			return "", fmt.Errorf("font content does not match .woff")
		}
		return "font/woff", nil
	case ".otf":
		if magic != "OTTO" {
			return "", fmt.Errorf("font content does not match .otf")
		}
		return "font/otf", nil
	case ".ttf":
		if !(content[0] == 0x00 && content[1] == 0x01 && content[2] == 0x00 && content[3] == 0x00) && magic != "true" && magic != "typ1" {
			return "", fmt.Errorf("font content does not match .ttf")
		}
		return "font/ttf", nil
	default:
		return "", fmt.Errorf("font type not allowed")
	}
}

func parseFontWeight(raw string) int {
	if raw == "" {
		return 400
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 400
	}
	return normalizeFontWeight(n)
}

func normalizeFontWeight(weight int) int {
	if weight < 100 {
		return 100
	}
	if weight > 900 {
		return 900
	}
	return ((weight + 50) / 100) * 100
}

func parseFontStyle(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "italic", "oblique":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return "normal"
	}
}
