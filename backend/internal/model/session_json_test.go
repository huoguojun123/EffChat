package model

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestSessionJSONUsesStructuredMetadataObject(t *testing.T) {
	session := Session{ID: 7, Title: "fixture", Metadata: []byte(`{"skills_enabled":["brainstorming"],"file_count":0}`)}
	body, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("marshal session: %v", err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode session payload: %v", err)
	}
	var metadata map[string]interface{}
	if err := json.Unmarshal(payload["metadata"], &metadata); err != nil {
		t.Fatalf("metadata should be an object: %v; body=%s", err, body)
	}
	if got := metadata["skills_enabled"].([]interface{})[0]; got != "brainstorming" {
		t.Fatalf("skills_enabled = %#v", got)
	}
}

func TestSessionJSONReadsLegacyBase64Metadata(t *testing.T) {
	legacy := base64.StdEncoding.EncodeToString([]byte(`{"skills_enabled":["brainstorming"]}`))
	var session Session
	if err := json.Unmarshal([]byte(`{"id":7,"metadata":"`+legacy+`"}`), &session); err != nil {
		t.Fatalf("unmarshal legacy session: %v", err)
	}
	if string(session.Metadata) != `{"skills_enabled":["brainstorming"]}` {
		t.Fatalf("legacy metadata = %s", session.Metadata)
	}
	body, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("marshal normalized session: %v", err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode normalized session: %v", err)
	}
	if len(payload["metadata"]) == 0 || payload["metadata"][0] != '{' {
		t.Fatalf("normalized metadata is not an object: %s", body)
	}
}

func TestSessionJSONDefaultsMissingMetadataToObject(t *testing.T) {
	var session Session
	if err := json.Unmarshal([]byte(`{"id":7}`), &session); err != nil {
		t.Fatalf("unmarshal session without metadata: %v", err)
	}
	if string(session.Metadata) != "{}" {
		t.Fatalf("missing metadata = %s", session.Metadata)
	}
}
