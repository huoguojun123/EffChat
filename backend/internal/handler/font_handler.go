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
	"github.com/huoguojun123/effchat/internal/filepolicy"
	"github.com/huoguojun123/effchat/internal/middleware"
	"github.com/huoguojun123/effchat/internal/model"
	"github.com/huoguojun123/effchat/internal/repository"
)

const fontUploadDir = filepolicy.FontRoot
const maxFontUploadBytes = int64(30 << 20)

func ListAdminFontsHandler(fontRepo *repository.FontRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		fonts, err := fontRepo.List()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list fonts"})
			return
		}
		for _, font := range fonts {
			attachFontURL(font)
		}
		selectedID, err := fontRepo.GetSelectedID()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read selected font"})
			return
		}
		selection, err := fontRepo.GetSelectedIDs()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read selected fonts"})
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
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "font file too large"})
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": "font file is required"})
			return
		}
		defer file.Close()

		safeName := sanitizeUploadFilename(header.Filename)
		if header.Size <= 0 || header.Size > maxFontUploadBytes {
			c.JSON(http.StatusBadRequest, gin.H{"error": "font file too large"})
			return
		}

		content, err := io.ReadAll(io.LimitReader(file, maxFontUploadBytes+1))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read font file"})
			return
		}
		if int64(len(content)) > maxFontUploadBytes {
			c.JSON(http.StatusBadRequest, gin.H{"error": "font file too large"})
			return
		}

		mimeType, err := validateFontContent(safeName, content)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		hash := fmt.Sprintf("%x", sha256.Sum256(content))
		if err := os.MkdirAll(fontUploadDir, 0755); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create font directory"})
			return
		}

		storedName := fmt.Sprintf("%d_%s_%s", time.Now().UnixNano(), hash[:12], safeName)
		storedPath := filepath.Join(fontUploadDir, storedName)
		if err := os.WriteFile(storedPath, content, 0644); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save font file"})
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save font metadata"})
			return
		}

		if c.PostForm("make_current") == "true" {
			if err := fontRepo.SetSelectedID(&font.ID); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "font uploaded but failed to set current font"})
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
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid font id"})
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
		font, err := fontRepo.Get(id)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "font not found"})
			return
		}
		if req.DisplayName != nil {
			font.DisplayName = strings.TrimSpace(*req.DisplayName)
		}
		if req.FamilyName != nil {
			font.FamilyName = strings.TrimSpace(*req.FamilyName)
		}
		if req.Weight != nil {
			font.Weight = normalizeFontWeight(*req.Weight)
		}
		if req.Style != nil {
			font.Style = parseFontStyle(*req.Style)
		}
		if req.Enabled != nil {
			font.Enabled = *req.Enabled
		}
		if font.DisplayName == "" || font.FamilyName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "display_name and family_name are required"})
			return
		}
		if err := fontRepo.Update(font); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if !font.Enabled {
			selection, err := fontRepo.GetSelectedIDs()
			if err == nil {
				changed := false
				if selection.Chinese != nil && *selection.Chinese == font.ID {
					selection.Chinese = nil
					changed = true
				}
				if selection.Latin != nil && *selection.Latin == font.ID {
					selection.Latin = nil
					changed = true
				}
				if selection.Code != nil && *selection.Code == font.ID {
					selection.Code = nil
					changed = true
				}
				if changed {
					_ = fontRepo.SetSelectedIDs(selection)
				}
			}
		}
		updated, _ := fontRepo.Get(id)
		if updated == nil {
			updated = font
		}
		attachFontURL(updated)
		c.JSON(http.StatusOK, updated)
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
		if req.FontID != nil {
			font, err := fontRepo.Get(*req.FontID)
			if err != nil || !font.Enabled {
				c.JSON(http.StatusBadRequest, gin.H{"error": "font is not available"})
				return
			}
		}
		if req.Slot == "" {
			if err := fontRepo.SetSelectedID(req.FontID); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"selected_font_id": req.FontID})
			return
		}
		selection, err := fontRepo.GetSelectedIDs()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read selected fonts"})
			return
		}
		switch req.Slot {
		case "chinese":
			selection.Chinese = req.FontID
		case "latin":
			selection.Latin = req.FontID
		case "code":
			selection.Code = req.FontID
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid font slot"})
			return
		}
		if err := fontRepo.SetSelectedIDs(selection); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"selected_font_id": selection.Chinese, "selected_font_ids": selection})
	}
}

func DeleteFontHandler(fontRepo *repository.FontRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid font id"})
			return
		}
		font, err := fontRepo.Get(id)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "font not found"})
			return
		}
		if err := fontRepo.Delete(id); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		_ = os.Remove(font.FilePath)
		c.JSON(http.StatusOK, gin.H{"message": "font deleted"})
	}
}

func DownloadFontFileHandler(fontRepo *repository.FontRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid font id"})
			return
		}
		font, err := fontRepo.Get(id)
		if err != nil || !font.Enabled {
			c.JSON(http.StatusNotFound, gin.H{"error": "font not found"})
			return
		}
		c.Header("Content-Type", font.MimeType)
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
		c.File(font.FilePath)
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
