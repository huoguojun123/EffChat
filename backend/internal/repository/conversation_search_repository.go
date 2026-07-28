package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type ConversationSearchScope string

const (
	ConversationSearchAll     ConversationSearchScope = "all"
	ConversationSearchUnfiled ConversationSearchScope = "unfiled"
	ConversationSearchFolder  ConversationSearchScope = "folder"
)

type ConversationSearchResult struct {
	Kind         string    `json:"kind"`
	SessionID    int64     `json:"session_id"`
	SessionTitle string    `json:"session_title"`
	FolderID     *int64    `json:"folder_id,omitempty"`
	MessageID    *int64    `json:"message_id,omitempty"`
	TurnID       *int64    `json:"turn_id,omitempty"`
	Role         string    `json:"role,omitempty"`
	Snippet      string    `json:"snippet"`
	CreatedAt    time.Time `json:"created_at"`
}

type ConversationSearchRepository struct{ db *sql.DB }

func NewConversationSearchRepository(db *sql.DB) *ConversationSearchRepository {
	return &ConversationSearchRepository{db: db}
}

func (r *ConversationSearchRepository) Search(ctx context.Context, userID int64, query string, scope ConversationSearchScope, folderID *int64, limit int) ([]ConversationSearchResult, error) {
	rows, err := r.db.QueryContext(ctx, `
		WITH scoped_sessions AS NOT MATERIALIZED (
			SELECT s.id, s.title, s.folder_id, s.updated_at
			FROM sessions s
			WHERE s.user_id = $1
			  AND s.deleted_at IS NULL
			  AND (
				$3::TEXT = 'all'
				OR ($3::TEXT = 'unfiled' AND s.folder_id IS NULL)
				OR ($3::TEXT = 'folder' AND s.folder_id = $4::BIGINT)
			  )
		), title_hits AS (
			SELECT 'session'::TEXT AS kind, s.id AS session_id, s.title AS session_title,
			       s.folder_id, NULL::BIGINT AS message_id, NULL::BIGINT AS turn_id,
			       ''::TEXT AS role, s.title AS snippet, s.updated_at AS created_at,
			       CASE
			         WHEN lower(s.title) = lower($2) THEN 400
			         WHEN lower(s.title) LIKE lower($2) || '%' THEN 350
			         WHEN lower(s.title) LIKE '%' || lower($2) || '%' THEN 300
			         ELSE 200 + similarity(lower(s.title), lower($2)) * 100
			       END AS score,
			       0 AS message_rank
			FROM scoped_sessions s
			WHERE lower(s.title) LIKE '%' || lower($2) || '%'
			   OR lower(s.title) % lower($2)
		), message_hits AS (
			SELECT 'message'::TEXT AS kind, s.id AS session_id, s.title AS session_title,
			       s.folder_id, m.id AS message_id,
			       COALESCE(
			         a.user_message_id,
			         CASE WHEN m.role = 'user' THEN m.id ELSE (
			           SELECT u.id FROM messages u
			           WHERE u.session_id = m.session_id AND u.deleted_at IS NULL
			             AND u.role = 'user' AND u.id < m.id
			           ORDER BY u.id DESC LIMIT 1
			         ) END
			       ) AS turn_id,
			       m.role, CASE
			         WHEN position(lower($2) in lower(m.message_data->>'content')) > 0 THEN
			           substring(m.message_data->>'content' FROM greatest(1, position(lower($2) in lower(m.message_data->>'content')) - 70) FOR 220)
			         ELSE left(m.message_data->>'content', 220)
			       END AS snippet,
			       m.created_at,
			       100 + CASE WHEN position(lower($2) in lower(m.message_data->>'content')) > 0 THEN 50 ELSE 0 END AS score,
			       row_number() OVER (PARTITION BY s.id ORDER BY m.created_at DESC, m.id DESC) AS message_rank
			FROM scoped_sessions s
			JOIN messages m ON m.session_id = s.id
			LEFT JOIN answer_attempts a ON a.id = m.answer_attempt_id
			WHERE m.deleted_at IS NULL
			  AND m.role IN ('user', 'assistant')
			  AND (m.answer_attempt_id IS NULL OR a.selected)
			  AND COALESCE(m.message_data->'metadata'->>'compaction_summary', '') <> 'true'
			  AND lower(COALESCE(m.message_data->>'content', '')) LIKE '%' || lower($2) || '%'
		), combined AS (
			SELECT * FROM title_hits
			UNION ALL
			SELECT * FROM message_hits WHERE message_rank <= 3
		)
		SELECT kind, session_id, session_title, folder_id, message_id, turn_id, role, snippet, created_at
		FROM combined
		ORDER BY score DESC, created_at DESC, session_id DESC, message_rank ASC
		LIMIT $5
	`, userID, query, string(scope), folderID, limit)
	if err != nil {
		return nil, fmt.Errorf("search conversations: %w", err)
	}
	defer rows.Close()

	results := make([]ConversationSearchResult, 0)
	for rows.Next() {
		var item ConversationSearchResult
		if err := rows.Scan(&item.Kind, &item.SessionID, &item.SessionTitle, &item.FolderID, &item.MessageID, &item.TurnID, &item.Role, &item.Snippet, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan conversation search result: %w", err)
		}
		results = append(results, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate conversation search results: %w", err)
	}
	return results, nil
}
