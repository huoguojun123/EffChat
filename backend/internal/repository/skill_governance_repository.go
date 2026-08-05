package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/huoguojun123/EffChat/internal/model"
)

type SkillGovernanceMutation struct {
	Action      string
	ActorType   string
	ActorUserID int64
	Reason      string
}

// GovernedSkillPackage is one member of an atomic package import. The service
// prepares immutable package files before this repository transaction starts;
// this boundary owns only the active database state and its audit history.
type GovernedSkillPackage struct {
	Skill    *model.Skill
	Files    []model.SkillFile
	Record   *model.SkillImportRecord
	Mutation SkillGovernanceMutation
}

type skillGovernanceState struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Description     string  `json:"description"`
	SourceType      string  `json:"source_type"`
	SourceURL       *string `json:"source_url,omitempty"`
	SourceRef       *string `json:"source_ref,omitempty"`
	SourcePath      *string `json:"source_path,omitempty"`
	Checksum        string  `json:"checksum"`
	PackageChecksum string  `json:"package_checksum"`
	EntryPath       string  `json:"entry_path"`
	MinGroupLevel   int     `json:"min_group_level"`
	Enabled         bool    `json:"enabled"`
	IsBuiltin       bool    `json:"is_builtin"`
	CreatedBy       *int64  `json:"created_by,omitempty"`
	ImportRecordID  int64   `json:"import_record_id"`
	Deleted         bool    `json:"deleted"`
}

type skillFileManifestItem struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Size     int64  `json:"size"`
	Checksum string `json:"checksum"`
}

// UpsertPackageGoverned commits the active package, immutable package version,
// and governance event together. A pre-051 active package is snapshotted first
// so every event created by this code has a locally retained rollback target.
func (r *SkillRepository) UpsertPackageGoverned(ctx context.Context, skill *model.Skill, files []model.SkillFile, record *model.SkillImportRecord, mutation SkillGovernanceMutation) (*model.GovernanceEvent, error) {
	if err := validateSkillGovernanceMutation(mutation); err != nil {
		return nil, err
	}
	if record == nil {
		return nil, fmt.Errorf("governed Skill package requires an import record")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin governed Skill package save: %w", err)
	}
	defer tx.Rollback()

	event, err := upsertPackageGovernedTx(ctx, tx, skill, files, record, mutation)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit governed Skill package save: %w", err)
	}
	skill.Files = files
	return event, nil
}

// UpsertPackagesGoverned makes one administrator import visible atomically.
// Every active package, immutable import record, and governance event commits
// in the same transaction; callers may safely retry after any returned error.
func (r *SkillRepository) UpsertPackagesGoverned(ctx context.Context, packages []GovernedSkillPackage) error {
	if len(packages) == 0 {
		return nil
	}
	for _, item := range packages {
		if item.Skill == nil || item.Record == nil {
			return fmt.Errorf("governed Skill package batch requires skill and import record")
		}
		if err := validateSkillGovernanceMutation(item.Mutation); err != nil {
			return err
		}
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin governed Skill package batch: %w", err)
	}
	defer tx.Rollback()
	for _, item := range packages {
		if _, err := upsertPackageGovernedTx(ctx, tx, item.Skill, item.Files, item.Record, item.Mutation); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit governed Skill package batch: %w", err)
	}
	for _, item := range packages {
		item.Skill.Files = item.Files
	}
	return nil
}

func upsertPackageGovernedTx(ctx context.Context, tx *sql.Tx, skill *model.Skill, files []model.SkillFile, record *model.SkillImportRecord, mutation SkillGovernanceMutation) (*model.GovernanceEvent, error) {
	before, err := skillGovernanceStateForUpdateTx(ctx, tx, skill.ID, mutation.ActorUserID, true)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if errors.Is(err, ErrNotFound) {
		before = nil
	}
	if err := upsertSkillPackageTx(ctx, tx, skill, files, record); err != nil {
		return nil, err
	}
	after := marshalSkillGovernanceState(skill, record.ID)
	event := &model.GovernanceEvent{
		ResourceType: "skill", ResourceKey: skill.ID, Action: mutation.Action,
		ActorType: mutation.ActorType, ActorUserID: &mutation.ActorUserID, Reason: mutation.Reason,
		BeforeState: before, AfterState: after, SkillImportRecordID: &record.ID,
	}
	if err := InsertGovernanceEventTx(ctx, tx, event); err != nil {
		return nil, fmt.Errorf("audit governed Skill package save: %w", err)
	}
	return event, nil
}

// UpdateMetadataGoverned keeps metadata-only mutations tied to the exact
// package version that remained active during the change.
func (r *SkillRepository) UpdateMetadataGoverned(ctx context.Context, skill *model.Skill, mutation SkillGovernanceMutation) (*model.GovernanceEvent, error) {
	if err := validateSkillGovernanceMutation(mutation); err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin governed Skill metadata update: %w", err)
	}
	defer tx.Rollback()
	before, err := skillGovernanceStateForUpdateTx(ctx, tx, skill.ID, mutation.ActorUserID, false)
	if err != nil {
		return nil, err
	}
	var state skillGovernanceState
	if err := json.Unmarshal(before, &state); err != nil {
		return nil, fmt.Errorf("decode governed Skill state: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `
		UPDATE skills
		SET name = $1, description = $2, enabled = $3, min_group_level = $4, updated_at = NOW()
		WHERE id = $5 AND deleted_at IS NULL
		RETURNING created_at, updated_at
	`, skill.Name, skill.Description, skill.Enabled, skill.MinGroupLevel, skill.ID).Scan(&skill.CreatedAt, &skill.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("skill not found: %w", ErrNotFound)
		}
		return nil, fmt.Errorf("update governed Skill metadata: %w", err)
	}
	after := marshalSkillGovernanceState(skill, state.ImportRecordID)
	event := &model.GovernanceEvent{
		ResourceType: "skill", ResourceKey: skill.ID, Action: mutation.Action,
		ActorType: mutation.ActorType, ActorUserID: &mutation.ActorUserID, Reason: mutation.Reason,
		BeforeState: before, AfterState: after, SkillImportRecordID: &state.ImportRecordID,
	}
	if err := InsertGovernanceEventTx(ctx, tx, event); err != nil {
		return nil, fmt.Errorf("audit governed Skill metadata update: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit governed Skill metadata update: %w", err)
	}
	return event, nil
}

// DeleteGoverned records a tombstone rather than removing either the Skill row
// or its retained package version, making deletion auditable and reversible.
func (r *SkillRepository) DeleteGoverned(ctx context.Context, id string, mutation SkillGovernanceMutation) (*model.GovernanceEvent, error) {
	if err := validateSkillGovernanceMutation(mutation); err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin governed Skill delete: %w", err)
	}
	defer tx.Rollback()
	before, err := skillGovernanceStateForUpdateTx(ctx, tx, id, mutation.ActorUserID, false)
	if err != nil {
		return nil, err
	}
	var state skillGovernanceState
	if err := json.Unmarshal(before, &state); err != nil {
		return nil, fmt.Errorf("decode governed Skill delete state: %w", err)
	}
	deleted, err := updateSkillDeletedTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	after := marshalSkillGovernanceState(deleted, state.ImportRecordID)
	event := &model.GovernanceEvent{
		ResourceType: "skill", ResourceKey: id, Action: "delete",
		ActorType: mutation.ActorType, ActorUserID: &mutation.ActorUserID, Reason: mutation.Reason,
		BeforeState: before, AfterState: after, SkillImportRecordID: &state.ImportRecordID,
	}
	if err := InsertGovernanceEventTx(ctx, tx, event); err != nil {
		return nil, fmt.Errorf("audit governed Skill delete: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit governed Skill delete: %w", err)
	}
	return event, nil
}

// RollbackGoverned restores metadata and package references from retained
// local files. The current-state comparison prevents an older event from
// overwriting a later administrator decision, while the unique rollback index
// prevents the same source event from being reversed twice across processes.
func (r *SkillRepository) RollbackGoverned(ctx context.Context, eventID int64, mutation SkillGovernanceMutation) (*model.Skill, *model.GovernanceEvent, error) {
	if err := validateSkillGovernanceMutation(mutation); err != nil {
		return nil, nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("begin governed Skill rollback: %w", err)
	}
	defer tx.Rollback()
	source, err := GetGovernanceEventForUpdateTx(ctx, tx, eventID)
	if err != nil {
		return nil, nil, err
	}
	if source.ResourceType != "skill" || source.Action == "rollback" {
		return nil, nil, fmt.Errorf("%w: event is not a reversible Skill mutation", ErrGovernanceConflict)
	}
	var alreadyRolledBack bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM governance_events WHERE rollback_of_event_id = $1)`, eventID).Scan(&alreadyRolledBack); err != nil {
		return nil, nil, fmt.Errorf("check prior Skill rollback: %w", err)
	}
	if alreadyRolledBack {
		return nil, nil, fmt.Errorf("%w: event was already rolled back", ErrGovernanceConflict)
	}
	current, currentFiles, err := getSkillPackageForUpdateTx(ctx, tx, source.ResourceKey)
	if err != nil {
		return nil, nil, err
	}
	currentRecordID, err := findRetainedSkillVersionTx(ctx, tx, current, currentFiles)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: current Skill package has no retained version", ErrGovernanceConflict)
	}
	currentState := marshalSkillGovernanceState(current, currentRecordID)
	if !equalJSON(currentState, source.AfterState) {
		return nil, nil, fmt.Errorf("%w: Skill changed after the selected event", ErrGovernanceConflict)
	}

	var restored *model.Skill
	var targetRecordID *int64
	if len(source.BeforeState) == 0 {
		if _, err := updateSkillDeletedTx(ctx, tx, source.ResourceKey); err != nil {
			return nil, nil, err
		}
	} else {
		var desired skillGovernanceState
		if err := json.Unmarshal(source.BeforeState, &desired); err != nil {
			return nil, nil, fmt.Errorf("decode Skill rollback state: %w", err)
		}
		if desired.ImportRecordID <= 0 {
			return nil, nil, fmt.Errorf("%w: Skill rollback target has no retained package", ErrGovernanceConflict)
		}
		files, err := listImportRecordFilesTx(ctx, tx, desired.ImportRecordID)
		if err != nil {
			return nil, nil, err
		}
		if !hasSkillEntryFile(files) {
			return nil, nil, fmt.Errorf("%w: retained Skill package has no entry file", ErrGovernanceConflict)
		}
		restored = desired.model()
		if err := upsertSkillPackageTx(ctx, tx, restored, files, nil); err != nil {
			return nil, nil, err
		}
		if desired.Deleted {
			restored, err = updateSkillDeletedTx(ctx, tx, restored.ID)
			if err != nil {
				return nil, nil, err
			}
		}
		targetRecordID = &desired.ImportRecordID
	}
	rollbackID := source.ID
	reverse := &model.GovernanceEvent{
		ResourceType: "skill", ResourceKey: source.ResourceKey, Action: "rollback",
		ActorType: mutation.ActorType, ActorUserID: &mutation.ActorUserID, Reason: mutation.Reason,
		BeforeState: currentState, AfterState: source.BeforeState,
		SkillImportRecordID: targetRecordID, RollbackOfEventID: &rollbackID,
	}
	if err := InsertGovernanceEventTx(ctx, tx, reverse); err != nil {
		return nil, nil, fmt.Errorf("audit governed Skill rollback: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("commit governed Skill rollback: %w", err)
	}
	if restored != nil {
		restored.Files, _ = r.ListFiles(restored.ID)
	}
	return restored, reverse, nil
}

func (r *SkillRepository) RollbackFiles(eventID int64) (string, []model.SkillImportRecordFile, error) {
	event, err := scanGovernanceEvent(r.db.QueryRow(`SELECT `+governanceEventColumns+` FROM governance_events WHERE id = $1`, eventID))
	if err == sql.ErrNoRows {
		return "", nil, fmt.Errorf("governance event not found: %w", ErrNotFound)
	}
	if err != nil {
		return "", nil, fmt.Errorf("get Skill rollback event: %w", err)
	}
	if event.ResourceType != "skill" || event.Action == "rollback" {
		return "", nil, fmt.Errorf("%w: event is not a reversible Skill mutation", ErrGovernanceConflict)
	}
	if len(event.BeforeState) == 0 {
		return event.ResourceKey, nil, nil
	}
	var state skillGovernanceState
	if err := json.Unmarshal(event.BeforeState, &state); err != nil {
		return "", nil, fmt.Errorf("decode Skill rollback package: %w", err)
	}
	if state.ImportRecordID <= 0 {
		return "", nil, fmt.Errorf("%w: Skill rollback target has no retained package", ErrGovernanceConflict)
	}
	files, err := r.ListImportRecordFiles(state.ImportRecordID)
	return event.ResourceKey, files, err
}

func validateSkillGovernanceMutation(mutation SkillGovernanceMutation) error {
	if mutation.ActorUserID <= 0 || mutation.ActorType == "" || mutation.Reason == "" || mutation.Action == "" {
		return fmt.Errorf("Skill governance actor, action, and reason are required")
	}
	return nil
}

func skillGovernanceStateForUpdateTx(ctx context.Context, tx *sql.Tx, id string, actorUserID int64, includeDeleted bool) (json.RawMessage, error) {
	skill, files, err := getSkillPackageForUpdateTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if skill.DeletedAt != nil && !includeDeleted {
		return nil, fmt.Errorf("skill not found: %w", ErrNotFound)
	}
	recordID, err := ensureSkillPackageVersionTx(ctx, tx, skill, files, actorUserID)
	if err != nil {
		return nil, err
	}
	return marshalSkillGovernanceState(skill, recordID), nil
}

func getSkillPackageForUpdateTx(ctx context.Context, tx *sql.Tx, id string) (*model.Skill, []model.SkillFile, error) {
	skill, err := scanSkill(tx.QueryRowContext(ctx, `
		SELECT id, name, description, content, source_type, source_url, source_ref,
		       source_path, checksum, package_checksum, entry_path, min_group_level,
		       enabled, is_builtin, created_by, created_at, updated_at, deleted_at
		FROM skills WHERE id = $1 FOR UPDATE
	`, id))
	if err == sql.ErrNoRows {
		return nil, nil, fmt.Errorf("skill not found: %w", ErrNotFound)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("get Skill for update: %w", err)
	}
	files, err := listSkillFilesTx(ctx, tx, id)
	if err != nil {
		return nil, nil, err
	}
	return skill, files, nil
}

func upsertSkillPackageTx(ctx context.Context, tx *sql.Tx, skill *model.Skill, files []model.SkillFile, record *model.SkillImportRecord) error {
	if skill.EntryPath == "" {
		skill.EntryPath = "SKILL.md"
	}
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO skills (
			id, name, description, content, source_type, source_url, source_ref,
			source_path, checksum, package_checksum, entry_path, min_group_level,
			enabled, is_builtin, created_by
		)
		VALUES ($1, $2, $3, '', $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		ON CONFLICT (id) DO UPDATE SET
			name = EXCLUDED.name, description = EXCLUDED.description, content = '',
			source_type = EXCLUDED.source_type, source_url = EXCLUDED.source_url,
			source_ref = EXCLUDED.source_ref, source_path = EXCLUDED.source_path,
			checksum = EXCLUDED.checksum, package_checksum = EXCLUDED.package_checksum,
			entry_path = EXCLUDED.entry_path, min_group_level = EXCLUDED.min_group_level,
			enabled = EXCLUDED.enabled, is_builtin = EXCLUDED.is_builtin,
			created_by = COALESCE(skills.created_by, EXCLUDED.created_by),
			deleted_at = NULL, updated_at = NOW()
		RETURNING created_by, created_at, updated_at, deleted_at
	`, skill.ID, skill.Name, skill.Description, skill.SourceType, skill.SourceURL,
		skill.SourceRef, skill.SourcePath, skill.Checksum, skill.PackageChecksum,
		skill.EntryPath, skill.MinGroupLevel, skill.Enabled, skill.IsBuiltin,
		skill.CreatedBy).Scan(&skill.CreatedBy, &skill.CreatedAt, &skill.UpdatedAt, &skill.DeletedAt); err != nil {
		return fmt.Errorf("failed to upsert skill: %w", err)
	}
	if err := replaceSkillFilesTx(ctx, tx, skill.ID, files); err != nil {
		return err
	}
	if record != nil {
		if err := insertSkillImportRecordTx(ctx, tx, skill, files, record); err != nil {
			return err
		}
	}
	return nil
}

func replaceSkillFilesTx(ctx context.Context, tx *sql.Tx, skillID string, files []model.SkillFile) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM skill_files WHERE skill_id = $1`, skillID); err != nil {
		return fmt.Errorf("failed to clear skill files: %w", err)
	}
	for _, file := range files {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO skill_files (skill_id, relative_path, storage_path, kind, size, checksum)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, skillID, file.RelativePath, file.StoragePath, file.Kind, file.Size, file.Checksum); err != nil {
			return fmt.Errorf("failed to insert skill file %s: %w", file.RelativePath, err)
		}
	}
	return nil
}

func insertSkillImportRecordTx(ctx context.Context, tx *sql.Tx, skill *model.Skill, files []model.SkillFile, record *model.SkillImportRecord) error {
	record.SkillID = skill.ID
	if record.PackageChecksum == "" {
		record.PackageChecksum = skill.PackageChecksum
	}
	if record.SourceType == "" {
		record.SourceType = skill.SourceType
	}
	if len(record.SelectedFiles) == 0 {
		record.SelectedFiles = json.RawMessage("[]")
	}
	if len(record.FileManifest) == 0 {
		record.FileManifest = json.RawMessage("[]")
	}
	if len(record.ImportReport) == 0 {
		record.ImportReport = json.RawMessage("{}")
	}
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO skill_import_records (
			skill_id, action, source_type, source_url, source_ref, source_path,
			upstream_skill_id, upstream_name, package_checksum,
			selected_files, file_manifest, import_report, created_by
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb, $11::jsonb, $12::jsonb, $13)
		RETURNING id, created_at
	`, record.SkillID, record.Action, record.SourceType, record.SourceURL,
		record.SourceRef, record.SourcePath, record.UpstreamSkillID,
		record.UpstreamName, record.PackageChecksum, string(record.SelectedFiles),
		string(record.FileManifest), string(record.ImportReport), record.CreatedBy,
	).Scan(&record.ID, &record.CreatedAt); err != nil {
		return fmt.Errorf("failed to insert skill import record: %w", err)
	}
	for _, file := range files {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO skill_import_record_files (
				import_record_id, relative_path, storage_path, kind, size, checksum
			) VALUES ($1, $2, $3, $4, $5, $6)
		`, record.ID, file.RelativePath, file.StoragePath, file.Kind, file.Size, file.Checksum); err != nil {
			return fmt.Errorf("failed to retain skill import file %s: %w", file.RelativePath, err)
		}
	}
	return nil
}

func ensureSkillPackageVersionTx(ctx context.Context, tx *sql.Tx, skill *model.Skill, files []model.SkillFile, actorUserID int64) (int64, error) {
	if recordID, err := findRetainedSkillVersionTx(ctx, tx, skill, files); err == nil {
		return recordID, nil
	} else if !errors.Is(err, ErrNotFound) {
		return 0, err
	}
	selected := make([]string, 0, len(files))
	manifest := make([]skillFileManifestItem, 0, len(files))
	for _, file := range files {
		selected = append(selected, file.RelativePath)
		manifest = append(manifest, skillFileManifestItem{file.RelativePath, file.Kind, file.Size, file.Checksum})
	}
	selectedJSON, _ := json.Marshal(selected)
	manifestJSON, _ := json.Marshal(manifest)
	record := &model.SkillImportRecord{
		Action: "update", SkillID: skill.ID, SourceType: skill.SourceType,
		SourceURL: skill.SourceURL, SourceRef: skill.SourceRef,
		UpstreamSkillID: skill.ID, UpstreamName: skill.Name,
		PackageChecksum: skill.PackageChecksum, SelectedFiles: selectedJSON,
		FileManifest: manifestJSON, ImportReport: json.RawMessage(`{"governance_snapshot":true}`),
		CreatedBy: &actorUserID,
	}
	if skill.SourcePath != nil {
		record.SourcePath = *skill.SourcePath
	}
	if err := insertSkillImportRecordTx(ctx, tx, skill, files, record); err != nil {
		return 0, err
	}
	return record.ID, nil
}

func findRetainedSkillVersionTx(ctx context.Context, tx *sql.Tx, skill *model.Skill, files []model.SkillFile) (int64, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id FROM skill_import_records
		WHERE skill_id = $1 AND package_checksum = $2
		ORDER BY created_at DESC, id DESC
	`, skill.ID, skill.PackageChecksum)
	if err != nil {
		return 0, fmt.Errorf("list retained Skill versions: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return 0, fmt.Errorf("scan retained Skill version: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate retained Skill versions: %w", err)
	}
	for _, id := range ids {
		retained, err := listImportRecordFilesTx(ctx, tx, id)
		if err != nil {
			return 0, err
		}
		if sameSkillFiles(files, retained) {
			return id, nil
		}
	}
	return 0, fmt.Errorf("retained Skill version not found: %w", ErrNotFound)
}

func listSkillFilesTx(ctx context.Context, tx *sql.Tx, skillID string) ([]model.SkillFile, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, skill_id, relative_path, storage_path, kind, size, checksum, created_at
		FROM skill_files WHERE skill_id = $1
		ORDER BY CASE WHEN kind = 'entry' THEN 0 ELSE 1 END, relative_path ASC
	`, skillID)
	if err != nil {
		return nil, fmt.Errorf("list Skill files in transaction: %w", err)
	}
	defer rows.Close()
	return scanSkillFileRows(rows)
}

func listImportRecordFilesTx(ctx context.Context, tx *sql.Tx, recordID int64) ([]model.SkillFile, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT relative_path, storage_path, kind, size, checksum
		FROM skill_import_record_files WHERE import_record_id = $1
		ORDER BY CASE WHEN kind = 'entry' THEN 0 ELSE 1 END, relative_path ASC
	`, recordID)
	if err != nil {
		return nil, fmt.Errorf("list retained Skill files in transaction: %w", err)
	}
	defer rows.Close()
	var files []model.SkillFile
	for rows.Next() {
		var file model.SkillFile
		if err := rows.Scan(&file.RelativePath, &file.StoragePath, &file.Kind, &file.Size, &file.Checksum); err != nil {
			return nil, fmt.Errorf("scan retained Skill file: %w", err)
		}
		files = append(files, file)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate retained Skill files: %w", err)
	}
	return files, nil
}

func updateSkillDeletedTx(ctx context.Context, tx *sql.Tx, id string) (*model.Skill, error) {
	skill, err := scanSkill(tx.QueryRowContext(ctx, `
		UPDATE skills SET deleted_at = NOW(), enabled = false, updated_at = NOW()
		WHERE id = $1
		RETURNING id, name, description, content, source_type, source_url, source_ref,
		          source_path, checksum, package_checksum, entry_path, min_group_level,
		          enabled, is_builtin, created_by, created_at, updated_at, deleted_at
	`, id))
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("skill not found: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("write Skill tombstone: %w", err)
	}
	return skill, nil
}

func marshalSkillGovernanceState(skill *model.Skill, importRecordID int64) json.RawMessage {
	if skill == nil {
		return nil
	}
	value, _ := json.Marshal(skillGovernanceState{
		ID: skill.ID, Name: skill.Name, Description: skill.Description,
		SourceType: skill.SourceType, SourceURL: skill.SourceURL, SourceRef: skill.SourceRef,
		SourcePath: skill.SourcePath, Checksum: skill.Checksum,
		PackageChecksum: skill.PackageChecksum, EntryPath: skill.EntryPath,
		MinGroupLevel: skill.MinGroupLevel, Enabled: skill.Enabled, IsBuiltin: skill.IsBuiltin,
		CreatedBy: skill.CreatedBy, ImportRecordID: importRecordID, Deleted: skill.DeletedAt != nil,
	})
	return value
}

func (state skillGovernanceState) model() *model.Skill {
	return &model.Skill{
		ID: state.ID, Name: state.Name, Description: state.Description,
		SourceType: state.SourceType, SourceURL: state.SourceURL, SourceRef: state.SourceRef,
		SourcePath: state.SourcePath, Checksum: state.Checksum,
		PackageChecksum: state.PackageChecksum, EntryPath: state.EntryPath,
		MinGroupLevel: state.MinGroupLevel, Enabled: state.Enabled, IsBuiltin: state.IsBuiltin,
		CreatedBy: state.CreatedBy,
	}
}

func sameSkillFiles(active []model.SkillFile, retained []model.SkillFile) bool {
	if len(active) != len(retained) {
		return false
	}
	byPath := make(map[string]model.SkillFile, len(active))
	for _, file := range active {
		byPath[file.RelativePath] = file
	}
	for _, file := range retained {
		current, ok := byPath[file.RelativePath]
		if !ok || current.StoragePath != file.StoragePath || current.Kind != file.Kind || current.Size != file.Size || current.Checksum != file.Checksum {
			return false
		}
	}
	return true
}

func hasSkillEntryFile(files []model.SkillFile) bool {
	for _, file := range files {
		if file.Kind == "entry" {
			return true
		}
	}
	return false
}
