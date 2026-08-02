package repository

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/huoguojun123/EffChat/internal/model"
)

const (
	selectedChatFontConfigKey        = "chat_font_asset_id"
	selectedChatChineseFontConfigKey = "chat_font_chinese_asset_id"
	selectedChatLatinFontConfigKey   = "chat_font_latin_asset_id"
	selectedChatCodeFontConfigKey    = "chat_font_code_asset_id"
)

type ChatFontSelection struct {
	Chinese *int64 `json:"chinese"`
	Latin   *int64 `json:"latin"`
	Code    *int64 `json:"code"`
}

type ChatFonts struct {
	Chinese *model.FontAsset `json:"chinese"`
	Latin   *model.FontAsset `json:"latin"`
	Code    *model.FontAsset `json:"code"`
}

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

func (r *FontRepository) Update(font *model.FontAsset) error {
	result, err := r.db.Exec(`
		UPDATE font_assets
		SET display_name = $1, family_name = $2, weight = $3, style = $4, enabled = $5, updated_at = NOW()
		WHERE id = $6 AND deleted_at IS NULL
	`, font.DisplayName, font.FamilyName, font.Weight, font.Style, font.Enabled, font.ID)
	if err != nil {
		return fmt.Errorf("failed to update font: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read updated font row count: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("font not found: %w", ErrNotFound)
	}
	return nil
}

func (r *FontRepository) Delete(id int64) error {
	result, err := r.db.Exec(`
		UPDATE font_assets
		SET deleted_at = NOW(), enabled = false, updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
	`, id)
	if err != nil {
		return fmt.Errorf("failed to delete font: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted font row count: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("font not found: %w", ErrNotFound)
	}
	selection, err := r.GetSelectedIDs()
	if err == nil {
		changed := false
		if selection.Chinese != nil && *selection.Chinese == id {
			selection.Chinese = nil
			changed = true
		}
		if selection.Latin != nil && *selection.Latin == id {
			selection.Latin = nil
			changed = true
		}
		if selection.Code != nil && *selection.Code == id {
			selection.Code = nil
			changed = true
		}
		if changed {
			return r.SetSelectedIDs(selection)
		}
	}
	return nil
}

func (r *FontRepository) GetSelected() (*model.FontAsset, error) {
	id, err := r.GetSelectedID()
	if err != nil || id == nil {
		return nil, err
	}
	font, err := r.Get(*id)
	if err != nil {
		return nil, err
	}
	if !font.Enabled {
		return nil, nil
	}
	return font, nil
}

func (r *FontRepository) GetSelectedID() (*int64, error) {
	return r.getSelectedIDByKey(selectedChatFontConfigKey)
}

func (r *FontRepository) SetSelectedID(id *int64) error {
	return r.setSelectedIDByKey(selectedChatFontConfigKey, id)
}

func (r *FontRepository) GetSelectedIDs() (ChatFontSelection, error) {
	legacyID, err := r.GetSelectedID()
	if err != nil {
		return ChatFontSelection{}, err
	}
	chineseID, err := r.getSelectedIDByKey(selectedChatChineseFontConfigKey)
	if err != nil {
		return ChatFontSelection{}, err
	}
	latinID, err := r.getSelectedIDByKey(selectedChatLatinFontConfigKey)
	if err != nil {
		return ChatFontSelection{}, err
	}
	codeID, err := r.getSelectedIDByKey(selectedChatCodeFontConfigKey)
	if err != nil {
		return ChatFontSelection{}, err
	}
	if chineseID == nil {
		chineseID = legacyID
	}
	if latinID == nil {
		latinID = legacyID
	}
	if codeID == nil {
		codeID = legacyID
	}
	return ChatFontSelection{Chinese: chineseID, Latin: latinID, Code: codeID}, nil
}

func (r *FontRepository) SetSelectedIDs(selection ChatFontSelection) error {
	if err := r.setSelectedIDByKey(selectedChatChineseFontConfigKey, selection.Chinese); err != nil {
		return err
	}
	if err := r.setSelectedIDByKey(selectedChatLatinFontConfigKey, selection.Latin); err != nil {
		return err
	}
	if err := r.setSelectedIDByKey(selectedChatCodeFontConfigKey, selection.Code); err != nil {
		return err
	}
	return r.SetSelectedID(selection.Chinese)
}

func (r *FontRepository) GetSelectedFonts() (ChatFonts, error) {
	selection, err := r.GetSelectedIDs()
	if err != nil {
		return ChatFonts{}, err
	}
	return ChatFonts{
		Chinese: r.enabledFontOrNil(selection.Chinese),
		Latin:   r.enabledFontOrNil(selection.Latin),
		Code:    r.enabledFontOrNil(selection.Code),
	}, nil
}

func (r *FontRepository) enabledFontOrNil(id *int64) *model.FontAsset {
	if id == nil {
		return nil
	}
	font, err := r.Get(*id)
	if err != nil || !font.Enabled {
		return nil
	}
	return font
}

func (r *FontRepository) getSelectedIDByKey(key string) (*int64, error) {
	var raw json.RawMessage
	err := r.db.QueryRow(`SELECT value FROM system_config WHERE key = $1`, key).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get selected font: %w", err)
	}
	if string(raw) == "null" {
		return nil, nil
	}
	var id int64
	if err := json.Unmarshal(raw, &id); err != nil {
		return nil, fmt.Errorf("decode selected font: %w", err)
	}
	if id > 0 {
		return &id, nil
	}
	return nil, fmt.Errorf("decode selected font: invalid font id %d", id)
}

func (r *FontRepository) setSelectedIDByKey(key string, id *int64) error {
	value := json.RawMessage("null")
	if id != nil {
		if _, err := r.Get(*id); err != nil {
			return err
		}
		b, err := json.Marshal(*id)
		if err != nil {
			return err
		}
		value = b
	}
	_, err := r.db.Exec(`
		INSERT INTO system_config (key, value, description, config_type, updated_at)
		VALUES ($1, $2, NULL, 'number', NOW())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, config_type = EXCLUDED.config_type, updated_at = NOW()
	`, key, value)
	if err != nil {
		return fmt.Errorf("failed to set selected font: %w", err)
	}
	return nil
}
