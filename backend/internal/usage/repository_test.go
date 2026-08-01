package usage

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/huoguojun123/EffChat/internal/testutil"
)

func TestRepositoryAggregateSeparatesRunsAttemptsAndDegradedTools(t *testing.T) {
	db := testutil.OpenPostgresTestDB(t)
	repo := NewRepository(db)
	ctx := context.Background()

	var userID int64
	if err := db.QueryRowContext(ctx, `
		INSERT INTO users (username, password_hash, role, is_active, permissions, preferences)
		VALUES ($1, 'hash', 'admin', true, '{}', '{}')
		RETURNING id
	`, fmt.Sprintf("usage_%d", time.Now().UnixNano())).Scan(&userID); err != nil {
		t.Fatalf("insert usage user: %v", err)
	}
	var sessionID int64
	if err := db.QueryRowContext(ctx, `
		INSERT INTO sessions (user_id, title, model_id, provider)
		VALUES ($1, 'Usage aggregation', 'model-a', 'provider-a')
		RETURNING id
	`, userID).Scan(&sessionID); err != nil {
		t.Fatalf("insert usage session: %v", err)
	}

	for _, event := range []Event{
		{UserID: userID, SessionID: sessionID, RunID: "run-completed", Kind: KindChat, Provider: "provider-a", ModelID: "model-a", Success: true, TotalTokens: 100},
		{UserID: userID, SessionID: sessionID, RunID: "run-failed", Kind: KindChat, Provider: "provider-a", ModelID: "model-a", ErrorType: "model_error"},
		{UserID: userID, SessionID: sessionID, RunID: "run-stopped", Kind: KindChat, Provider: "provider-a", ModelID: "model-a", ErrorType: "canceled"},
	} {
		event := event
		if err := repo.Create(ctx, &event); err != nil {
			t.Fatalf("insert model usage event: %v", err)
		}
	}

	for _, event := range []ToolEvent{
		{UserID: userID, SessionID: sessionID, RunID: "run-completed", ToolKey: "web_search", Success: true},
		{UserID: userID, SessionID: sessionID, RunID: "run-completed", ToolKey: "web_extract", ErrorType: "degraded_refinement_cooldown"},
		{UserID: userID, SessionID: sessionID, RunID: "run-completed", ToolKey: "web_extract", ErrorType: "degraded_source_truncated"},
		{UserID: userID, SessionID: sessionID, RunID: "run-completed", ToolKey: "web_extract", ErrorType: "refinement_failed", ErrorMessage: "tool returned a degraded result"},
		{UserID: userID, SessionID: sessionID, RunID: "run-completed", ToolKey: "web_extract", ErrorType: "refinement_disabled", ErrorMessage: "tool returned a degraded result"},
		{UserID: userID, SessionID: sessionID, RunID: "run-completed", ToolKey: "web_extract", ErrorType: "refinement_unavailable", ErrorMessage: "tool returned a degraded result"},
		{UserID: userID, SessionID: sessionID, RunID: "run-failed", ToolKey: "web_extract", ErrorType: "refinement_failed"},
		{UserID: userID, SessionID: sessionID, RunID: "run-failed", ToolKey: "web_extract", ErrorType: "blocked"},
	} {
		event := event
		if err := repo.CreateToolEvent(ctx, &event); err != nil {
			t.Fatalf("insert tool usage event: %v", err)
		}
	}

	now := time.Now()
	for _, run := range []struct {
		id          string
		kind        string
		operation   string
		status      string
		cancelCause string
		terminalAt  *time.Time
	}{
		{id: "run-completed", kind: "chat", operation: "send", status: "completed", terminalAt: timePointer(now.Add(-4 * time.Minute))},
		{id: "run-failed", kind: "chat", operation: "send", status: "failed", terminalAt: timePointer(now.Add(-3 * time.Minute))},
		{id: "run-stopped", kind: "chat", operation: "send", status: "canceled", cancelCause: "user_stop", terminalAt: timePointer(now.Add(-2 * time.Minute))},
		{id: "run-drained", kind: "chat", operation: "send", status: "canceled", cancelCause: "server_draining", terminalAt: timePointer(now.Add(-time.Minute))},
		{id: "run-active", kind: "chat", operation: "send", status: "running"},
		{id: "run-compaction", kind: "compaction", operation: "compaction", status: "failed", terminalAt: timePointer(now)},
	} {
		acceptedAt := now.Add(-5 * time.Minute)
		if _, err := db.ExecContext(ctx, `
			INSERT INTO chat_run_reservations (
				run_id, user_id, session_id, kind, operation, status, cancel_cause,
				accepted_at, terminal_at, expires_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		`, run.id, userID, sessionID, run.kind, run.operation, run.status, run.cancelCause, acceptedAt, run.terminalAt, now.Add(time.Hour)); err != nil {
			t.Fatalf("insert chat run %s: %v", run.id, err)
		}
	}

	summary, err := repo.Aggregate(ctx, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("aggregate model and run usage: %v", err)
	}
	toolTotals, byTool, err := repo.AggregateToolUsage(ctx, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("aggregate tool usage: %v", err)
	}

	if summary.Totals.Requests != 3 || summary.Totals.Successes != 1 || summary.Totals.Failures != 1 || summary.Totals.Canceled != 1 {
		t.Fatalf("model attempt totals = %#v", summary.Totals)
	}
	if summary.RunTotals.Runs != 5 || summary.RunTotals.Running != 1 || summary.RunTotals.Completed != 1 || summary.RunTotals.Failed != 1 || summary.RunTotals.Canceled != 2 {
		t.Fatalf("chat run totals = %#v", summary.RunTotals)
	}
	if summary.RunTotals.UserStopped != 1 || summary.RunTotals.SystemCanceled != 1 {
		t.Fatalf("chat run cancellation totals = %#v", summary.RunTotals)
	}
	if toolTotals.Calls != 8 || toolTotals.Successes != 1 || toolTotals.Failures != 2 || toolTotals.Degraded != 5 {
		t.Fatalf("tool totals = %#v", toolTotals)
	}
	for _, item := range byTool {
		if item.ToolKey == "web_extract" && (item.Calls != 7 || item.Failures != 2 || item.Degraded != 5) {
			t.Fatalf("web extract totals = %#v", item)
		}
	}
}

func TestRepositoryQuotaUsersIgnoreCompactionCheckpoints(t *testing.T) {
	db := testutil.OpenPostgresTestDB(t)
	repo := NewRepository(db)
	ctx := context.Background()

	var userID int64
	if err := db.QueryRowContext(ctx, `
		INSERT INTO users (username, password_hash, role, is_active, permissions, preferences)
		VALUES ($1, 'hash', 'user', true, '{}', '{}')
		RETURNING id
	`, fmt.Sprintf("usage_compaction_%d", time.Now().UnixNano())).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	var sessionID int64
	if err := db.QueryRowContext(ctx, `
		INSERT INTO sessions (user_id, title, model_id, provider)
		VALUES ($1, 'Compaction quota', 'model-a', 'provider-a')
		RETURNING id
	`, userID).Scan(&sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO messages (session_id, schema_version, message_data)
		VALUES
			($1, 'v1', '{"role":"user","content":"actual user message"}'),
			($1, 'v1', '{"role":"user","content":"checkpoint","metadata":{"compaction_summary":true}}')
	`, sessionID); err != nil {
		t.Fatal(err)
	}

	users, err := repo.QuotaUsersForToday(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range users {
		if user.UserID == userID {
			if user.DailyMessages != 1 {
				t.Fatalf("admin daily messages = %d, want only the actual user message", user.DailyMessages)
			}
			return
		}
	}
	t.Fatalf("quota user %d was not returned", userID)
}

func timePointer(value time.Time) *time.Time {
	return &value
}
