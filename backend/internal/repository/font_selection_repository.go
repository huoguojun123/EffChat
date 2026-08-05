package repository

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/huoguojun123/EffChat/internal/model"
)

var ErrFontUnavailable = errors.New("font is not available")

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

type ChatFontSlot string

const (
	ChatFontSlotChinese ChatFontSlot = "chinese"
	ChatFontSlotLatin   ChatFontSlot = "latin"
	ChatFontSlotCode    ChatFontSlot = "code"
)

type ChatFonts struct {
	Chinese *model.FontAsset `json:"chinese"`
	Latin   *model.FontAsset `json:"latin"`
	Code    *model.FontAsset `json:"code"`
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
	return getSelectedIDByKey(r.db, selectedChatFontConfigKey)
}

func (r *FontRepository) SetSelectedID(id *int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin selected font update: %w", err)
	}
	defer tx.Rollback()
	if err := ensureSelectableFontTx(tx, id); err != nil {
		return err
	}
	if err := setSelectedIDByKeyTx(tx, selectedChatFontConfigKey, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit selected font update: %w", err)
	}
	return nil
}

func (r *FontRepository) GetSelectedIDs() (ChatFontSelection, error) {
	return getSelectedIDs(r.db)
}

// SetSelectedSlot owns exactly one visible slot. The Chinese slot and the
// legacy compatibility key share a transaction; unrelated slots are never
// rewritten from a stale snapshot.
func (r *FontRepository) SetSelectedSlot(slot ChatFontSlot, id *int64) (ChatFontSelection, error) {
	key, ok := selectedFontConfigKey(slot)
	if !ok {
		return ChatFontSelection{}, fmt.Errorf("invalid font slot %q", slot)
	}
	tx, err := r.db.Begin()
	if err != nil {
		return ChatFontSelection{}, fmt.Errorf("begin font selection update: %w", err)
	}
	defer tx.Rollback()

	// Locking the asset row serializes selection with disable/delete. Either the
	// selection commits first and is then cleared, or it observes an unavailable
	// asset; a stale reference cannot be committed after the lifecycle change.
	if err := ensureSelectableFontTx(tx, id); err != nil {
		return ChatFontSelection{}, err
	}
	if err := setSelectedIDByKeyTx(tx, key, id); err != nil {
		return ChatFontSelection{}, err
	}
	if slot == ChatFontSlotChinese {
		if err := setSelectedIDByKeyTx(tx, selectedChatFontConfigKey, id); err != nil {
			return ChatFontSelection{}, err
		}
	}
	selection, err := getSelectedIDs(tx)
	if err != nil {
		return ChatFontSelection{}, err
	}
	if err := tx.Commit(); err != nil {
		return ChatFontSelection{}, fmt.Errorf("commit font selection update: %w", err)
	}
	return selection, nil
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

type fontSelectionQueryer interface {
	QueryRow(query string, args ...any) *sql.Row
}

func getSelectedIDs(queryer fontSelectionQueryer) (ChatFontSelection, error) {
	legacyID, err := getSelectedIDByKey(queryer, selectedChatFontConfigKey)
	if err != nil {
		return ChatFontSelection{}, err
	}
	chineseID, err := getSelectedIDByKey(queryer, selectedChatChineseFontConfigKey)
	if err != nil {
		return ChatFontSelection{}, err
	}
	latinID, err := getSelectedIDByKey(queryer, selectedChatLatinFontConfigKey)
	if err != nil {
		return ChatFontSelection{}, err
	}
	codeID, err := getSelectedIDByKey(queryer, selectedChatCodeFontConfigKey)
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

func getSelectedIDByKey(queryer fontSelectionQueryer, key string) (*int64, error) {
	var raw json.RawMessage
	err := queryer.QueryRow(`SELECT value FROM system_config WHERE key = $1`, key).Scan(&raw)
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

func selectedFontConfigKey(slot ChatFontSlot) (string, bool) {
	switch slot {
	case ChatFontSlotChinese:
		return selectedChatChineseFontConfigKey, true
	case ChatFontSlotLatin:
		return selectedChatLatinFontConfigKey, true
	case ChatFontSlotCode:
		return selectedChatCodeFontConfigKey, true
	default:
		return "", false
	}
}

func ensureSelectableFontTx(tx *sql.Tx, id *int64) error {
	if id == nil {
		return nil
	}
	var enabled bool
	err := tx.QueryRow(`
		SELECT enabled
		FROM font_assets
		WHERE id = $1 AND deleted_at IS NULL
		FOR UPDATE
	`, *id).Scan(&enabled)
	if err == sql.ErrNoRows {
		return fmt.Errorf("font not found: %w", ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("failed to validate selected font: %w", err)
	}
	if !enabled {
		return ErrFontUnavailable
	}
	return nil
}

func setSelectedIDByKeyTx(tx *sql.Tx, key string, id *int64) error {
	value := json.RawMessage("null")
	if id != nil {
		b, err := json.Marshal(*id)
		if err != nil {
			return err
		}
		value = b
	}
	_, err := tx.Exec(`
		INSERT INTO system_config (key, value, description, config_type, updated_at)
		VALUES ($1, $2, NULL, 'number', NOW())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, config_type = EXCLUDED.config_type, updated_at = NOW()
	`, key, value)
	if err != nil {
		return fmt.Errorf("failed to set selected font: %w", err)
	}
	return nil
}

func clearSelectedFontTx(tx *sql.Tx, id int64) error {
	// The predicate is the ownership fence: a concurrent selection of another
	// font survives, while every slot still owned by this asset is cleared in
	// the same transaction as disable/delete.
	_, err := tx.Exec(`
		UPDATE system_config
		SET value = 'null'::jsonb, config_type = 'number', updated_at = NOW()
		WHERE key IN ($1, $2, $3, $4)
		  AND value = to_jsonb($5::bigint)
	`, selectedChatFontConfigKey, selectedChatChineseFontConfigKey,
		selectedChatLatinFontConfigKey, selectedChatCodeFontConfigKey, id)
	if err != nil {
		return fmt.Errorf("failed to clear selected font: %w", err)
	}
	return nil
}
