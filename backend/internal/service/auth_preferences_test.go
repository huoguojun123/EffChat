package service

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildUserPreferencesMergesCustomValues(t *testing.T) {
	data, err := buildUserPreferences(json.RawMessage(`{"theme":"light","custom_flag":true}`))
	if err != nil {
		t.Fatalf("buildUserPreferences: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal preferences: %v", err)
	}
	if got["theme"] != "light" {
		t.Fatalf("theme = %v, want light", got["theme"])
	}
	if got["language"] != "zh-CN" {
		t.Fatalf("language = %v, want default zh-CN", got["language"])
	}
	if got["custom_flag"] != true {
		t.Fatalf("custom_flag = %v, want true", got["custom_flag"])
	}
}

func TestBuildUserPreferencesRejectsNonObject(t *testing.T) {
	if _, err := buildUserPreferences(json.RawMessage(`["dark"]`)); err == nil {
		t.Fatal("array preferences should be rejected")
	}
}

func TestBuildUserPreferencesRejectsUnboundedValues(t *testing.T) {
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"value":"` + strings.Repeat("x", 513) + `"}`),
		json.RawMessage(`{"items":[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17]}`),
		json.RawMessage(`{"one":1,"two":2,"three":3,"four":4,"five":5,"six":6,"seven":7,"eight":8,"nine":9,"ten":10,"eleven":11,"twelve":12,"thirteen":13,"fourteen":14,"fifteen":15,"sixteen":16,"seventeen":17,"eighteen":18,"nineteen":19,"twenty":20,"twentyone":21,"twentytwo":22,"twentythree":23,"twentyfour":24,"twentyfive":25,"twentysix":26,"twentyseven":27,"twentyeight":28,"twentynine":29,"thirty":30,"thirtyone":31,"thirtytwo":32,"thirtythree":33}`),
	} {
		if _, err := buildUserPreferences(raw); err == nil {
			t.Fatalf("expected preferences %s to be rejected", raw)
		}
	}
}
