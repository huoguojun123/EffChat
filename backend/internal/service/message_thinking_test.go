package service

import "testing"

func TestNormalizeMessageThinkingEffort(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{in: "", want: ""},
		{in: "auto", want: ""},
		{in: "HIGH", want: "high"},
		{in: "max", want: "max"},
		{in: "turbo", want: ""},
	}
	for _, tc := range cases {
		if got := normalizeMessageThinkingEffort(tc.in); got != tc.want {
			t.Fatalf("normalizeMessageThinkingEffort(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
