package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/huoguojun123/EffChat/internal/model"
)

func TestGovernanceRepositoryAppendAndRollbackLink(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	userID := createRepositoryTestUser(t, db, "governance_event")
	defer db.Exec("DELETE FROM users WHERE id = $1", userID)

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	original := &model.GovernanceEvent{
		ResourceType: "tool", ResourceKey: "memory", Action: "update",
		ActorType: "admin", ActorUserID: &userID, Reason: "fixture toggle",
		BeforeState: json.RawMessage(`{"enabled":true}`), AfterState: json.RawMessage(`{"enabled":false}`),
	}
	if err := InsertGovernanceEventTx(context.Background(), tx, original); err != nil {
		t.Fatalf("insert original event: %v", err)
	}
	rollback := &model.GovernanceEvent{
		ResourceType: "tool", ResourceKey: "memory", Action: "rollback",
		ActorType: "admin", ActorUserID: &userID, Reason: "fixture rollback",
		BeforeState: json.RawMessage(`{"enabled":false}`), AfterState: json.RawMessage(`{"enabled":true}`),
		RollbackOfEventID: &original.ID,
	}
	if err := InsertGovernanceEventTx(context.Background(), tx, rollback); err != nil {
		t.Fatalf("insert rollback event: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	events, err := NewGovernanceRepository(db).List(context.Background(), "tool", "memory", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Action != "rollback" || events[0].RollbackOfEventID == nil || *events[0].RollbackOfEventID != original.ID {
		t.Fatalf("events = %+v", events)
	}
}

func TestGovernanceEventFailureRollsBackCatalogMutation(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	if _, err := db.Exec(`
		INSERT INTO tool_configs (tool_key, display_name, enabled, timeout_seconds, sort_order)
		VALUES ('memory', 'Fixture memory', true, 20, 10)
	`); err != nil {
		t.Fatal(err)
	}
	var before bool
	if err := db.QueryRow(`SELECT enabled FROM tool_configs WHERE tool_key = 'memory'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE tool_configs SET enabled = NOT enabled WHERE tool_key = 'memory'`); err != nil {
		t.Fatal(err)
	}
	invalid := &model.GovernanceEvent{
		ResourceType: "tool", ResourceKey: "memory", Action: "update",
		ActorType: "admin", Reason: "missing actor",
		BeforeState: json.RawMessage(`{"enabled":true}`), AfterState: json.RawMessage(`{"enabled":false}`),
	}
	if err := InsertGovernanceEventTx(context.Background(), tx, invalid); err == nil {
		t.Fatal("expected actor constraint failure")
	}
	_ = tx.Rollback()
	var after bool
	if err := db.QueryRow(`SELECT enabled FROM tool_configs WHERE tool_key = 'memory'`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("tool mutation survived failed audit insert: before=%v after=%v", before, after)
	}
}

func TestSkillImportRecordsRetainPackageVersions(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	userID := createRepositoryTestUser(t, db, "skill_versions")
	skillID := fmt.Sprintf("fixture-version-%d", time.Now().UnixNano())
	defer db.Exec("DELETE FROM skills WHERE id = $1", skillID)
	defer db.Exec("DELETE FROM users WHERE id = $1", userID)
	repo := NewSkillRepository(db)

	writeVersion := func(checksum, path string) *model.SkillImportRecord {
		skill := &model.Skill{
			ID: skillID, Name: "Fixture", Description: "fixture", SourceType: "manual",
			Checksum: checksum, PackageChecksum: checksum, EntryPath: "SKILL.md",
			Enabled: true, CreatedBy: &userID,
		}
		files := []model.SkillFile{{RelativePath: "SKILL.md", StoragePath: path, Kind: "entry", Size: 7, Checksum: checksum}}
		record := &model.SkillImportRecord{Action: "update", SourceType: "manual", PackageChecksum: checksum, CreatedBy: &userID}
		if err := repo.UpsertPackageWithRecord(skill, files, record); err != nil {
			t.Fatalf("write version %s: %v", checksum, err)
		}
		return record
	}
	first := writeVersion("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "/fixture/v1/SKILL.md")
	second := writeVersion("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "/fixture/v2/SKILL.md")

	firstFiles, err := repo.ListImportRecordFiles(first.ID)
	if err != nil || len(firstFiles) != 1 || firstFiles[0].StoragePath != "/fixture/v1/SKILL.md" {
		t.Fatalf("first retained files = %+v err=%v", firstFiles, err)
	}
	paths, err := repo.RetainedFilePaths()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"/fixture/v1/SKILL.md": false, "/fixture/v2/SKILL.md": false}
	for _, path := range paths {
		if _, ok := want[path]; ok {
			want[path] = true
		}
	}
	if !want["/fixture/v1/SKILL.md"] || !want["/fixture/v2/SKILL.md"] || second.ID == first.ID {
		t.Fatalf("retained paths=%v records=%d/%d", want, first.ID, second.ID)
	}
}

func TestToolConfigGovernedSaveAndRollback(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	userID := createRepositoryTestUser(t, db, "tool_rollback")
	defer db.Exec("DELETE FROM users WHERE id = $1", userID)
	if _, err := db.Exec(`
		INSERT INTO tool_configs (tool_key, display_name, enabled, timeout_seconds, sort_order)
		VALUES ('memory', 'Fixture memory', true, 20, 10)
	`); err != nil {
		t.Fatal(err)
	}
	repo := NewToolConfigRepository(db)
	saved, event, err := repo.SaveGoverned(context.Background(), &model.ToolConfig{
		Key: "memory", DisplayName: "Fixture memory", Enabled: false, TimeoutSeconds: 25, SortOrder: 11,
	}, userID, "disable fixture")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Enabled || event.Action != "update" || event.ID == 0 {
		t.Fatalf("saved=%+v event=%+v", saved, event)
	}
	restored, reverse, err := repo.RollbackGoverned(context.Background(), event.ID, userID, "restore fixture")
	if err != nil {
		t.Fatal(err)
	}
	if restored == nil || !restored.Enabled || restored.TimeoutSeconds != 20 || restored.SortOrder != 10 {
		t.Fatalf("restored=%+v", restored)
	}
	if reverse.Action != "rollback" || reverse.RollbackOfEventID == nil || *reverse.RollbackOfEventID != event.ID {
		t.Fatalf("reverse=%+v", reverse)
	}
	if _, _, err := repo.RollbackGoverned(context.Background(), event.ID, userID, "duplicate"); !errors.Is(err, ErrGovernanceConflict) {
		t.Fatalf("duplicate rollback error=%v", err)
	}
}

func TestToolConfigRollbackRejectsLaterState(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	userID := createRepositoryTestUser(t, db, "tool_cas")
	defer db.Exec("DELETE FROM users WHERE id = $1", userID)
	if _, err := db.Exec(`
		INSERT INTO tool_configs (tool_key, display_name, enabled, timeout_seconds, sort_order)
		VALUES ('web_search', 'Fixture search', true, 20, 80)
	`); err != nil {
		t.Fatal(err)
	}
	repo := NewToolConfigRepository(db)
	_, first, err := repo.SaveGoverned(context.Background(), &model.ToolConfig{
		Key: "web_search", DisplayName: "Fixture search", Enabled: false, TimeoutSeconds: 20, SortOrder: 80,
	}, userID, "first change")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.SaveGoverned(context.Background(), &model.ToolConfig{
		Key: "web_search", DisplayName: "Fixture search", Enabled: true, TimeoutSeconds: 30, SortOrder: 80,
	}, userID, "later change"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.RollbackGoverned(context.Background(), first.ID, userID, "stale rollback"); !errors.Is(err, ErrGovernanceConflict) {
		t.Fatalf("stale rollback error=%v", err)
	}
}
