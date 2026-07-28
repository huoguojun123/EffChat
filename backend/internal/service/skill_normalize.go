package service

import (
	"regexp"
	"strings"
)

var skillIDClean = regexp.MustCompile(`[^a-z0-9_-]+`)

func normalizeSkillID(input string) string {
	input = strings.ToLower(strings.TrimSpace(input))
	input = strings.ReplaceAll(input, " ", "-")
	input = skillIDClean.ReplaceAllString(input, "-")
	input = strings.Trim(input, "-_")
	if len(input) > 80 {
		input = input[:80]
	}
	return input
}
