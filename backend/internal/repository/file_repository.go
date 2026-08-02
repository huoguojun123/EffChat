package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/lib/pq"
)

type FileRepository struct {
	db *sql.DB
}

const (
	FileStatusStaged         = "staged"
	FileStatusFormal         = "formal"
	FileStatusCleanupClaimed = "cleanup_claimed"
	FileStatusStorageRemoved = "storage_removed"
)

var (
	ErrOCRSourceUnavailable  = errors.New("ocr source is unavailable")
	ErrOCRLeaseLost          = errors.New("ocr lease is no longer owned")
	ErrAttachmentUnavailable = errors.New("attachment is unavailable")
	cleanupClaimSequence     atomic.Uint64
)

type FileCleanupClaim struct {
	File  *model.File
	Token string
}

func attachmentIDsFromMessageData(data []byte) ([]int64, error) {
	var payload struct {
		Attachments []struct {
			FileID json.RawMessage `json:"file_id"`
		} `json:"attachments"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("decode message attachments: %w", err)
	}
	if len(payload.Attachments) == 0 {
		return nil, nil
	}

	ids := make([]int64, 0, len(payload.Attachments))
	seen := make(map[int64]struct{}, len(payload.Attachments))
	for _, attachment := range payload.Attachments {
		id, ok := parseAttachmentFileID(attachment.FileID)
		if !ok {
			return nil, fmt.Errorf("invalid attachment file_id: %w", ErrAttachmentUnavailable)
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, nil
}

func parseAttachmentFileID(raw json.RawMessage) (int64, bool) {
	var id int64
	if len(raw) > 0 && json.Unmarshal(raw, &id) == nil && id > 0 {
		return id, true
	}
	var value string
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return 0, false
	}
	id, err := strconv.ParseInt(value, 10, 64)
	return id, err == nil && id > 0
}

func isUserMessageData(data []byte) (bool, error) {
	var payload struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return false, fmt.Errorf("decode message role: %w", err)
	}
	return payload.Role == "user", nil
}

func claimStagedAttachmentsForMessages(ctx context.Context, tx *sql.Tx, sessionID, userID int64, messages []*model.Message) error {
	ids := make([]int64, 0)
	seen := make(map[int64]struct{})
	for _, message := range messages {
		isUser, err := isUserMessageData(message.MessageData)
		if err != nil {
			return err
		}
		if !isUser {
			continue
		}
		attachmentIDs, err := attachmentIDsFromMessageData(message.MessageData)
		if err != nil {
			return err
		}
		for _, id := range attachmentIDs {
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}

	rows, err := tx.QueryContext(ctx, `
		UPDATE files
		SET status = 'formal'
		WHERE user_id = $1
		  AND session_id = $2
		  AND id = ANY($3)
		  AND status = 'staged'
		  AND (file_type LIKE 'image/%' OR extract_status = 'ready')
		RETURNING id
	`, userID, sessionID, pq.Array(ids))
	if err != nil {
		return fmt.Errorf("claim staged attachments: %w", err)
	}
	claimed := make(map[int64]struct{}, len(ids))
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("scan staged attachment claim: %w", err)
		}
		claimed[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate staged attachment claim: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close staged attachment claim: %w", err)
	}
	if len(claimed) != len(ids) {
		return fmt.Errorf("attachment is no longer available: %w", ErrAttachmentUnavailable)
	}
	return nil
}

func ensureFormalMessageAttachmentsAvailableTx(ctx context.Context, tx *sql.Tx, sessionID, userID int64, message *model.Message) error {
	ids, err := attachmentIDsFromMessageData(message.MessageData)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id
		FROM files
		WHERE user_id = $1
		  AND session_id = $2
		  AND id = ANY($3)
		  AND status = 'formal'
		FOR KEY SHARE
	`, userID, sessionID, pq.Array(ids))
	if err != nil {
		return fmt.Errorf("lock formal attachments for retry: %w", err)
	}
	available := make(map[int64]struct{}, len(ids))
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("scan formal attachment for retry: %w", err)
		}
		available[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate formal attachments for retry: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close formal attachments for retry: %w", err)
	}
	if len(available) != len(ids) {
		return fmt.Errorf("attachment required by this retry is unavailable: %w", ErrAttachmentUnavailable)
	}
	return nil
}

func markMessageAttachmentsUnavailable(ctx context.Context, tx *sql.Tx, fileID int64) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE messages m
		SET message_data = jsonb_set(
			m.message_data,
			'{attachments}',
			(
				SELECT jsonb_agg(
					CASE
						WHEN attachment.item->>'file_id' ~ '^[0-9]+$'
						 AND (attachment.item->>'file_id')::bigint = $1
						THEN jsonb_set(attachment.item, '{unavailable}', 'true'::jsonb, true)
						ELSE attachment.item
					END
					ORDER BY attachment.ordinality
				)
				FROM jsonb_array_elements(m.message_data->'attachments') WITH ORDINALITY AS attachment(item, ordinality)
			),
			false
		),
		updated_at = NOW()
		WHERE jsonb_typeof(m.message_data->'attachments') = 'array'
		  AND EXISTS (
			SELECT 1
			FROM jsonb_array_elements(m.message_data->'attachments') AS attachment(item)
			WHERE attachment.item->>'file_id' ~ '^[0-9]+$'
			  AND (attachment.item->>'file_id')::bigint = $1
		)
	`, fileID)
	if err != nil {
		return fmt.Errorf("mark message attachments unavailable: %w", err)
	}
	return nil
}

func NewFileRepository(db *sql.DB) *FileRepository {
	return &FileRepository{db: db}
}

func (r *FileRepository) Create(f *model.File) error {
	query := `
		INSERT INTO files (
			user_id, session_id, file_name, file_path, file_type, file_size, file_hash,
			extracted_text_path, extract_status, extract_error, token_estimate,
			ocr_provider, ocr_task_id, ocr_page_count, ocr_progress_pages, ocr_started_at, ocr_completed_at, ocr_error_type, ocr_source_path
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
		RETURNING id, status, created_at
	`
	if f.ExtractStatus == "" {
		f.ExtractStatus = "pending"
	}
	err := r.db.QueryRow(
		query, f.UserID, f.SessionID, f.FileName, f.FilePath, f.FileType, f.FileSize, f.FileHash,
		f.ExtractedTextPath, f.ExtractStatus, f.ExtractError, f.TokenEstimate,
		f.OCRProvider, f.OCRTaskID, f.OCRPageCount, f.OCRProgressPages, f.OCRStartedAt, f.OCRCompletedAt, f.OCRErrorType, f.OCRSourcePath,
	).Scan(&f.ID, &f.Status, &f.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	return nil
}

func (r *FileRepository) GetByID(id, userID int64) (*model.File, error) {
	f := &model.File{}
	query := `
		SELECT id, user_id, session_id, file_name, file_path, file_type, file_size, file_hash, status,
		       extracted_text_path, extract_status, extract_error, token_estimate,
		       ocr_provider, ocr_task_id, ocr_page_count, ocr_progress_pages, ocr_started_at, ocr_completed_at, ocr_error_type, ocr_source_path,
		       ocr_lease_until, ocr_lease_generation, ocr_attempts, ocr_next_retry_at,
		       created_at
		FROM files
			WHERE id = $1 AND user_id = $2 AND status IN ('staged', 'formal')
	`
	err := r.db.QueryRow(query, id, userID).Scan(
		&f.ID, &f.UserID, &f.SessionID, &f.FileName, &f.FilePath, &f.FileType, &f.FileSize, &f.FileHash, &f.Status,
		&f.ExtractedTextPath, &f.ExtractStatus, &f.ExtractError, &f.TokenEstimate,
		&f.OCRProvider, &f.OCRTaskID, &f.OCRPageCount, &f.OCRProgressPages, &f.OCRStartedAt, &f.OCRCompletedAt, &f.OCRErrorType, &f.OCRSourcePath,
		&f.OCRLeaseUntil, &f.OCRLeaseGeneration, &f.OCRAttempts, &f.OCRNextRetryAt,
		&f.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("file not found: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get file: %w", err)
	}
	return f, nil
}

func (r *FileRepository) ListByUser(userID int64, limit, offset int) ([]*model.File, error) {
	query := `
		SELECT id, user_id, session_id, file_name, file_path, file_type, file_size, file_hash, status,
		       extracted_text_path, extract_status, extract_error, token_estimate,
		       ocr_provider, ocr_task_id, ocr_page_count, ocr_progress_pages, ocr_started_at, ocr_completed_at, ocr_error_type, ocr_source_path,
		       created_at
		FROM files
			WHERE user_id = $1 AND status IN ('staged', 'formal')
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`
	rows, err := r.db.Query(query, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list files: %w", err)
	}
	defer rows.Close()
	return scanFileRows(rows)
}

func (r *FileRepository) ListBySession(userID, sessionID int64, limit, offset int) ([]*model.File, error) {
	query := `
		SELECT id, user_id, session_id, file_name, file_path, file_type, file_size, file_hash, status,
		       extracted_text_path, extract_status, extract_error, token_estimate,
		       ocr_provider, ocr_task_id, ocr_page_count, ocr_progress_pages, ocr_started_at, ocr_completed_at, ocr_error_type, ocr_source_path,
		       created_at
		FROM files
			WHERE user_id = $1 AND session_id = $2 AND status IN ('staged', 'formal')
		ORDER BY created_at DESC, id DESC
		LIMIT $3 OFFSET $4
	`
	rows, err := r.db.Query(query, userID, sessionID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list session files: %w", err)
	}
	defer rows.Close()
	return scanFileRows(rows)
}

func (r *FileRepository) ListUnreferencedBySession(userID, sessionID int64, limit, offset int) ([]*model.File, error) {
	query := `
		SELECT f.id, f.user_id, f.session_id, f.file_name, f.file_path, f.file_type, f.file_size, f.file_hash, f.status,
		       f.extracted_text_path, f.extract_status, f.extract_error, f.token_estimate,
		       f.ocr_provider, f.ocr_task_id, f.ocr_page_count, f.ocr_progress_pages, f.ocr_started_at, f.ocr_completed_at, f.ocr_error_type, f.ocr_source_path,
		       f.created_at
		FROM files f
		WHERE f.user_id = $1
		  AND f.session_id = $2
			  AND f.status = 'staged'
		ORDER BY f.created_at ASC, f.id ASC
		LIMIT $3 OFFSET $4
	`
	rows, err := r.db.Query(query, userID, sessionID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list unreferenced session files: %w", err)
	}
	defer rows.Close()
	return scanFileRows(rows)
}

func (r *FileRepository) ListReferencedBySession(userID, sessionID int64, limit, offset int) ([]*model.File, error) {
	query := `
		SELECT f.id, f.user_id, f.session_id, f.file_name, f.file_path, f.file_type, f.file_size, f.file_hash, f.status,
		       f.extracted_text_path, f.extract_status, f.extract_error, f.token_estimate,
		       f.ocr_provider, f.ocr_task_id, f.ocr_page_count, f.ocr_progress_pages, f.ocr_started_at, f.ocr_completed_at, f.ocr_error_type, f.ocr_source_path,
		       f.created_at
		FROM files f
		WHERE f.user_id = $1
		  AND f.session_id = $2
			  AND f.status = 'formal'
		ORDER BY f.created_at DESC, f.id DESC
		LIMIT $3 OFFSET $4
	`
	rows, err := r.db.Query(query, userID, sessionID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list referenced session files: %w", err)
	}
	defer rows.Close()
	return scanFileRows(rows)
}

func (r *FileRepository) IsReferencedByMessage(userID, fileID int64) (bool, error) {
	var referenced bool
	err := r.db.QueryRow(`
		SELECT EXISTS (
		  SELECT 1
		  FROM files
		  WHERE id = $1 AND user_id = $2 AND status = 'formal'
		)
	`, fileID, userID).Scan(&referenced)
	if err != nil {
		return false, fmt.Errorf("failed to check file references: %w", err)
	}
	return referenced, nil
}

func (r *FileRepository) CountActiveBySession(userID, sessionID int64) (int, error) {
	var count int
	err := r.db.QueryRow(
		`SELECT COUNT(*) FROM files WHERE user_id = $1 AND session_id = $2 AND status IN ('staged', 'formal')`,
		userID, sessionID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count session files: %w", err)
	}
	return count, nil
}

func (r *FileRepository) CountActiveOCRTasks(provider string) (int, error) {
	var count int
	err := r.db.QueryRow(`
		SELECT COUNT(*)
		FROM files
			WHERE status = 'staged'
		  AND ocr_provider = $1
		  AND extract_status IN ('ocr_pending', 'ocr_running')
	`, provider).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count active OCR tasks: %w", err)
	}
	return count, nil
}

func (r *FileRepository) FindActiveByHashInSession(userID, sessionID int64, hash string, size int64) (*model.File, error) {
	f := &model.File{}
	query := `
		SELECT id, user_id, session_id, file_name, file_path, file_type, file_size, file_hash, status,
		       extracted_text_path, extract_status, extract_error, token_estimate,
		       ocr_provider, ocr_task_id, ocr_page_count, ocr_progress_pages, ocr_started_at, ocr_completed_at, ocr_error_type, ocr_source_path,
		       created_at
		FROM files
		WHERE user_id = $1
		  AND session_id = $2
		  AND file_hash = $3
		  AND file_size = $4
			  AND status = 'staged'
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`
	err := r.db.QueryRow(query, userID, sessionID, hash, size).Scan(
		&f.ID, &f.UserID, &f.SessionID, &f.FileName, &f.FilePath, &f.FileType, &f.FileSize, &f.FileHash, &f.Status,
		&f.ExtractedTextPath, &f.ExtractStatus, &f.ExtractError, &f.TokenEstimate,
		&f.OCRProvider, &f.OCRTaskID, &f.OCRPageCount, &f.OCRProgressPages, &f.OCRStartedAt, &f.OCRCompletedAt, &f.OCRErrorType, &f.OCRSourcePath,
		&f.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("file not found: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find duplicate file: %w", err)
	}
	return f, nil
}

func (r *FileRepository) RequestDeletion(ctx context.Context, id, userID int64, now, cleanupAfter time.Time) error {
	if now.IsZero() {
		now = time.Now()
	}
	if cleanupAfter.Before(now) {
		cleanupAfter = now
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin file deletion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var sessionID sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT session_id FROM files WHERE id = $1 AND user_id = $2`, id, userID).Scan(&sessionID)
	if err == sql.ErrNoRows {
		return fmt.Errorf("file not found: %w", ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("load file for deletion: %w", err)
	}

	// Match retry's session -> messages -> files order to avoid a lock cycle.
	if sessionID.Valid {
		if err := lockSessionForFileDeletion(ctx, tx, sessionID.Int64, userID); err != nil {
			return err
		}
		if err := lockActiveMessagesForFileDeletion(ctx, tx, sessionID.Int64); err != nil {
			return err
		}
	}

	var status string
	err = tx.QueryRowContext(ctx, `SELECT status FROM files WHERE id = $1 AND user_id = $2 FOR UPDATE`, id, userID).Scan(&status)
	if err == sql.ErrNoRows {
		return fmt.Errorf("file not found: %w", ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("lock file for deletion: %w", err)
	}
	if status == FileStatusCleanupClaimed || status == FileStatusStorageRemoved {
		return tx.Commit()
	}
	if status != FileStatusStaged && status != FileStatusFormal {
		return fmt.Errorf("file is not available: %w", ErrAttachmentUnavailable)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE files
		SET status = 'cleanup_claimed',
			deleted_at = COALESCE(deleted_at, $3),
			cleanup_after = $4,
			cleanup_claim_token = NULL,
			cleanup_lease_until = NULL,
			ocr_lease_until = NULL,
			ocr_lease_generation = ocr_lease_generation + 1,
			ocr_next_retry_at = NULL,
			extract_status = CASE WHEN extract_status IN ('ocr_pending', 'ocr_running') THEN 'failed' ELSE extract_status END,
			extract_error = CASE WHEN extract_status IN ('ocr_pending', 'ocr_running') THEN '附件已删除，解析已停止' ELSE extract_error END,
			ocr_error_type = CASE WHEN extract_status IN ('ocr_pending', 'ocr_running') THEN 'attachment_deleted' ELSE ocr_error_type END
		WHERE id = $1 AND user_id = $2
	`, id, userID, now, cleanupAfter); err != nil {
		return fmt.Errorf("mark file for cleanup: %w", err)
	}
	if status == FileStatusFormal {
		if err := markMessageAttachmentsUnavailable(ctx, tx, id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit file deletion: %w", err)
	}
	return nil
}

func lockSessionForFileDeletion(ctx context.Context, tx *sql.Tx, sessionID, userID int64) error {
	var found int
	err := tx.QueryRowContext(ctx, `
		SELECT 1
		FROM sessions
		WHERE id = $1 AND user_id = $2
		FOR UPDATE
	`, sessionID, userID).Scan(&found)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lock session for file deletion: %w", err)
	}
	return nil
}

func lockActiveMessagesForFileDeletion(ctx context.Context, tx *sql.Tx, sessionID int64) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT id
		FROM messages
		WHERE session_id = $1 AND deleted_at IS NULL
		ORDER BY id ASC
		FOR UPDATE
	`, sessionID)
	if err != nil {
		return fmt.Errorf("lock messages for file deletion: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("scan message lock for file deletion: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate message locks for file deletion: %w", err)
	}
	return nil
}

func (r *FileRepository) UpdateOCRRunning(id, userID, generation int64, progressPages int) error {
	if progressPages < 0 {
		progressPages = 0
	}
	result, err := r.db.Exec(`
		UPDATE files
		SET extract_status = 'ocr_running',
		    ocr_progress_pages = GREATEST(ocr_progress_pages, $4)
		WHERE id = $1
		  AND user_id = $2
		  AND $3 > 0
		  AND ocr_lease_generation = $3
		  AND status = 'staged'
		  AND extract_status IN ('ocr_pending', 'ocr_running')
	`, id, userID, generation, progressPages)
	if err != nil {
		return fmt.Errorf("failed to update OCR running status: %w", err)
	}
	return requireOCRLeaseMutation(result)
}

func (r *FileRepository) StartOCRTask(id, userID, generation int64, taskID string, pageCount int) error {
	if pageCount < 0 {
		pageCount = 0
	}
	result, err := r.db.Exec(`
		UPDATE files
		SET extract_status = 'ocr_running',
		    ocr_task_id = NULLIF($4, ''),
		    ocr_error_type = NULL,
		    ocr_page_count = GREATEST(ocr_page_count, $5),
		    ocr_started_at = COALESCE(ocr_started_at, NOW()),
		    ocr_next_retry_at = NOW()
		WHERE id = $1
		  AND user_id = $2
		  AND $3 > 0
		  AND ocr_lease_generation = $3
		  AND status = 'staged'
		  AND extract_status IN ('ocr_pending', 'ocr_running')
	`, id, userID, generation, taskID, pageCount)
	if err != nil {
		return fmt.Errorf("failed to start OCR task: %w", err)
	}
	return requireOCRLeaseMutation(result)
}

func (r *FileRepository) MarkOCRSubmissionStarted(id, userID, generation int64) error {
	result, err := r.db.Exec(`
		UPDATE files
		SET ocr_error_type = 'ocr_submission_started'
		WHERE id = $1
		  AND user_id = $2
		  AND $3 > 0
		  AND ocr_lease_generation = $3
		  AND status = 'staged'
		  AND extract_status IN ('ocr_pending', 'ocr_running')
		  AND NULLIF(TRIM(ocr_task_id), '') IS NULL
		  AND ocr_error_type IS NULL
	`, id, userID, generation)
	if err != nil {
		return fmt.Errorf("mark OCR submission started: %w", err)
	}
	return requireOCRLeaseMutation(result)
}

func (r *FileRepository) FailStaleOCRSubmissions(now time.Time) (int64, error) {
	result, err := r.db.Exec(`
		UPDATE files
		SET extract_status = 'failed',
		    extract_error = 'OCR 任务提交结果未确认，请在服务恢复后手动重试',
		    ocr_error_type = 'ocr_submission_unknown',
		    ocr_completed_at = $1,
		    ocr_lease_until = NULL,
		    ocr_lease_generation = ocr_lease_generation + 1,
		    ocr_next_retry_at = NULL
		WHERE status = 'staged'
		  AND extract_status IN ('ocr_pending', 'ocr_running')
		  AND ocr_error_type = 'ocr_submission_started'
		  AND ocr_lease_until <= $1
	`, now)
	if err != nil {
		return 0, fmt.Errorf("fail stale OCR submissions: %w", err)
	}
	rows, _ := result.RowsAffected()
	return rows, nil
}

func (r *FileRepository) RecordOCRAttempt(id, userID, generation int64) (int, error) {
	var attempts int
	err := r.db.QueryRow(`
		UPDATE files
		SET ocr_attempts = ocr_attempts + 1
		WHERE id = $1 AND user_id = $2 AND $3 > 0 AND ocr_lease_generation = $3
		  AND status = 'staged' AND extract_status IN ('ocr_pending', 'ocr_running')
		RETURNING ocr_attempts
	`, id, userID, generation).Scan(&attempts)
	if err == sql.ErrNoRows {
		return 0, ErrOCRLeaseLost
	}
	if err != nil {
		return 0, fmt.Errorf("record OCR attempt: %w", err)
	}
	return attempts, nil
}

// CompleteOCRClaim serializes filesystem promotion with the database terminal
// transition. The row lock proves that the caller still owns generation before
// promotion; stale workers therefore never touch the shared final sidecar.
// Once promotion succeeds, callers must not compensate by deleting the final
// path because a commit error can be ambiguous to the client.
func (r *FileRepository) CompleteOCRClaim(ctx context.Context, id, userID, generation int64, extractedPath string, tokenEstimate int, promote func() error) error {
	if tokenEstimate < 0 {
		tokenEstimate = 0
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin OCR completion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var owned bool
	if err := tx.QueryRowContext(ctx, `
		SELECT status = 'staged'
		   AND extract_status IN ('ocr_pending', 'ocr_running')
		   AND $3 > 0
		   AND ocr_lease_generation = $3
		FROM files
		WHERE id = $1 AND user_id = $2
		FOR UPDATE
	`, id, userID, generation).Scan(&owned); err != nil {
		if err == sql.ErrNoRows {
			return ErrOCRLeaseLost
		}
		return fmt.Errorf("lock OCR completion: %w", err)
	}
	if !owned {
		return ErrOCRLeaseLost
	}
	if promote == nil {
		return errors.New("OCR sidecar promotion is required")
	}
	if err := promote(); err != nil {
		return fmt.Errorf("promote OCR sidecar: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE files
		SET extract_status = 'ready',
		    extract_error = NULL,
		    ocr_error_type = NULL,
		    extracted_text_path = $3,
		    file_path = $3,
		    token_estimate = $4,
		    ocr_progress_pages = GREATEST(ocr_progress_pages, ocr_page_count),
		    ocr_completed_at = NOW(),
		    ocr_lease_until = NULL,
		    ocr_next_retry_at = NULL
		WHERE id = $1
		  AND user_id = $2
		  AND ocr_lease_generation = $5
		  AND status = 'staged'
		  AND extract_status IN ('ocr_pending', 'ocr_running')
	`, id, userID, extractedPath, tokenEstimate, generation)
	if err != nil {
		return fmt.Errorf("update completed OCR file: %w", err)
	}
	if err := requireOCRLeaseMutation(result); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit OCR completion: %w", err)
	}
	return nil
}

func (r *FileRepository) FailOCR(id, userID int64, errorType, message string) error {
	_, err := r.db.Exec(`
		UPDATE files
		SET extract_status = 'failed',
		    extract_error = NULLIF($3, ''),
		    ocr_error_type = NULLIF($4, ''),
		    ocr_completed_at = NOW(),
		    ocr_lease_until = NULL,
		    ocr_lease_generation = ocr_lease_generation + 1,
		    ocr_next_retry_at = NULL
		WHERE id = $1
		  AND user_id = $2
		  AND status = 'staged'
		  AND extract_status IN ('ocr_pending', 'ocr_running')
	`, id, userID, message, errorType)
	if err != nil {
		return fmt.Errorf("failed to mark OCR file failed: %w", err)
	}
	return nil
}

func (r *FileRepository) FailOCRClaim(id, userID, generation int64, errorType, message string) error {
	result, err := r.db.Exec(`
		UPDATE files
		SET extract_status = 'failed',
		    extract_error = NULLIF($4, ''),
		    ocr_error_type = NULLIF($5, ''),
		    ocr_completed_at = NOW(),
		    ocr_lease_until = NULL,
		    ocr_next_retry_at = NULL
		WHERE id = $1
		  AND user_id = $2
		  AND $3 > 0
		  AND ocr_lease_generation = $3
		  AND status = 'staged'
		  AND extract_status IN ('ocr_pending', 'ocr_running')
	`, id, userID, generation, message, errorType)
	if err != nil {
		return fmt.Errorf("fail owned OCR claim: %w", err)
	}
	return requireOCRLeaseMutation(result)
}

func (r *FileRepository) ClearOCRSourcePath(id, userID int64, sourcePath string) error {
	_, err := r.db.Exec(`
		UPDATE files
		SET ocr_source_path = NULL
		WHERE id = $1
		  AND user_id = $2
		  AND status = 'staged'
		  AND (extract_status = 'ready' OR (extract_status = 'failed' AND ocr_error_type = 'ocr_source_expired'))
		  AND ocr_source_path = NULLIF($3, '')
	`, id, userID, sourcePath)
	if err != nil {
		return fmt.Errorf("clear OCR source path: %w", err)
	}
	return nil
}

func (r *FileRepository) ClearOCRSourcePathClaim(id, userID, generation int64, sourcePath string) error {
	result, err := r.db.Exec(`
		UPDATE files
		SET ocr_source_path = NULL
		WHERE id = $1
		  AND user_id = $2
		  AND $3 > 0
		  AND ocr_lease_generation = $3
		  AND status = 'staged'
		  AND extract_status = 'ready'
		  AND ocr_source_path = NULLIF($4, '')
	`, id, userID, generation, sourcePath)
	if err != nil {
		return fmt.Errorf("clear owned OCR source path: %w", err)
	}
	return requireOCRLeaseMutation(result)
}

func (r *FileRepository) ClaimRecoverableOCRTasks(provider string, now time.Time, lease time.Duration, maxConcurrency int) ([]*model.File, error) {
	if maxConcurrency <= 0 {
		return nil, nil
	}
	if lease <= 0 {
		lease = 2 * time.Minute
	}
	provider = strings.TrimSpace(provider)
	tx, err := r.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin OCR task claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext($1))`, "effchat:ocr:"+provider); err != nil {
		return nil, fmt.Errorf("lock OCR task claim: %w", err)
	}
	var active int
	if err := tx.QueryRow(`
		SELECT COUNT(*)
		FROM files
		WHERE status = 'staged'
		  AND ocr_provider = $1
		  AND extract_status IN ('ocr_pending', 'ocr_running')
		  AND ocr_lease_until > $2
	`, provider, now).Scan(&active); err != nil {
		return nil, fmt.Errorf("count active OCR leases: %w", err)
	}
	available := maxConcurrency - active
	if available <= 0 {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit OCR task claim: %w", err)
		}
		return nil, nil
	}
	rows, err := tx.Query(`
		WITH candidates AS (
			SELECT id
			FROM files
			WHERE status = 'staged'
			  AND ocr_provider = $1
			  AND extract_status IN ('ocr_pending', 'ocr_running')
			  AND COALESCE(ocr_error_type, '') <> 'ocr_submission_started'
			  AND COALESCE(ocr_next_retry_at, created_at) <= $2
			  AND (ocr_lease_until IS NULL OR ocr_lease_until <= $2)
			ORDER BY (ocr_task_id IS NULL), COALESCE(ocr_next_retry_at, created_at), id
				FOR UPDATE SKIP LOCKED
			LIMIT $4
		)
		UPDATE files f
		SET ocr_lease_until = $3,
		    ocr_lease_generation = f.ocr_lease_generation + 1
		FROM candidates c
		WHERE f.id = c.id
		RETURNING f.id, f.user_id, f.session_id, f.file_name, f.file_path, f.file_type, f.file_size, f.file_hash, f.status,
		          f.extracted_text_path, f.extract_status, f.extract_error, f.token_estimate,
		          f.ocr_provider, f.ocr_task_id, f.ocr_page_count, f.ocr_progress_pages, f.ocr_started_at, f.ocr_completed_at, f.ocr_error_type,
		          f.ocr_source_path, f.ocr_lease_until, f.ocr_lease_generation, f.ocr_attempts, f.ocr_next_retry_at, f.created_at
	`, provider, now, now.Add(lease), available)
	if err != nil {
		return nil, fmt.Errorf("claim OCR tasks: %w", err)
	}
	files, scanErr := scanOCRWorkRows(rows)
	rows.Close()
	if scanErr != nil {
		return nil, scanErr
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit OCR task claim: %w", err)
	}
	return files, nil
}

func (r *FileRepository) ReleaseOCRLease(id, userID, generation int64, retryAt time.Time) error {
	result, err := r.db.Exec(`
		UPDATE files
		SET ocr_lease_until = NULL,
		    ocr_error_type = NULL,
		    ocr_next_retry_at = $4
		WHERE id = $1
		  AND user_id = $2
		  AND $3 > 0
		  AND ocr_lease_generation = $3
		  AND status = 'staged'
		  AND extract_status IN ('ocr_pending', 'ocr_running')
	`, id, userID, generation, retryAt)
	if err != nil {
		return fmt.Errorf("release OCR lease: %w", err)
	}
	return requireOCRLeaseMutation(result)
}

func (r *FileRepository) RestartOCR(id, userID int64, now, sourceCutoff time.Time) (*model.File, error) {
	f, err := r.GetByID(id, userID)
	if err != nil {
		return nil, err
	}
	if f.ExtractStatus == "ocr_pending" || f.ExtractStatus == "ocr_running" {
		return f, nil
	}
	if f.ExtractStatus != "failed" || f.OCRSourcePath == nil || strings.TrimSpace(*f.OCRSourcePath) == "" || f.CreatedAt.Before(sourceCutoff) {
		return nil, ErrOCRSourceUnavailable
	}
	result, err := r.db.Exec(`
		UPDATE files
		SET extract_status = 'ocr_pending',
		    extract_error = NULL,
		    ocr_error_type = NULL,
		    ocr_task_id = NULL,
		    ocr_page_count = 0,
		    ocr_progress_pages = 0,
		    ocr_attempts = 0,
		    ocr_completed_at = NULL,
		    ocr_lease_until = NULL,
		    ocr_lease_generation = ocr_lease_generation + 1,
		    ocr_next_retry_at = $3
		WHERE id = $1
		  AND user_id = $2
		  AND status = 'staged'
		  AND extract_status = 'failed'
	`, id, userID, now)
	if err != nil {
		return nil, fmt.Errorf("restart OCR: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return nil, ErrOCRSourceUnavailable
	}
	return r.GetByID(id, userID)
}

func (r *FileRepository) ExpireStaleOCROriginals(cutoff, now time.Time, limit int) ([]*model.File, error) {
	if limit <= 0 {
		return nil, nil
	}
	tx, err := r.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin OCR source cleanup: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.Query(`
		SELECT files.id, files.user_id, files.session_id, files.file_name, files.file_path, files.file_type, files.file_size, files.file_hash, files.status,
		       files.extracted_text_path, files.extract_status, files.extract_error, files.token_estimate,
		       files.ocr_provider, files.ocr_task_id, files.ocr_page_count, files.ocr_progress_pages, files.ocr_started_at, files.ocr_completed_at, files.ocr_error_type,
		       files.ocr_source_path, files.ocr_lease_until, files.ocr_lease_generation, files.ocr_attempts, files.ocr_next_retry_at, files.created_at
		FROM files
		LEFT JOIN sessions owner_session ON owner_session.id = files.session_id
		WHERE files.status IN ('staged', 'formal')
		  AND files.ocr_source_path IS NOT NULL
		  AND (files.ocr_lease_until IS NULL OR files.ocr_lease_until <= $2)
		  AND (
		    (owner_session.deleted_at IS NOT NULL AND owner_session.deleted_at < $1)
		    OR (
		      files.status = 'staged'
		      AND
		      owner_session.deleted_at IS NULL
		      AND files.created_at < $1
		      AND NOT EXISTS (
		        SELECT 1
		        FROM messages m
		        JOIN sessions visible_session ON visible_session.id = m.session_id
		        WHERE visible_session.user_id = files.user_id
		          AND visible_session.deleted_at IS NULL
		          AND m.deleted_at IS NULL
		          AND EXISTS (
		            SELECT 1
		            FROM jsonb_array_elements(
		              CASE
		                WHEN jsonb_typeof(m.message_data->'attachments') = 'array'
		                THEN m.message_data->'attachments'
		                ELSE '[]'::jsonb
		              END
		            ) AS attachment(item)
		            WHERE attachment.item->>'file_id' ~ '^[0-9]+$'
		              AND (attachment.item->>'file_id')::bigint = files.id
		          )
		      )
		    )
		  )
		ORDER BY files.created_at, files.id
		FOR UPDATE OF files SKIP LOCKED
		LIMIT $3
	`, cutoff, now, limit)
	if err != nil {
		return nil, fmt.Errorf("list stale OCR sources: %w", err)
	}
	files, scanErr := scanOCRWorkRows(rows)
	rows.Close()
	if scanErr != nil {
		return nil, scanErr
	}
	for _, f := range files {
		if _, err := tx.Exec(`
			UPDATE files
			SET extract_status = CASE WHEN extract_status IN ('ocr_pending', 'ocr_running') THEN 'failed' ELSE extract_status END,
			    extract_error = CASE WHEN extract_status IN ('ocr_pending', 'ocr_running') THEN 'OCR 原文件已超过 24 小时暂存期，无法继续解析' ELSE extract_error END,
			    ocr_error_type = CASE WHEN extract_status IN ('ocr_pending', 'ocr_running') THEN 'ocr_source_expired' ELSE ocr_error_type END,
			    ocr_task_id = CASE WHEN extract_status IN ('ocr_pending', 'ocr_running') THEN NULL ELSE ocr_task_id END,
			    ocr_lease_until = NULL,
			    ocr_lease_generation = ocr_lease_generation + 1,
			    ocr_next_retry_at = NULL,
			    ocr_completed_at = CASE WHEN extract_status IN ('ocr_pending', 'ocr_running') THEN $2 ELSE ocr_completed_at END
			WHERE id = $1
		`, f.ID, now); err != nil {
			return nil, fmt.Errorf("expire OCR source %d: %w", f.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit OCR source cleanup: %w", err)
	}
	return files, nil
}

func scanOCRWorkRows(rows *sql.Rows) ([]*model.File, error) {
	var files []*model.File
	for rows.Next() {
		f := &model.File{}
		if err := rows.Scan(
			&f.ID, &f.UserID, &f.SessionID, &f.FileName, &f.FilePath, &f.FileType, &f.FileSize, &f.FileHash, &f.Status,
			&f.ExtractedTextPath, &f.ExtractStatus, &f.ExtractError, &f.TokenEstimate,
			&f.OCRProvider, &f.OCRTaskID, &f.OCRPageCount, &f.OCRProgressPages, &f.OCRStartedAt, &f.OCRCompletedAt, &f.OCRErrorType,
			&f.OCRSourcePath, &f.OCRLeaseUntil, &f.OCRLeaseGeneration, &f.OCRAttempts, &f.OCRNextRetryAt, &f.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan OCR work item: %w", err)
		}
		files = append(files, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate OCR work items: %w", err)
	}
	return files, nil
}

func requireOCRLeaseMutation(result sql.Result) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read OCR mutation result: %w", err)
	}
	if rows == 0 {
		return ErrOCRLeaseLost
	}
	return nil
}

func scanFileCleanupClaims(rows *sql.Rows) ([]FileCleanupClaim, error) {
	claims := make([]FileCleanupClaim, 0)
	for rows.Next() {
		file := &model.File{}
		var token string
		if err := rows.Scan(
			&file.ID, &file.UserID, &file.SessionID, &file.FileName, &file.FilePath, &file.FileType, &file.FileSize, &file.FileHash, &file.Status,
			&file.ExtractedTextPath, &file.ExtractStatus, &file.ExtractError, &file.TokenEstimate,
			&file.OCRProvider, &file.OCRTaskID, &file.OCRPageCount, &file.OCRProgressPages, &file.OCRStartedAt, &file.OCRCompletedAt, &file.OCRErrorType,
			&file.OCRSourcePath, &file.CreatedAt, &token,
		); err != nil {
			return nil, fmt.Errorf("scan storage cleanup claim: %w", err)
		}
		claims = append(claims, FileCleanupClaim{File: file, Token: token})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate storage cleanup claims: %w", err)
	}
	return claims, nil
}

func (r *FileRepository) ClaimFilesForStorageCleanup(ctx context.Context, cutoff, now time.Time, lease time.Duration, limit int) ([]FileCleanupClaim, error) {
	if now.IsZero() {
		now = time.Now()
	}
	if lease <= 0 {
		lease = 2 * time.Minute
	}
	if limit <= 0 {
		limit = 100
	}
	token := fmt.Sprintf("cleanup-%d-%d", now.UnixNano(), cleanupClaimSequence.Add(1))
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin storage cleanup claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `
		WITH candidates AS (
			SELECT f.id
			FROM files f
			LEFT JOIN sessions s ON s.id = f.session_id
			WHERE (
				f.status = 'staged'
				AND f.created_at < $1
				AND (f.ocr_lease_until IS NULL OR f.ocr_lease_until <= $2)
				AND f.extract_status NOT IN ('ocr_pending', 'ocr_running')
			) OR (
				f.status = 'formal'
				AND s.deleted_at IS NOT NULL
				AND s.deleted_at < $1
				AND (f.ocr_lease_until IS NULL OR f.ocr_lease_until <= $2)
				AND f.extract_status NOT IN ('ocr_pending', 'ocr_running')
			) OR (
				f.status = 'cleanup_claimed'
				AND COALESCE(f.cleanup_after, f.deleted_at, f.created_at) <= $2
				AND (f.cleanup_lease_until IS NULL OR f.cleanup_lease_until <= $2)
			)
			ORDER BY f.created_at ASC, f.id ASC
			FOR UPDATE OF f SKIP LOCKED
			LIMIT $4
		)
		UPDATE files f
		SET status = 'cleanup_claimed',
			cleanup_after = COALESCE(f.cleanup_after, $2),
			cleanup_claim_token = $3,
			cleanup_lease_until = $5,
			deleted_at = COALESCE(f.deleted_at, $2)
		FROM candidates c
		WHERE f.id = c.id
		RETURNING f.id, f.user_id, f.session_id, f.file_name, f.file_path, f.file_type, f.file_size, f.file_hash, f.status,
		          f.extracted_text_path, f.extract_status, f.extract_error, f.token_estimate,
		          f.ocr_provider, f.ocr_task_id, f.ocr_page_count, f.ocr_progress_pages, f.ocr_started_at, f.ocr_completed_at, f.ocr_error_type,
		          f.ocr_source_path, f.created_at, f.cleanup_claim_token
	`, cutoff, now, token, limit, now.Add(lease))
	if err != nil {
		return nil, fmt.Errorf("claim files for storage cleanup: %w", err)
	}
	claims, scanErr := scanFileCleanupClaims(rows)
	rows.Close()
	if scanErr != nil {
		return nil, scanErr
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit storage cleanup claim: %w", err)
	}
	return claims, nil
}

func (r *FileRepository) FinalizeFileStorageRemoval(ctx context.Context, id int64, token string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE files
		SET status = 'storage_removed',
			cleanup_claim_token = NULL,
			cleanup_lease_until = NULL,
			deleted_at = COALESCE(deleted_at, NOW())
		WHERE id = $1 AND status = 'cleanup_claimed' AND cleanup_claim_token = NULLIF($2, '')
	`, id, token)
	if err != nil {
		return fmt.Errorf("finalize file storage removal: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return fmt.Errorf("file cleanup claim is unavailable: %w", ErrAttachmentUnavailable)
	}
	return nil
}

func (r *FileRepository) ReleaseFileStorageCleanupClaim(ctx context.Context, id int64, token string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE files
		SET cleanup_lease_until = NULL
		WHERE id = $1 AND status = 'cleanup_claimed' AND cleanup_claim_token = NULLIF($2, '')
	`, id, token)
	if err != nil {
		return fmt.Errorf("release file storage cleanup claim: %w", err)
	}
	return nil
}

func (r *FileRepository) CountStaleReferencedFiles(cutoff time.Time) (int, error) {
	var count int
	err := r.db.QueryRow(`
		SELECT COUNT(*)
		FROM files f
		JOIN sessions s ON s.id = f.session_id
		WHERE f.status = 'formal'
		  AND f.created_at < $1
		  AND s.deleted_at IS NULL
	`, cutoff).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count referenced files: %w", err)
	}
	return count, nil
}

func (r *FileRepository) GetActiveFilesForSession(userID, sessionID int64, ids []int64) (map[int64]*model.File, error) {
	return r.GetActiveFilesForSessionContext(context.Background(), userID, sessionID, ids)
}

func (r *FileRepository) GetActiveFilesForSessionContext(ctx context.Context, userID, sessionID int64, ids []int64) (map[int64]*model.File, error) {
	return r.getFilesForSessionByStatuses(ctx, userID, sessionID, ids, []string{FileStatusStaged, FileStatusFormal})
}

func (r *FileRepository) GetStagedFilesForSessionContext(ctx context.Context, userID, sessionID int64, ids []int64) (map[int64]*model.File, error) {
	return r.getFilesForSessionByStatuses(ctx, userID, sessionID, ids, []string{FileStatusStaged})
}

func (r *FileRepository) GetFormalFilesForSessionContext(ctx context.Context, userID, sessionID int64, ids []int64) (map[int64]*model.File, error) {
	return r.getFilesForSessionByStatuses(ctx, userID, sessionID, ids, []string{FileStatusFormal})
}

func (r *FileRepository) getFilesForSessionByStatuses(ctx context.Context, userID, sessionID int64, ids []int64, statuses []string) (map[int64]*model.File, error) {
	out := make(map[int64]*model.File, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, user_id, session_id, file_name, file_path, file_type, file_size, file_hash, status,
		       extracted_text_path, extract_status, extract_error, token_estimate,
		       ocr_provider, ocr_task_id, ocr_page_count, ocr_progress_pages, ocr_started_at, ocr_completed_at, ocr_error_type, ocr_source_path,
		       created_at
		FROM files
		WHERE user_id = $1 AND session_id = $2 AND status = ANY($3) AND id = ANY($4)
	`, userID, sessionID, pq.Array(statuses), pq.Array(ids))
	if err != nil {
		return nil, fmt.Errorf("failed to query session files: %w", err)
	}
	defer rows.Close()
	files, err := scanFileRows(rows)
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		out[file.ID] = file
	}
	return out, nil
}

// GetReadableFileForAgent 返回当前 agent run 允许读取的单个文件。
//
// 权限口径故意比“文件属于用户”更窄：文件必须属于当前会话且已进入正式消息。
// 消息里的历史 attachments 只作为展示记录，不能放宽文件归属边界，否则旧的错误
// 引用会让模型跨会话读取文件。
func (r *FileRepository) GetReadableFileForAgent(userID, sessionID, fileID int64) (*model.File, error) {
	f := &model.File{}
	query := `
		SELECT f.id, f.user_id, f.session_id, f.file_name, f.file_path, f.file_type, f.file_size, f.file_hash, f.status,
		       f.extracted_text_path, f.extract_status, f.extract_error, f.token_estimate,
		       f.ocr_provider, f.ocr_task_id, f.ocr_page_count, f.ocr_progress_pages, f.ocr_started_at, f.ocr_completed_at, f.ocr_error_type, f.ocr_source_path,
		       f.created_at
		FROM files f
		WHERE f.id = $3
		  AND f.user_id = $1
	  AND f.status = 'formal'
		  AND f.session_id = $2
	`
	err := r.db.QueryRow(query, userID, sessionID, fileID).Scan(
		&f.ID, &f.UserID, &f.SessionID, &f.FileName, &f.FilePath, &f.FileType, &f.FileSize, &f.FileHash, &f.Status,
		&f.ExtractedTextPath, &f.ExtractStatus, &f.ExtractError, &f.TokenEstimate,
		&f.OCRProvider, &f.OCRTaskID, &f.OCRPageCount, &f.OCRProgressPages, &f.OCRStartedAt, &f.OCRCompletedAt, &f.OCRErrorType, &f.OCRSourcePath,
		&f.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("file not readable: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get readable file: %w", err)
	}
	return f, nil
}

func (r *FileRepository) ListReadableFilesForAgent(userID, sessionID int64) ([]*model.File, error) {
	query := `
		SELECT DISTINCT f.id, f.user_id, f.session_id, f.file_name, f.file_path, f.file_type, f.file_size, f.file_hash, f.status,
		       f.extracted_text_path, f.extract_status, f.extract_error, f.token_estimate,
		       f.ocr_provider, f.ocr_task_id, f.ocr_page_count, f.ocr_progress_pages, f.ocr_started_at, f.ocr_completed_at, f.ocr_error_type, f.ocr_source_path,
		       f.created_at
		FROM files f
		WHERE f.user_id = $1
	  AND f.status = 'formal'
		  AND f.session_id = $2
		ORDER BY f.created_at DESC, f.id DESC
	`
	rows, err := r.db.Query(query, userID, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to list readable files: %w", err)
	}
	defer rows.Close()
	return scanFileRows(rows)
}

func scanFileRows(rows *sql.Rows) ([]*model.File, error) {
	var files []*model.File
	for rows.Next() {
		f := &model.File{}
		if err := rows.Scan(
			&f.ID, &f.UserID, &f.SessionID, &f.FileName, &f.FilePath, &f.FileType, &f.FileSize, &f.FileHash, &f.Status,
			&f.ExtractedTextPath, &f.ExtractStatus, &f.ExtractError, &f.TokenEstimate,
			&f.OCRProvider, &f.OCRTaskID, &f.OCRPageCount, &f.OCRProgressPages, &f.OCRStartedAt, &f.OCRCompletedAt, &f.OCRErrorType, &f.OCRSourcePath,
			&f.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan file: %w", err)
		}
		files = append(files, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate files: %w", err)
	}
	return files, nil
}
