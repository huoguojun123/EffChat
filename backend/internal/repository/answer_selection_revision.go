package repository

import (
	"context"
	"database/sql"
	"errors"
)

var ErrAnswerSelectionRevisionConflict = errors.New("session answer selection changed before derived state save")

func lockActiveSessionAnswerSelectionRevision(ctx context.Context, tx *sql.Tx, sessionID, userID int64) (int64, error) {
	var revision int64
	err := tx.QueryRowContext(ctx, `
		SELECT answer_selection_revision FROM sessions
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
		FOR UPDATE
	`, sessionID, userID).Scan(&revision)
	if err == sql.ErrNoRows {
		return 0, ErrNotFound
	}
	return revision, err
}

func ensureAnswerSelectionRevision(actual int64, expected *int64) error {
	if expected != nil && actual != *expected {
		return ErrAnswerSelectionRevisionConflict
	}
	return nil
}
