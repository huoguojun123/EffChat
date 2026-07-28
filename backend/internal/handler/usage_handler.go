package handler

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/effchat/internal/usage"
)

func AdminUsageHandler(usageService *usage.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		start, end, custom, err := parseUsageWindow(c.Query("range"), c.Query("start_at"), c.Query("end_at"), time.Now())
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "invalid_usage_range"})
			return
		}
		var summary *usage.Summary
		if custom {
			summary, err = usageService.SummaryBetween(c.Request.Context(), start, end)
		} else {
			summary, err = usageService.Summary(c.Request.Context(), c.Query("range"))
		}
		if err != nil {
			writeServerError(c, http.StatusInternalServerError, "usage_summary_failed", "failed to load usage", err)
			return
		}
		c.JSON(http.StatusOK, summary)
	}
}

func parseUsageWindow(rangeValue, startValue, endValue string, now time.Time) (time.Time, time.Time, bool, error) {
	rangeValue = strings.TrimSpace(rangeValue)
	startValue = strings.TrimSpace(startValue)
	endValue = strings.TrimSpace(endValue)
	if startValue == "" && endValue == "" {
		return time.Time{}, time.Time{}, false, nil
	}
	if rangeValue != "" {
		return time.Time{}, time.Time{}, false, fmt.Errorf("range cannot be combined with start_at and end_at")
	}
	if startValue == "" || endValue == "" {
		return time.Time{}, time.Time{}, false, fmt.Errorf("start_at and end_at are both required")
	}
	start, err := time.Parse(time.RFC3339, startValue)
	if err != nil {
		return time.Time{}, time.Time{}, false, fmt.Errorf("start_at must be RFC3339")
	}
	end, err := time.Parse(time.RFC3339, endValue)
	if err != nil {
		return time.Time{}, time.Time{}, false, fmt.Errorf("end_at must be RFC3339")
	}
	if !start.Before(end) {
		return time.Time{}, time.Time{}, false, fmt.Errorf("start_at must be before end_at")
	}
	if end.Sub(start) > 90*24*time.Hour {
		return time.Time{}, time.Time{}, false, fmt.Errorf("usage range cannot exceed 90 days")
	}
	if start.After(now) {
		return time.Time{}, time.Time{}, false, fmt.Errorf("usage range cannot start in the future")
	}
	localNow := now.In(end.Location())
	tomorrow := time.Date(localNow.Year(), localNow.Month(), localNow.Day()+1, 0, 0, 0, 0, end.Location())
	if end.After(tomorrow) {
		return time.Time{}, time.Time{}, false, fmt.Errorf("usage range cannot include future dates")
	}
	return start, end, true, nil
}
