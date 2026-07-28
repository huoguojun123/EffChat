package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRuntimeConfigSecretsAreNotSerialized(t *testing.T) {
	payload, err := json.Marshal(AIChannel{
		Key:       "openai",
		APIKey:    "sk-secret-value",
		APIKeySet: true,
	})
	if err != nil {
		t.Fatalf("marshal channel: %v", err)
	}
	body := string(payload)
	if strings.Contains(body, "sk-secret-value") || strings.Contains(body, "api_key\"") {
		t.Fatalf("serialized channel leaked api key: %s", body)
	}
	if !strings.Contains(body, "api_key_set") || strings.Contains(body, "api_key_hint") || strings.Contains(body, "...alue") {
		t.Fatalf("serialized channel should only include key status metadata: %s", body)
	}

	payload, err = json.Marshal(ExternalService{
		Key:       "tavily_search",
		APIKey:    "tvly-secret-value",
		APIKeySet: true,
	})
	if err != nil {
		t.Fatalf("marshal external service: %v", err)
	}
	body = string(payload)
	if strings.Contains(body, "tvly-secret-value") || strings.Contains(body, "api_key\"") {
		t.Fatalf("serialized service leaked api key: %s", body)
	}
	if strings.Contains(body, "api_key_hint") || strings.Contains(body, "...alue") {
		t.Fatalf("serialized service leaked key hint: %s", body)
	}
}
