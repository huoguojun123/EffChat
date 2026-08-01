package service

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
	_ "github.com/lib/pq"
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

func TestResolveAuthenticatedUserPreservesRepositoryCause(t *testing.T) {
	db, err := sql.Open("postgres", "postgres://fixture:secret@db.example/effchat?sslmode=disable")
	if err != nil {
		t.Fatalf("open database handle: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close database handle: %v", err)
	}
	svc := NewAuthService(repository.NewUserRepository(db), "test-secret")

	_, err = svc.ResolveAuthenticatedUser(42, 1)

	if !errors.Is(err, ErrInternal) {
		t.Fatalf("error = %v, want ErrInternal", err)
	}
	if !strings.Contains(err.Error(), "database is closed") {
		t.Fatalf("error lost repository cause: %v", err)
	}
}
