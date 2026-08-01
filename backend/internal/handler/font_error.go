package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/repository"
)

func writeFontError(c *gin.Context, operation string, err error) {
	if errors.Is(err, repository.ErrNotFound) {
		writePublicError(c, http.StatusNotFound, "font_not_found", "font not found", false)
		return
	}
	message := "failed to " + strings.ReplaceAll(operation, "_", " ") + " font"
	switch operation {
	case "list":
		message = "failed to list fonts"
	case "load":
		message = "failed to load font"
	case "selection_load":
		message = "failed to load font selection"
	case "selection_update":
		message = "failed to update font selection"
	}
	writeServerError(c, http.StatusInternalServerError, "font_"+operation+"_failed", message, err)
}
