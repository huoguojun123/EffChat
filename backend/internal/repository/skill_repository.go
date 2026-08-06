package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/lib/pq"
)

type SkillRepository struct {
	db *sql.DB
}

func NewSkillRepository(db *sql.DB) *SkillRepository {
	return &SkillRepository{db: db}
}

func (r *SkillRepository) UpsertPackage(skill *model.Skill, files []model.SkillFile) error {
	return r.UpsertPackageWithRecord(skill, files, nil)
}

func (r *SkillRepository) UpsertPackageWithRecord(skill *model.Skill, files []model.SkillFile, record *model.SkillImportRecord) error {
	return r.UpsertPackageWithRecordContext(context.Background(), skill, files, record)
}

func (r *SkillRepository) UpsertPackageWithRecordContext(ctx context.Context, skill *model.Skill, files []model.SkillFile, record *model.SkillImportRecord) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin skill tx: %w", skillContextError(ctx, err))
	}
	defer tx.Rollback()
	if err := upsertSkillPackageTx(ctx, tx, skill, files, record); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit skill tx: %w", skillContextError(ctx, err))
	}
	skill.Files = files
	return nil
}

func skillContextError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

func (r *SkillRepository) ListImportRecords(skillID string, limit int) ([]model.SkillImportRecord, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	rows, err := r.db.Query(`
		SELECT id, skill_id, action, source_type, source_url, source_ref, source_path,
		       upstream_skill_id, upstream_name, package_checksum,
		       selected_files, file_manifest, import_report, created_by, created_at
		FROM skill_import_records
		WHERE skill_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT $2
	`, skillID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list skill import records: %w", err)
	}
	defer rows.Close()
	var records []model.SkillImportRecord
	for rows.Next() {
		record := model.SkillImportRecord{}
		if err := rows.Scan(
			&record.ID,
			&record.SkillID,
			&record.Action,
			&record.SourceType,
			&record.SourceURL,
			&record.SourceRef,
			&record.SourcePath,
			&record.UpstreamSkillID,
			&record.UpstreamName,
			&record.PackageChecksum,
			&record.SelectedFiles,
			&record.FileManifest,
			&record.ImportReport,
			&record.CreatedBy,
			&record.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan skill import record: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate skill import records: %w", err)
	}
	return records, nil
}

func (r *SkillRepository) List(includeDisabled bool) ([]*model.Skill, error) {
	return r.ListContext(context.Background(), includeDisabled)
}

func (r *SkillRepository) ListContext(ctx context.Context, includeDisabled bool) ([]*model.Skill, error) {
	query := `
		SELECT id, name, description, content, source_type, source_url, source_ref,
		       source_path, checksum, package_checksum, entry_path, min_group_level,
		       enabled, is_builtin, created_by, created_at, updated_at, deleted_at
		FROM skills s
		WHERE deleted_at IS NULL
		  AND EXISTS (
		    SELECT 1 FROM skill_files sf
		    WHERE sf.skill_id = s.id AND sf.kind = 'entry'
		  )
	`
	if !includeDisabled {
		query += " AND enabled = true"
	}
	query += " ORDER BY name ASC"

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list skills: %w", err)
	}
	defer rows.Close()

	var skills []*model.Skill
	for rows.Next() {
		skill, err := scanSkill(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan skill: %w", err)
		}
		skills = append(skills, skill)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate skills: %w", err)
	}
	if err := r.attachFilesContext(ctx, skills); err != nil {
		return nil, err
	}
	return skills, nil
}

func (r *SkillRepository) Get(id string, includeDisabled bool) (*model.Skill, error) {
	query := `
		SELECT id, name, description, content, source_type, source_url, source_ref,
		       source_path, checksum, package_checksum, entry_path, min_group_level,
		       enabled, is_builtin, created_by, created_at, updated_at, deleted_at
		FROM skills s
		WHERE id = $1 AND deleted_at IS NULL
		  AND EXISTS (
		    SELECT 1 FROM skill_files sf
		    WHERE sf.skill_id = s.id AND sf.kind = 'entry'
		  )
	`
	if !includeDisabled {
		query += " AND enabled = true"
	}
	skill, err := scanSkill(r.db.QueryRow(query, id))
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("skill not found: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get skill: %w", err)
	}
	files, err := r.ListFiles(id)
	if err != nil {
		return nil, err
	}
	skill.Files = files
	return skill, nil
}

func (r *SkillRepository) Delete(id string) error {
	result, err := r.db.Exec(`UPDATE skills SET deleted_at = NOW(), enabled = false, updated_at = NOW() WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("failed to delete skill: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get deleted skill count: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("skill not found: %w", ErrNotFound)
	}
	return nil
}

func (r *SkillRepository) ListFiles(skillID string) ([]model.SkillFile, error) {
	rows, err := r.db.Query(`
		SELECT id, skill_id, relative_path, storage_path, kind, size, checksum, created_at
		FROM skill_files
		WHERE skill_id = $1
		ORDER BY CASE WHEN kind = 'entry' THEN 0 ELSE 1 END, relative_path ASC
	`, skillID)
	if err != nil {
		return nil, fmt.Errorf("failed to list skill files: %w", err)
	}
	defer rows.Close()
	return scanSkillFileRows(rows)
}

func (r *SkillRepository) GetFile(skillID, path string) (*model.SkillFile, error) {
	file := &model.SkillFile{}
	err := r.db.QueryRow(`
		SELECT id, skill_id, relative_path, storage_path, kind, size, checksum, created_at
		FROM skill_files
		WHERE skill_id = $1 AND relative_path = $2
	`, skillID, path).Scan(
		&file.ID, &file.SkillID, &file.RelativePath, &file.StoragePath, &file.Kind, &file.Size, &file.Checksum, &file.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("skill file not found: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get skill file: %w", err)
	}
	return file, nil
}

func (r *SkillRepository) FilePathsForSkill(id string) ([]string, error) {
	rows, err := r.db.Query(`SELECT storage_path FROM skill_files WHERE skill_id = $1`, id)
	if err != nil {
		return nil, fmt.Errorf("failed to list skill paths: %w", err)
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, fmt.Errorf("failed to scan skill path: %w", err)
		}
		paths = append(paths, path)
	}
	return paths, rows.Err()
}

// RetainedFilePaths returns both the active package files and immutable files
// referenced by import history. Cleanup must preserve both sets: an inactive
// package may still be the only locally available source for a native rollback.
func (r *SkillRepository) RetainedFilePaths() ([]string, error) {
	rows, err := r.db.Query(`
		SELECT f.storage_path
		FROM skill_files f
		JOIN skills s ON s.id = f.skill_id
		WHERE s.deleted_at IS NULL
		UNION
		SELECT storage_path
		FROM skill_import_record_files
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list active skill paths: %w", err)
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, fmt.Errorf("failed to scan active skill path: %w", err)
		}
		paths = append(paths, path)
	}
	return paths, rows.Err()
}

func (r *SkillRepository) ListImportRecordFiles(recordID int64) ([]model.SkillImportRecordFile, error) {
	rows, err := r.db.Query(`
		SELECT import_record_id, relative_path, storage_path, kind, size, checksum
		FROM skill_import_record_files
		WHERE import_record_id = $1
		ORDER BY CASE WHEN kind = 'entry' THEN 0 ELSE 1 END, relative_path ASC
	`, recordID)
	if err != nil {
		return nil, fmt.Errorf("failed to list retained skill import files: %w", err)
	}
	defer rows.Close()
	var files []model.SkillImportRecordFile
	for rows.Next() {
		var file model.SkillImportRecordFile
		if err := rows.Scan(&file.ImportRecordID, &file.RelativePath, &file.StoragePath, &file.Kind, &file.Size, &file.Checksum); err != nil {
			return nil, fmt.Errorf("failed to scan retained skill import file: %w", err)
		}
		files = append(files, file)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate retained skill import files: %w", err)
	}
	return files, nil
}

type skillScanner interface {
	Scan(dest ...interface{}) error
}

func scanSkill(row skillScanner) (*model.Skill, error) {
	skill := &model.Skill{}
	err := row.Scan(
		&skill.ID,
		&skill.Name,
		&skill.Description,
		&skill.Content,
		&skill.SourceType,
		&skill.SourceURL,
		&skill.SourceRef,
		&skill.SourcePath,
		&skill.Checksum,
		&skill.PackageChecksum,
		&skill.EntryPath,
		&skill.MinGroupLevel,
		&skill.Enabled,
		&skill.IsBuiltin,
		&skill.CreatedBy,
		&skill.CreatedAt,
		&skill.UpdatedAt,
		&skill.DeletedAt,
	)
	return skill, err
}

func (r *SkillRepository) attachFilesContext(ctx context.Context, skills []*model.Skill) error {
	if len(skills) == 0 {
		return nil
	}
	ids := make([]string, 0, len(skills))
	byID := make(map[string]*model.Skill, len(skills))
	for _, skill := range skills {
		ids = append(ids, skill.ID)
		byID[skill.ID] = skill
		skill.Files = nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, skill_id, relative_path, storage_path, kind, size, checksum, created_at
		FROM skill_files
		WHERE skill_id = ANY($1)
		ORDER BY skill_id ASC, CASE WHEN kind = 'entry' THEN 0 ELSE 1 END, relative_path ASC
	`, pq.Array(ids))
	if err != nil {
		return fmt.Errorf("failed to list skill files: %w", err)
	}
	defer rows.Close()
	files, err := scanSkillFileRows(rows)
	if err != nil {
		return err
	}
	for _, file := range files {
		if skill := byID[file.SkillID]; skill != nil {
			skill.Files = append(skill.Files, file)
		}
	}
	return nil
}

func scanSkillFileRows(rows *sql.Rows) ([]model.SkillFile, error) {
	var files []model.SkillFile
	for rows.Next() {
		file := model.SkillFile{}
		if err := rows.Scan(
			&file.ID,
			&file.SkillID,
			&file.RelativePath,
			&file.StoragePath,
			&file.Kind,
			&file.Size,
			&file.Checksum,
			&file.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan skill file: %w", err)
		}
		files = append(files, file)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate skill files: %w", err)
	}
	return files, nil
}
