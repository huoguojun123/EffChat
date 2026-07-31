package repository

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/huoguojun123/EffChat/internal/model"
)

func TestCheckToolQuotaCountLimits(t *testing.T) {
	reset := time.Now().Add(time.Hour)
	cases := []struct {
		name     string
		limits   UserQuotaLimits
		usage    QuotaUsage
		toolName string
		wantCode string
	}{
		{
			name:     "tool total limit blocks any tool",
			limits:   UserQuotaLimits{DailyToolCallLimit: 2},
			usage:    QuotaUsage{DailyToolCalls: 2, ResetAt: reset},
			toolName: "file_read",
			wantCode: "daily_tool_call_limit_exceeded",
		},
		{
			name:     "search limit blocks only web_search",
			limits:   UserQuotaLimits{DailyWebSearchLimit: 1},
			usage:    QuotaUsage{DailyWebSearches: 1, ResetAt: reset},
			toolName: "web_search",
			wantCode: "daily_web_search_limit_exceeded",
		},
		{
			name:     "extract limit blocks only web_extract",
			limits:   UserQuotaLimits{DailyWebExtractLimit: 1},
			usage:    QuotaUsage{DailyWebExtracts: 1, ResetAt: reset},
			toolName: "web_extract",
			wantCode: "daily_web_extract_limit_exceeded",
		},
		{
			name:     "under limit allows call",
			limits:   UserQuotaLimits{DailyToolCallLimit: 3, DailyWebSearchLimit: 2},
			usage:    QuotaUsage{DailyToolCalls: 2, DailyWebSearches: 1, ResetAt: reset},
			toolName: "web_search",
		},
		{
			name:     "zero means unlimited",
			limits:   UserQuotaLimits{},
			usage:    QuotaUsage{DailyToolCalls: 999, DailyWebSearches: 999, DailyWebExtracts: 999, ResetAt: reset},
			toolName: "web_extract",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkToolQuota(tc.limits, tc.usage, tc.toolName)
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("expected allowed call, got %v", err)
				}
				return
			}
			var quotaErr *ToolQuotaExceeded
			if !errors.As(err, &quotaErr) {
				t.Fatalf("expected ToolQuotaExceeded, got %v", err)
			}
			if quotaErr.Code != tc.wantCode || quotaErr.ResetAt.IsZero() {
				t.Fatalf("unexpected quota error: %#v", quotaErr)
			}
		})
	}
}

func TestQuotaRepository_UsageForTodayCountsOCRByStartTime(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	user := &model.User{
		Username:     fmt.Sprintf("ocr_quota_%d", time.Now().UnixNano()),
		PasswordHash: "x",
		Role:         "user",
		IsActive:     true,
		Permissions:  []byte(`{}`),
		Preferences:  []byte(`{}`),
	}
	if err := NewUserRepository(db).Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM files WHERE user_id = $1", user.ID)
		_, _ = db.Exec("DELETE FROM users WHERE id = $1", user.ID)
	})

	provider := "mineru"
	file := &model.File{
		UserID:        user.ID,
		FileName:      "overnight.pdf",
		FilePath:      fmt.Sprintf("/tmp/overnight_%d.pdf", time.Now().UnixNano()),
		FileType:      "application/pdf",
		FileSize:      64,
		ExtractStatus: "ocr_pending",
		OCRProvider:   &provider,
	}
	if err := NewFileRepository(db).Create(file); err != nil {
		t.Fatalf("create file: %v", err)
	}
	if _, err := db.Exec(`
		UPDATE files
		SET created_at = NOW() - INTERVAL '1 day',
		    ocr_started_at = NOW(),
		    ocr_page_count = 7
		WHERE id = $1
	`, file.ID); err != nil {
		t.Fatalf("set OCR timestamps: %v", err)
	}

	usage, err := NewQuotaRepository(db).UsageForToday(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("load usage: %v", err)
	}
	if usage.DailyOCRFiles != 1 || usage.DailyOCRPages != 7 {
		t.Fatalf("OCR usage = files:%d pages:%d, want files:1 pages:7", usage.DailyOCRFiles, usage.DailyOCRPages)
	}
}

func TestQuotaRepositoryReserveChatRunEnforcesDailyMessageAndConcurrentLimits(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	userID := createRepositoryTestUser(t, db, "chat_reservation")
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM users WHERE id = $1", userID) })
	sessionRepo := NewSessionRepository(db)
	first := &model.Session{UserID: userID, Title: "first", ModelID: "m", Provider: "p", MessageFormat: "v1", Metadata: []byte(`{}`)}
	second := &model.Session{UserID: userID, Title: "second", ModelID: "m", Provider: "p", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := sessionRepo.Create(first); err != nil {
		t.Fatal(err)
	}
	if err := sessionRepo.Create(second); err != nil {
		t.Fatal(err)
	}
	groupName := fmt.Sprintf("quota_reservation_group_%d", time.Now().UnixNano())
	if _, err := db.Exec(`
		INSERT INTO user_groups (name, level, daily_message_limit, concurrent_run_limit, is_default)
		VALUES ($1, 99, 1, 1, false)
	`, groupName); err != nil {
		t.Fatal(err)
	}
	var groupID int64
	if err := db.QueryRow(`SELECT id FROM user_groups WHERE name = $1`, groupName).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("UPDATE users SET group_id = NULL WHERE group_id = $1", groupID)
		_, _ = db.Exec("DELETE FROM user_groups WHERE id = $1", groupID)
	})
	if _, err := db.Exec(`UPDATE users SET group_id = $1 WHERE id = $2`, groupID, userID); err != nil {
		t.Fatal(err)
	}

	repo := NewQuotaRepository(db)
	firstInput := ChatRunReservationInput{UserID: userID, AuthVersion: 1, SessionID: first.ID, RunID: "quota-run-1", ReserveMessage: true, ExpiresAt: time.Now().Add(time.Minute)}
	if _, err := repo.AdmitChatMessage(context.Background(), firstInput, &model.Message{
		SessionID: first.ID, SchemaVersion: "v1",
		MessageData: []byte(`{"role":"user","content":"reserved message","metadata":{"run_id":"quota-run-1"}}`),
	}); err != nil {
		t.Fatalf("admit first run: %v", err)
	}
	// expires_at is replay-retention metadata, not a running lease. A model
	// stream that has already started may legitimately outlive the original
	// estimate and must keep owning its concurrency slot until terminal state.
	if _, err := db.Exec(`
		UPDATE chat_run_reservations
		SET expires_at = NOW() - INTERVAL '1 second'
		WHERE run_id = $1
	`, firstInput.RunID); err != nil {
		t.Fatalf("age active reservation: %v", err)
	}
	if _, err := repo.ReserveChatRun(context.Background(), ChatRunReservationInput{UserID: userID, AuthVersion: 1, SessionID: second.ID, RunID: "quota-run-2", ReserveMessage: true, ExpiresAt: time.Now().Add(time.Minute)}); err == nil {
		t.Fatal("concurrent reservation should be rejected")
	} else if quotaErr := new(ToolQuotaExceeded); !errors.As(err, &quotaErr) || quotaErr.Code != "concurrent_run_limit_exceeded" {
		t.Fatalf("concurrent reserve error = %v", err)
	}
	if _, transitioned, err := repo.TransitionChatRun(context.Background(), ChatRunTransitionInput{
		RunID: firstInput.RunID, Status: "completed", ExpiresAt: time.Now().Add(time.Minute),
	}); err != nil || !transitioned {
		t.Fatalf("complete first run: transitioned=%v err=%v", transitioned, err)
	}
	if _, err := repo.ReserveChatRun(context.Background(), ChatRunReservationInput{UserID: userID, AuthVersion: 1, SessionID: second.ID, RunID: "quota-run-3", ReserveMessage: true, ExpiresAt: time.Now().Add(time.Minute)}); err == nil {
		t.Fatal("daily message reservation should remain consumed after run release")
	} else if quotaErr := new(ToolQuotaExceeded); !errors.As(err, &quotaErr) || quotaErr.Code != "daily_message_limit_exceeded" {
		t.Fatalf("daily message reserve error = %v", err)
	}
}

func TestQuotaRepositoryReserveOCRSubmissionAtomicallyMarksQuotaUsage(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	userID := createRepositoryTestUser(t, db, "ocr_reservation")
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM users WHERE id = $1", userID) })
	groupName := fmt.Sprintf("ocr_reservation_group_%d", time.Now().UnixNano())
	if _, err := db.Exec(`
		INSERT INTO user_groups (name, level, daily_ocr_file_limit, daily_ocr_page_limit, is_default)
		VALUES ($1, 98, 1, 3, false)
	`, groupName); err != nil {
		t.Fatal(err)
	}
	var groupID int64
	if err := db.QueryRow(`SELECT id FROM user_groups WHERE name = $1`, groupName).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("UPDATE users SET group_id = NULL WHERE group_id = $1", groupID)
		_, _ = db.Exec("DELETE FROM user_groups WHERE id = $1", groupID)
	})
	if _, err := db.Exec(`UPDATE users SET group_id = $1 WHERE id = $2`, groupID, userID); err != nil {
		t.Fatal(err)
	}
	file := &model.File{UserID: userID, FileName: "test.pdf", FilePath: fmt.Sprintf("/tmp/ocr_reserve_%d.pdf", time.Now().UnixNano()), FileType: "application/pdf", FileSize: 10, ExtractStatus: "ocr_pending"}
	if err := NewFileRepository(db).Create(file); err != nil {
		t.Fatal(err)
	}
	repo := NewQuotaRepository(db)
	reserved, err := repo.ReserveOCRSubmission(context.Background(), file.ID, userID, 3)
	if err != nil || !reserved {
		t.Fatalf("reserve OCR = %v, %v", reserved, err)
	}
	if _, err := db.Exec("UPDATE files SET ocr_provider = 'mineru', ocr_started_at = NOW() WHERE id = $1", file.ID); err != nil {
		t.Fatalf("mark OCR started: %v", err)
	}
	reserved, err = repo.ReserveOCRSubmission(context.Background(), file.ID, userID, 3)
	if err != nil || reserved {
		t.Fatalf("duplicate OCR reserve = %v, %v", reserved, err)
	}
	usage, err := repo.UsageForToday(context.Background(), userID)
	if err != nil || usage.DailyOCRFiles != 1 || usage.DailyOCRPages != 3 {
		t.Fatalf("OCR usage = %+v, err=%v", usage, err)
	}
}

func TestQuotaRepositoryReserveOCRSubmissionCountsByStartTime(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	createUserWithLimit := func(name string) int64 {
		userID := createRepositoryTestUser(t, db, name)
		groupName := fmt.Sprintf("%s_group_%d", name, time.Now().UnixNano())
		var groupID int64
		if err := db.QueryRow(`
			INSERT INTO user_groups (name, level, daily_ocr_file_limit, daily_ocr_page_limit, is_default)
			VALUES ($1, 97, 1, 10, false)
			RETURNING id
		`, groupName).Scan(&groupID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE users SET group_id = $1 WHERE id = $2`, groupID, userID); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = db.Exec("DELETE FROM files WHERE user_id = $1", userID)
			_, _ = db.Exec("DELETE FROM users WHERE id = $1", userID)
			_, _ = db.Exec("DELETE FROM user_groups WHERE id = $1", groupID)
		})
		return userID
	}
	createPendingFile := func(userID int64, name string) *model.File {
		file := &model.File{
			UserID: userID, FileName: name, FilePath: fmt.Sprintf("/tmp/%s_%d.pdf", name, time.Now().UnixNano()),
			FileType: "application/pdf", FileSize: 10, ExtractStatus: "ocr_pending",
		}
		if err := NewFileRepository(db).Create(file); err != nil {
			t.Fatal(err)
		}
		return file
	}

	t.Run("pending upload does not consume quota before OCR starts", func(t *testing.T) {
		userID := createUserWithLimit("ocr_pending_not_counted")
		queued := createPendingFile(userID, "queued.pdf")
		candidate := createPendingFile(userID, "candidate.pdf")
		if _, err := db.Exec(`UPDATE files SET ocr_provider = 'mineru', ocr_page_count = 8 WHERE id = $1`, queued.ID); err != nil {
			t.Fatal(err)
		}

		reserved, err := NewQuotaRepository(db).ReserveOCRSubmission(context.Background(), candidate.ID, userID, 3)
		if err != nil || !reserved {
			t.Fatalf("reserve candidate = %v, %v; pending upload must not consume today's OCR quota", reserved, err)
		}
	})

	t.Run("overnight upload counts on the day OCR starts", func(t *testing.T) {
		userID := createUserWithLimit("ocr_overnight_counted")
		started := createPendingFile(userID, "started.pdf")
		candidate := createPendingFile(userID, "next.pdf")
		if _, err := db.Exec(`
			UPDATE files
			SET created_at = NOW() - INTERVAL '1 day', ocr_provider = 'mineru', ocr_started_at = NOW(), ocr_page_count = 4
			WHERE id = $1
		`, started.ID); err != nil {
			t.Fatal(err)
		}

		reserved, err := NewQuotaRepository(db).ReserveOCRSubmission(context.Background(), candidate.ID, userID, 1)
		var quotaErr *ToolQuotaExceeded
		if reserved || !errors.As(err, &quotaErr) || quotaErr.Code != "daily_ocr_file_limit_exceeded" {
			t.Fatalf("reserve candidate = %v, %v; today's started OCR must consume the file quota", reserved, err)
		}
	})
}
