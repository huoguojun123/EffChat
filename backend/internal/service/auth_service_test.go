package service

import (
	"encoding/json"
	"testing"

	"github.com/huoguojun123/EffChat/internal/model"
)

func TestGenerateTokenIncludesAuthVersion(t *testing.T) {
	svc := NewAuthService(nil, "test-secret")
	token, err := svc.generateToken(&model.User{ID: 42, Username: "alice", Role: "user", AuthVersion: 3})
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	claims, err := svc.ValidateToken(token)
	if err != nil {
		t.Fatalf("validate token: %v", err)
	}
	version, ok := (*claims)["auth_version"].(json.Number)
	if !ok || version.String() != "3" {
		t.Fatalf("auth_version = %#v, want json.Number(3)", (*claims)["auth_version"])
	}
}
