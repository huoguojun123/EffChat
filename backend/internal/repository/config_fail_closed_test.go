package repository

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
)

func TestConfigContextGettersFailClosedOnQueryErrors(t *testing.T) {
	db := setupTestDB(t)
	repo := NewConfigRepository(db)
	if err := db.Close(); err != nil {
		t.Fatalf("close test database: %v", err)
	}

	tests := []struct {
		name string
		read func() error
	}{
		{name: "string", read: func() error {
			_, err := repo.GetStringContext(context.Background(), "system_name", "fallback")
			return err
		}},
		{name: "integer", read: func() error {
			_, err := repo.GetIntContext(context.Background(), "file_upload_max_size_mb", 20)
			return err
		}},
		{name: "boolean", read: func() error {
			_, err := repo.GetBoolContext(context.Background(), "attachment_extract_enabled", true)
			return err
		}},
		{name: "string array", read: func() error {
			_, err := repo.GetStringSliceContext(context.Background(), "file_upload_allowed_types", DefaultUploadAllowedTypes)
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.read(); err == nil {
				t.Fatal("expected database failure to be returned")
			}
		})
	}
}

func TestPolicyGettersReuseOnlyLastSuccessfulValues(t *testing.T) {
	db := setupTestDB(t)
	repo := NewConfigRepository(db)
	ctx := context.Background()

	wantSize, degraded, err := repo.GetPolicyIntContext(ctx, "file_upload_max_size_mb", 20)
	if err != nil || degraded {
		t.Fatalf("prime integer policy: value=%d degraded=%v err=%v", wantSize, degraded, err)
	}
	wantEnabled, degraded, err := repo.GetPolicyBoolContext(ctx, "attachment_extract_enabled", true)
	if err != nil || degraded {
		t.Fatalf("prime boolean policy: value=%v degraded=%v err=%v", wantEnabled, degraded, err)
	}
	wantModel, degraded, err := repo.GetPolicyStringContext(ctx, "extract_summary_model", "fallback-model")
	if err != nil || degraded {
		t.Fatalf("prime string policy: value=%q degraded=%v err=%v", wantModel, degraded, err)
	}
	wantTypes, degraded, err := repo.GetPolicyStringSliceContext(ctx, "file_upload_allowed_types", DefaultUploadAllowedTypes)
	if err != nil || degraded {
		t.Fatalf("prime string-array policy: value=%v degraded=%v err=%v", wantTypes, degraded, err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close test database: %v", err)
	}

	if got, degraded, err := repo.GetPolicyIntContext(ctx, "file_upload_max_size_mb", 20); err != nil || !degraded || got != wantSize {
		t.Fatalf("cached integer policy: value=%d degraded=%v err=%v, want %d/true/nil", got, degraded, err, wantSize)
	}
	if got, degraded, err := repo.GetPolicyBoolContext(ctx, "attachment_extract_enabled", true); err != nil || !degraded || got != wantEnabled {
		t.Fatalf("cached boolean policy: value=%v degraded=%v err=%v, want %v/true/nil", got, degraded, err, wantEnabled)
	}
	if got, degraded, err := repo.GetPolicyStringContext(ctx, "extract_summary_model", "fallback-model"); err != nil || !degraded || got != wantModel {
		t.Fatalf("cached string policy: value=%q degraded=%v err=%v, want %q/true/nil", got, degraded, err, wantModel)
	}
	gotTypes, degraded, err := repo.GetPolicyStringSliceContext(ctx, "file_upload_allowed_types", DefaultUploadAllowedTypes)
	if err != nil || !degraded || !slices.Equal(gotTypes, wantTypes) {
		t.Fatalf("cached string-array policy: value=%v degraded=%v err=%v, want %v/true/nil", gotTypes, degraded, err, wantTypes)
	}
	gotTypes[0] = "mutated/by/caller"
	again, _, err := repo.GetPolicyStringSliceContext(ctx, "file_upload_allowed_types", nil)
	if err != nil || len(again) == 0 || again[0] == gotTypes[0] {
		t.Fatalf("cached string array was mutated through caller slice: %v err=%v", again, err)
	}
}

func TestConfigContextGettersRejectMalformedStoredValues(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	repo := NewConfigRepository(db)

	values := map[string]json.RawMessage{
		"system_name":                json.RawMessage(`{}`),
		"file_upload_max_size_mb":    json.RawMessage(`"not-a-number"`),
		"attachment_extract_enabled": json.RawMessage(`"not-a-boolean"`),
		"file_upload_allowed_types":  json.RawMessage(`{}`),
	}
	originals := make(map[string]*ConfigItem, len(values))
	for key, value := range values {
		item, err := repo.Get(key)
		if err == nil {
			originals[key] = item
		} else if errors.Is(err, ErrNotFound) {
			originals[key] = nil
		} else {
			t.Fatalf("read original %s: %v", key, err)
		}
		if _, err := db.Exec(`
			INSERT INTO system_config (key, value, config_type)
			VALUES ($1, $2, 'json')
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value
		`, key, value); err != nil {
			t.Fatalf("seed malformed %s: %v", key, err)
		}
	}
	t.Cleanup(func() {
		for key, item := range originals {
			if item == nil {
				_, _ = db.Exec("DELETE FROM system_config WHERE key = $1", key)
				continue
			}
			_, _ = db.Exec("UPDATE system_config SET value = $1 WHERE key = $2", item.Value, key)
		}
	})

	if _, err := repo.GetStringContext(context.Background(), "system_name", "fallback"); err == nil {
		t.Fatal("malformed string must not use fallback")
	}
	if _, err := repo.GetIntContext(context.Background(), "file_upload_max_size_mb", 20); err == nil {
		t.Fatal("malformed integer must not use fallback")
	}
	if _, err := repo.GetBoolContext(context.Background(), "attachment_extract_enabled", true); err == nil {
		t.Fatal("malformed boolean must not use fallback")
	}
	if _, err := repo.GetStringSliceContext(context.Background(), "file_upload_allowed_types", DefaultUploadAllowedTypes); err == nil {
		t.Fatal("malformed string array must not use fallback")
	}
}
