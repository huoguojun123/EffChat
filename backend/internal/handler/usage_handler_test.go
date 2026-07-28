package handler

import (
	"testing"
	"time"
)

func TestParseUsageWindow(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	tests := []struct {
		name       string
		rangeValue string
		start      string
		end        string
		wantCustom bool
		wantError  bool
	}{
		{name: "preset", rangeValue: "7d"},
		{name: "custom", start: "2026-07-01T00:00:00+08:00", end: "2026-07-27T00:00:00+08:00", wantCustom: true},
		{name: "inclusive current date boundary", start: "2026-07-27T00:00:00+08:00", end: "2026-07-28T00:00:00+08:00", wantCustom: true},
		{name: "mixed modes", rangeValue: "30d", start: "2026-07-01T00:00:00+08:00", end: "2026-07-02T00:00:00+08:00", wantError: true},
		{name: "missing end", start: "2026-07-01T00:00:00+08:00", wantError: true},
		{name: "invalid order", start: "2026-07-02T00:00:00+08:00", end: "2026-07-01T00:00:00+08:00", wantError: true},
		{name: "over 90 days", start: "2026-04-01T00:00:00+08:00", end: "2026-07-01T00:00:00+08:00", wantError: true},
		{name: "future start", start: "2026-07-28T00:00:00+08:00", end: "2026-07-29T00:00:00+08:00", wantError: true},
		{name: "future date", start: "2026-07-27T00:00:00+08:00", end: "2026-07-29T00:00:00+08:00", wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, custom, err := parseUsageWindow(tt.rangeValue, tt.start, tt.end, now)
			if (err != nil) != tt.wantError {
				t.Fatalf("error = %v, wantError %v", err, tt.wantError)
			}
			if custom != tt.wantCustom {
				t.Fatalf("custom = %v, want %v", custom, tt.wantCustom)
			}
		})
	}
}
