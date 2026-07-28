package service

import (
	"strings"
	"testing"
)

func TestValidateSessionSystemPromptBoundsInput(t *testing.T) {
	tooLong := strings.Repeat("x", maxSessionSystemPromptBytes+1)
	if err := validateSessionSystemPrompt(&tooLong); err == nil {
		t.Fatal("expected oversized session prompt to be rejected")
	}
	short := "Keep answers concise."
	if err := validateSessionSystemPrompt(&short); err != nil {
		t.Fatalf("short prompt should be accepted: %v", err)
	}
}
