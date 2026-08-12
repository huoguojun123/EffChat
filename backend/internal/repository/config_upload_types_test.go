package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// file_upload_allowed_types 的 Admin 默认值必须与权威常量 DefaultUploadAllowedTypes
// 逐项一致，杜绝多处字面量漂移（曾导致全新库 seed 窄表、前端放行的 docx/xlsx/json 被拒）。
func TestUploadAllowedTypesDefaultMatchesConstant(t *testing.T) {
	meta, ok := AdminEditableConfig["file_upload_allowed_types"]
	if !ok {
		t.Fatal("file_upload_allowed_types missing from AdminEditableConfig")
	}
	var got []string
	if err := json.Unmarshal(meta.Default, &got); err != nil {
		t.Fatalf("default is not a json string array: %v", err)
	}
	if len(got) != len(DefaultUploadAllowedTypes) {
		t.Fatalf("default len=%d, want %d (%v)", len(got), len(DefaultUploadAllowedTypes), DefaultUploadAllowedTypes)
	}
	for i, want := range DefaultUploadAllowedTypes {
		if got[i] != want {
			t.Errorf("default[%d]=%q, want %q", i, got[i], want)
		}
	}
}

// 全新库回归：默认白名单必须覆盖前端 accept 与 extractor 真实支持的类型。
func TestUploadAllowedTypesCoversRealSupport(t *testing.T) {
	must := []string{
		"image/png",
		"image/jpeg",
		"image/gif",
		"image/webp",
		"application/json",
		"application/xml",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	}
	set := make(map[string]bool, len(DefaultUploadAllowedTypes))
	for _, t := range DefaultUploadAllowedTypes {
		set[t] = true
	}
	for _, m := range must {
		if !set[m] {
			t.Errorf("DefaultUploadAllowedTypes missing %q", m)
		}
	}
}

func TestValidateUploadAllowedTypesRejectsEmptyPolicy(t *testing.T) {
	for _, value := range []json.RawMessage{
		json.RawMessage(`[]`),
		json.RawMessage(`["text/plain", ""]`),
		json.RawMessage(`["text/plain", "   "]`),
	} {
		if _, err := validateAdminEditableValue("file_upload_allowed_types", value); err == nil {
			t.Fatalf("expected upload allowlist %s to be rejected", value)
		}
	}
	if _, err := validateAdminEditableValue("file_upload_allowed_types", json.RawMessage(`["text/plain"]`)); err != nil {
		t.Fatalf("valid upload allowlist rejected: %v", err)
	}
}

func TestUploadSizeAdminPolicyHonorsDeploymentCeiling(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repo := NewConfigRepository(db)
	original, originalErr := repo.Get("file_upload_max_size_mb")
	if originalErr != nil && !errors.Is(originalErr, ErrNotFound) {
		t.Fatalf("read original upload limit: %v", originalErr)
	}
	t.Cleanup(func() {
		if original != nil {
			_ = repo.Update("file_upload_max_size_mb", original.Value)
		} else {
			_, _ = db.Exec("DELETE FROM system_config WHERE key = 'file_upload_max_size_mb'")
		}
	})
	repo.SetUploadMaxSizeMB(25)
	if err := repo.UpdateAdminEditable("file_upload_max_size_mb", json.RawMessage(`25`)); err != nil {
		t.Fatalf("save reachable upload limit: %v", err)
	}
	if err := repo.UpdateAdminEditable("file_upload_max_size_mb", json.RawMessage(`26`)); !errors.Is(err, ErrConfigInvalid) || !strings.Contains(err.Error(), "deployed 25MB") {
		t.Fatalf("save unreachable upload limit error = %v", err)
	}

	if _, err := db.Exec(`UPDATE system_config SET value = '50' WHERE key = 'file_upload_max_size_mb'`); err != nil {
		t.Fatalf("seed legacy oversized value: %v", err)
	}
	items, err := repo.ListAdminEditable()
	if err != nil {
		t.Fatalf("list admin config: %v", err)
	}
	for _, item := range items {
		if item.Key == "file_upload_max_size_mb" {
			if string(item.Value) != "25" {
				t.Fatalf("effective legacy upload value = %s, want 25", item.Value)
			}
			return
		}
	}
	t.Fatal("file_upload_max_size_mb missing from admin config")
}

func TestListAdminEditableIncludesSystemPromptDefault(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repo := NewConfigRepository(db)
	items, err := repo.ListAdminEditable()
	if err != nil {
		t.Fatalf("ListAdminEditable failed: %v", err)
	}

	for _, item := range items {
		if item.Key != "system_prompt_template" {
			continue
		}
		var got string
		if err := json.Unmarshal(item.Default, &got); err != nil {
			t.Fatalf("system_prompt_template default is not a JSON string: %v", err)
		}
		if got != DefaultSystemPromptTemplate {
			t.Fatalf("system_prompt_template default drifted")
		}
		return
	}
	t.Fatal("system_prompt_template missing from ListAdminEditable")
}

func TestValidateSystemPromptTemplateRejectsInvalidOrOversizedTemplates(t *testing.T) {
	for _, value := range []string{"", "{{ .Missing }}", strings.Repeat("x", maxSystemPromptTemplateBytes+1)} {
		if err := ValidateSystemPromptTemplate(value); err == nil {
			t.Fatalf("expected template %q to be rejected", value[:min(len(value), 32)])
		}
	}
	if err := ValidateSystemPromptTemplate("You are {{system_name}} in {{timezone}}."); err != nil {
		t.Fatalf("expected short-token template to be valid: %v", err)
	}
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func TestUpdateAdminEditableBatchIsAtomic(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repo := NewConfigRepository(db)
	originalName, nameErr := repo.Get("system_name")
	originalTrigger, triggerErr := repo.Get("title_generation_trigger")
	if nameErr != nil && !errors.Is(nameErr, ErrNotFound) {
		t.Fatalf("get original system_name: %v", nameErr)
	}
	if triggerErr != nil && !errors.Is(triggerErr, ErrNotFound) {
		t.Fatalf("get original title_generation_trigger: %v", triggerErr)
	}
	t.Cleanup(func() {
		if originalName != nil {
			_ = repo.Update("system_name", originalName.Value)
		} else {
			_, _ = db.Exec("DELETE FROM system_config WHERE key = 'system_name'")
		}
		if originalTrigger != nil {
			_ = repo.Update("title_generation_trigger", originalTrigger.Value)
		} else {
			_, _ = db.Exec("DELETE FROM system_config WHERE key = 'title_generation_trigger'")
		}
	})
	if err := repo.Update("system_name", json.RawMessage(`"Original Mock Chat"`)); err != nil {
		t.Fatalf("seed system_name: %v", err)
	}
	if err := repo.Update("title_generation_trigger", json.RawMessage(`2`)); err != nil {
		t.Fatalf("seed title_generation_trigger: %v", err)
	}

	err := repo.UpdateAdminEditableBatch(map[string]json.RawMessage{
		"system_name":      json.RawMessage(`"Must Not Persist"`),
		"memory_max_chars": json.RawMessage(`9999`),
	})
	if !errors.Is(err, ErrConfigInvalid) || !strings.Contains(err.Error(), "not an allowed option") {
		t.Fatalf("invalid batch error = %v", err)
	}
	gotName, err := repo.Get("system_name")
	if err != nil {
		t.Fatalf("get system_name after rejected batch: %v", err)
	}
	if string(gotName.Value) != `"Original Mock Chat"` {
		t.Fatalf("rejected batch changed system_name: got %s", gotName.Value)
	}

	suffix := time.Now().UnixNano()
	functionName := fmt.Sprintf("fail_config_batch_%d", suffix)
	triggerName := fmt.Sprintf("fail_config_batch_trigger_%d", suffix)
	if _, err := db.Exec(fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger AS $$
		BEGIN
			IF NEW.key = 'title_generation_trigger' AND NEW.value = '13'::jsonb THEN
				RAISE EXCEPTION 'forced config batch failure';
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql;
		CREATE TRIGGER %s BEFORE INSERT OR UPDATE ON system_config
		FOR EACH ROW EXECUTE FUNCTION %s();
	`, functionName, triggerName, functionName)); err != nil {
		t.Fatalf("install rollback trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON system_config", triggerName))
		_, _ = db.Exec(fmt.Sprintf("DROP FUNCTION IF EXISTS %s()", functionName))
	})
	err = repo.UpdateAdminEditableBatch(map[string]json.RawMessage{
		"system_name":              json.RawMessage(`"Must Roll Back After First SQL"`),
		"title_generation_trigger": json.RawMessage(`13`),
	})
	if err == nil || !strings.Contains(err.Error(), "forced config batch failure") {
		t.Fatalf("mid-transaction batch error = %v", err)
	}
	if got := repo.GetString("system_name", ""); got != "Original Mock Chat" {
		t.Fatalf("transaction failure retained first update: %q", got)
	}
	if got := repo.GetInt("title_generation_trigger", 0); got != 2 {
		t.Fatalf("transaction failure changed later config: %d", got)
	}

	err = repo.UpdateAdminEditableBatch(map[string]json.RawMessage{
		"system_name":              json.RawMessage(`"Mock Batch Chat"`),
		"title_generation_trigger": json.RawMessage(`3`),
	})
	if err != nil {
		t.Fatalf("valid batch update: %v", err)
	}
	if got := repo.GetString("system_name", ""); got != "Mock Batch Chat" {
		t.Fatalf("system_name = %q, want Mock Batch Chat", got)
	}
	if got := repo.GetInt("title_generation_trigger", 0); got != 3 {
		t.Fatalf("title_generation_trigger = %d, want 3", got)
	}
}

func TestUpdateAdminEditableBatchHonorsContextCancellation(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	repo := NewConfigRepository(db)
	original, err := repo.Get("system_name")
	if err != nil {
		t.Fatalf("read system_name: %v", err)
	}

	blocker, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin blocker transaction: %v", err)
	}
	defer blocker.Rollback()
	var lockedKey string
	if err := blocker.QueryRowContext(context.Background(), `SELECT key FROM system_config WHERE key = 'system_name' FOR UPDATE`).Scan(&lockedKey); err != nil {
		t.Fatalf("lock system_name row: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err = repo.UpdateAdminEditableBatchContext(ctx, map[string]json.RawMessage{"system_name": json.RawMessage(`"Canceled Config"`)})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("config update error = %v, want context deadline", err)
	}
	stored, err := repo.Get("system_name")
	if err != nil {
		t.Fatalf("read canceled system_name: %v", err)
	}
	if string(stored.Value) != string(original.Value) {
		t.Fatalf("canceled config update committed %s, want %s", stored.Value, original.Value)
	}
}
