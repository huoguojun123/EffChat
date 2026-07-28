package repository

import (
	"database/sql"
	"fmt"

	"github.com/huoguojun123/effchat/internal/model"
)

type SessionFolderRepository struct {
	db *sql.DB
}

func NewSessionFolderRepository(db *sql.DB) *SessionFolderRepository {
	return &SessionFolderRepository{db: db}
}

func (r *SessionFolderRepository) Create(folder *model.SessionFolder) error {
	query := `
		INSERT INTO session_folders (user_id, name)
		VALUES ($1, $2)
		RETURNING id, created_at, updated_at
	`
	if err := r.db.QueryRow(query, folder.UserID, folder.Name).Scan(&folder.ID, &folder.CreatedAt, &folder.UpdatedAt); err != nil {
		return fmt.Errorf("failed to create session folder: %w", err)
	}
	return nil
}

func (r *SessionFolderRepository) ListByUser(userID int64) ([]*model.SessionFolder, error) {
	query := `
		SELECT id, user_id, name, pinned_at, created_at, updated_at
		FROM session_folders
		WHERE user_id = $1
		ORDER BY pinned_at DESC NULLS LAST, name ASC, id ASC
	`
	rows, err := r.db.Query(query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list session folders: %w", err)
	}
	defer rows.Close()

	folders := []*model.SessionFolder{}
	for rows.Next() {
		folder := &model.SessionFolder{}
		if err := rows.Scan(&folder.ID, &folder.UserID, &folder.Name, &folder.PinnedAt, &folder.CreatedAt, &folder.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan session folder: %w", err)
		}
		folders = append(folders, folder)
	}
	return folders, nil
}

func (r *SessionFolderRepository) GetByID(id, userID int64) (*model.SessionFolder, error) {
	folder := &model.SessionFolder{}
	query := `
		SELECT id, user_id, name, pinned_at, created_at, updated_at
		FROM session_folders
		WHERE id = $1 AND user_id = $2
	`
	err := r.db.QueryRow(query, id, userID).Scan(&folder.ID, &folder.UserID, &folder.Name, &folder.PinnedAt, &folder.CreatedAt, &folder.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("session folder not found: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get session folder: %w", err)
	}
	return folder, nil
}

func (r *SessionFolderRepository) SetPinned(id, userID int64, pinned bool) error {
	result, err := r.db.Exec(`UPDATE session_folders SET pinned_at = CASE WHEN $1 THEN NOW() ELSE NULL END WHERE id = $2 AND user_id = $3`, pinned, id, userID)
	if err != nil {
		return fmt.Errorf("failed to update session folder pin: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("session folder not found")
	}
	return nil
}

func (r *SessionFolderRepository) Update(folder *model.SessionFolder) error {
	query := `
		UPDATE session_folders
		SET name = $1
		WHERE id = $2 AND user_id = $3
	`
	result, err := r.db.Exec(query, folder.Name, folder.ID, folder.UserID)
	if err != nil {
		return fmt.Errorf("failed to update session folder: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("session folder not found")
	}
	return nil
}

func (r *SessionFolderRepository) Delete(id, userID int64) error {
	query := `DELETE FROM session_folders WHERE id = $1 AND user_id = $2`
	result, err := r.db.Exec(query, id, userID)
	if err != nil {
		return fmt.Errorf("failed to delete session folder: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("session folder not found")
	}
	return nil
}
