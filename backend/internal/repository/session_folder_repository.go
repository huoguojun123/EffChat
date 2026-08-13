package repository

import (
	"database/sql"
	"fmt"

	"github.com/huoguojun123/EffChat/internal/model"
)

type SessionFolderRepository struct {
	db *sql.DB
}

type SessionFolderPatch struct {
	Name      string
	NameSet   bool
	Pinned    bool
	PinnedSet bool
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
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate session folders: %w", err)
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

func (r *SessionFolderRepository) Patch(id, userID int64, patch SessionFolderPatch) (*model.SessionFolder, error) {
	folder := &model.SessionFolder{}
	// The public PATCH accepts name and pinned together. One owner-scoped SQL
	// statement is the atomic boundary: a constraint or database failure cannot
	// leave one requested field committed while the other is rejected.
	query := `
		UPDATE session_folders
		SET name = CASE WHEN $1 THEN $2 ELSE name END,
		    pinned_at = CASE WHEN $3 THEN CASE WHEN $4 THEN NOW() ELSE NULL END ELSE pinned_at END
		WHERE id = $5 AND user_id = $6
		RETURNING id, user_id, name, pinned_at, created_at, updated_at
	`
	err := r.db.QueryRow(query, patch.NameSet, patch.Name, patch.PinnedSet, patch.Pinned, id, userID).Scan(
		&folder.ID,
		&folder.UserID,
		&folder.Name,
		&folder.PinnedAt,
		&folder.CreatedAt,
		&folder.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("session folder not found: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to patch session folder: %w", err)
	}
	return folder, nil
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
		return fmt.Errorf("session folder not found: %w", ErrNotFound)
	}
	return nil
}
