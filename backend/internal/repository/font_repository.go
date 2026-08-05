package repository

import (
	"database/sql"
	"fmt"

	"github.com/huoguojun123/EffChat/internal/model"
)

type FontRepository struct {
	db *sql.DB
}

func NewFontRepository(db *sql.DB) *FontRepository {
	return &FontRepository{db: db}
}

func (r *FontRepository) Create(font *model.FontAsset) error {
	query := `
		INSERT INTO font_assets (
			display_name, family_name, file_name, file_path, mime_type, file_size,
			checksum, weight, style, enabled, created_by
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, created_at, updated_at
	`
	if font.Weight == 0 {
		font.Weight = 400
	}
	if font.Style == "" {
		font.Style = "normal"
	}
	if err := r.db.QueryRow(
		query,
		font.DisplayName,
		font.FamilyName,
		font.FileName,
		font.FilePath,
		font.MimeType,
		font.FileSize,
		font.Checksum,
		font.Weight,
		font.Style,
		font.Enabled,
		font.CreatedBy,
	).Scan(&font.ID, &font.CreatedAt, &font.UpdatedAt); err != nil {
		return fmt.Errorf("create font: %w", err)
	}
	return nil
}

func (r *FontRepository) List() ([]*model.FontAsset, error) {
	rows, err := r.db.Query(`
		SELECT id, display_name, family_name, file_name, file_path, mime_type,
		       file_size, checksum, weight, style, enabled, created_by,
		       created_at, updated_at, deleted_at
		FROM font_assets
		WHERE deleted_at IS NULL
		ORDER BY created_at DESC, id DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list fonts: %w", err)
	}
	defer rows.Close()

	var fonts []*model.FontAsset
	for rows.Next() {
		font := &model.FontAsset{}
		if err := rows.Scan(
			&font.ID,
			&font.DisplayName,
			&font.FamilyName,
			&font.FileName,
			&font.FilePath,
			&font.MimeType,
			&font.FileSize,
			&font.Checksum,
			&font.Weight,
			&font.Style,
			&font.Enabled,
			&font.CreatedBy,
			&font.CreatedAt,
			&font.UpdatedAt,
			&font.DeletedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan font: %w", err)
		}
		fonts = append(fonts, font)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate fonts: %w", err)
	}
	return fonts, nil
}

func (r *FontRepository) Get(id int64) (*model.FontAsset, error) {
	font := &model.FontAsset{}
	err := r.db.QueryRow(`
		SELECT id, display_name, family_name, file_name, file_path, mime_type,
		       file_size, checksum, weight, style, enabled, created_by,
		       created_at, updated_at, deleted_at
		FROM font_assets
		WHERE id = $1 AND deleted_at IS NULL
	`, id).Scan(
		&font.ID,
		&font.DisplayName,
		&font.FamilyName,
		&font.FileName,
		&font.FilePath,
		&font.MimeType,
		&font.FileSize,
		&font.Checksum,
		&font.Weight,
		&font.Style,
		&font.Enabled,
		&font.CreatedBy,
		&font.CreatedAt,
		&font.UpdatedAt,
		&font.DeletedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("font not found: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get font: %w", err)
	}
	return font, nil
}

func (r *FontRepository) Update(font *model.FontAsset) (ChatFontSelection, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return ChatFontSelection{}, fmt.Errorf("begin font update: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.Exec(`
		UPDATE font_assets
		SET display_name = $1, family_name = $2, weight = $3, style = $4, enabled = $5, updated_at = NOW()
		WHERE id = $6 AND deleted_at IS NULL
	`, font.DisplayName, font.FamilyName, font.Weight, font.Style, font.Enabled, font.ID)
	if err != nil {
		return ChatFontSelection{}, fmt.Errorf("failed to update font: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return ChatFontSelection{}, fmt.Errorf("read updated font row count: %w", err)
	}
	if rows == 0 {
		return ChatFontSelection{}, fmt.Errorf("font not found: %w", ErrNotFound)
	}
	if !font.Enabled {
		if err := clearSelectedFontTx(tx, font.ID); err != nil {
			return ChatFontSelection{}, err
		}
	}
	selection, err := getSelectedIDs(tx)
	if err != nil {
		return ChatFontSelection{}, err
	}
	if err := tx.Commit(); err != nil {
		return ChatFontSelection{}, fmt.Errorf("commit font update: %w", err)
	}
	return selection, nil
}

func (r *FontRepository) Delete(id int64) (ChatFontSelection, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return ChatFontSelection{}, fmt.Errorf("begin font delete: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.Exec(`
		UPDATE font_assets
		SET deleted_at = NOW(), enabled = false, updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
	`, id)
	if err != nil {
		return ChatFontSelection{}, fmt.Errorf("failed to delete font: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return ChatFontSelection{}, fmt.Errorf("read deleted font row count: %w", err)
	}
	if rows == 0 {
		return ChatFontSelection{}, fmt.Errorf("font not found: %w", ErrNotFound)
	}
	if err := clearSelectedFontTx(tx, id); err != nil {
		return ChatFontSelection{}, err
	}
	selection, err := getSelectedIDs(tx)
	if err != nil {
		return ChatFontSelection{}, err
	}
	if err := tx.Commit(); err != nil {
		return ChatFontSelection{}, fmt.Errorf("commit font delete: %w", err)
	}
	return selection, nil
}
