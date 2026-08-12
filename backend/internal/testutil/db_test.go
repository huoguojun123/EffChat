package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
)

func TestValidateTestDSNRejectsNonTestDatabase(t *testing.T) {
	if err := validateTestDSN("postgres://tester@localhost/effchat"); err == nil {
		t.Fatal("production-like database name should be rejected")
	}
	if err := validateTestDSN("postgres://tester@localhost/effchat_test?sslmode=disable"); err != nil {
		t.Fatalf("test database should be accepted: %v", err)
	}
}

func TestPostgresDatabaseName(t *testing.T) {
	if got := postgresDatabaseName("postgres://user:pass@localhost:5432/effchat_test?sslmode=disable"); got != "effchat_test" {
		t.Fatalf("URL database = %q", got)
	}
	if got := postgresDatabaseName("host=localhost dbname=effchat_test sslmode=disable"); got != "" {
		t.Fatalf("keyword database = %q, want empty", got)
	}
}

func TestValidateTestDSNRejectsTargetOverrides(t *testing.T) {
	for _, dsn := range []string{
		"postgres://tester@localhost/effchat_test?dbname=effchat",
		"postgres://tester@localhost/effchat_test?database=effchat",
		"postgres://tester@localhost/effchat_test?host=production.example",
		"postgres://tester@localhost/effchat_test?user=production",
		"host=localhost dbname=effchat_test sslmode=disable",
	} {
		if err := validateTestDSN(dsn); err == nil {
			t.Fatalf("unsafe test DSN was accepted: %q", dsn)
		}
	}
}

func TestWithPostgresDatabase(t *testing.T) {
	for _, tc := range []struct {
		name string
		dsn  string
	}{
		{name: "url", dsn: "postgres://user:pass@localhost:5432/effchat_test?sslmode=disable"},
		{name: "postgresql url", dsn: "postgresql://user:pass@localhost:5432/effchat_test?sslmode=disable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := withPostgresDatabase(tc.dsn, "postgres")
			if err != nil {
				t.Fatalf("rewrite dsn: %v", err)
			}
			if name := postgresDatabaseName(got); name != "postgres" {
				t.Fatalf("database name = %q, want postgres; dsn=%q", name, got)
			}
		})
	}
}

func TestNextIsolationDatabaseNameIsUniqueAndParseable(t *testing.T) {
	first, err := nextIsolationDatabaseName()
	if err != nil {
		t.Fatal(err)
	}
	second, err := nextIsolationDatabaseName()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("isolation database names collided: %q", first)
	}
	if _, ok := isolationDatabaseCreatedAt(first); !ok {
		t.Fatalf("isolation database name is not parseable: %q", first)
	}
}

func TestOpenPostgresTestDBIsolatesAndCleansUp(t *testing.T) {
	baseDSN := postgresTestDSN()
	if baseDSN == "" {
		t.Skip("set EFFCHAT_TEST_DATABASE_DSN to run PostgreSQL integration tests")
	}
	if err := validateTestDSN(baseDSN); err != nil {
		t.Fatal(err)
	}

	var isolationDatabases []string
	if !t.Run("state does not leak", func(t *testing.T) {
		first := OpenPostgresTestDB(t)
		defer first.Close()
		var firstName string
		if err := first.QueryRow("SELECT current_database()").Scan(&firstName); err != nil {
			t.Fatal(err)
		}
		if firstName == postgresDatabaseName(baseDSN) || !strings.HasPrefix(firstName, "effchat_test_isolated_") {
			t.Fatalf("first database = %q, want an isolated clone", firstName)
		}
		if _, ok := isolationDatabaseCreatedAt(firstName); !ok {
			t.Fatalf("first database = %q, want a current isolation database name", firstName)
		}
		var defaultGroups int
		if err := first.QueryRow("SELECT COUNT(*) FROM user_groups WHERE is_default = true").Scan(&defaultGroups); err != nil {
			t.Fatal(err)
		}
		if defaultGroups != 1 {
			t.Fatalf("first database default groups = %d, want migration baseline", defaultGroups)
		}
		if _, err := first.Exec("CREATE TABLE isolation_sentinel (id integer primary key)"); err != nil {
			t.Fatal(err)
		}

		second := OpenPostgresTestDB(t)
		defer second.Close()
		var secondName string
		if err := second.QueryRow("SELECT current_database()").Scan(&secondName); err != nil {
			t.Fatal(err)
		}
		if secondName == firstName {
			t.Fatalf("second database = %q, want a separate clone", secondName)
		}
		var exists bool
		if err := second.QueryRow("SELECT to_regclass('public.isolation_sentinel') IS NOT NULL").Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatal("isolated databases leaked test state")
		}
		if err := second.QueryRow("SELECT COUNT(*) FROM user_groups WHERE is_default = true").Scan(&defaultGroups); err != nil {
			t.Fatal(err)
		}
		if defaultGroups != 1 {
			t.Fatalf("second database default groups = %d, want migration baseline", defaultGroups)
		}
		isolationDatabases = []string{firstName, secondName}
	}) {
		return
	}

	maintenanceDSN, err := withPostgresDatabase(baseDSN, "postgres")
	if err != nil {
		t.Fatalf("build PostgreSQL maintenance DSN: %v", err)
	}
	maintenanceDB, err := sql.Open("postgres", maintenanceDSN)
	if err != nil {
		t.Fatalf("open PostgreSQL maintenance database: %v", err)
	}
	defer maintenanceDB.Close()
	for _, database := range isolationDatabases {
		var exists bool
		if err := maintenanceDB.QueryRow("SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", database).Scan(&exists); err != nil {
			t.Fatalf("check isolated database %q: %v", database, err)
		}
		if exists {
			t.Fatalf("isolated database %q was not cleaned up", database)
		}
	}
}

func TestCleanupStaleIsolationDatabases(t *testing.T) {
	baseDSN := postgresTestDSN()
	if baseDSN == "" {
		t.Skip("set EFFCHAT_TEST_DATABASE_DSN to run PostgreSQL integration tests")
	}
	if err := validateTestDSN(baseDSN); err != nil {
		t.Fatal(err)
	}

	maintenanceDSN, err := withPostgresDatabase(baseDSN, "postgres")
	if err != nil {
		t.Fatal(err)
	}
	maintenanceDB, err := sql.Open("postgres", maintenanceDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer maintenanceDB.Close()
	lockCtx, lockCancel := context.WithTimeout(context.Background(), isolationDatabaseLockTTL)
	lockConn, err := maintenanceDB.Conn(lockCtx)
	lockCancel()
	if err != nil {
		t.Fatal(err)
	}
	defer lockConn.Close()
	lockCtx, lockCancel = context.WithTimeout(context.Background(), isolationDatabaseLockTTL)
	if _, err := lockConn.ExecContext(lockCtx, "SELECT pg_advisory_lock($1)", isolationDatabaseLock); err != nil {
		lockCancel()
		t.Fatal(err)
	}
	lockCancel()
	defer func() {
		unlockCtx, unlockCancel := context.WithTimeout(context.Background(), isolationDatabaseTTL)
		defer unlockCancel()
		_, _ = lockConn.ExecContext(unlockCtx, "SELECT pg_advisory_unlock($1)", isolationDatabaseLock)
	}()

	staleDatabase := isolationDatabaseName(time.Now().Add(-isolationDatabaseStaleAfter-time.Minute), strings.Repeat("a", 16))
	if _, err := maintenanceDB.Exec(fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s", pq.QuoteIdentifier(staleDatabase), pq.QuoteIdentifier(postgresDatabaseName(baseDSN)))); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dropIsolationDatabaseBounded(maintenanceDB, staleDatabase) }()

	ctx, cancel := context.WithTimeout(context.Background(), isolationDatabaseCloneTTL)
	defer cancel()
	if err := cleanupStaleIsolationDatabases(ctx, maintenanceDB, time.Now()); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := maintenanceDB.QueryRow("SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", staleDatabase).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatalf("stale isolated database %q was not removed", staleDatabase)
	}

	liveDatabase := isolationDatabaseName(time.Now().Add(-isolationDatabaseStaleAfter-time.Minute), strings.Repeat("b", 16))
	if _, err := maintenanceDB.Exec(fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s", pq.QuoteIdentifier(liveDatabase), pq.QuoteIdentifier(postgresDatabaseName(baseDSN)))); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dropIsolationDatabaseBounded(maintenanceDB, liveDatabase) }()
	liveDSN, err := withPostgresDatabase(baseDSN, liveDatabase)
	if err != nil {
		t.Fatal(err)
	}
	liveDB, err := sql.Open("postgres", liveDSN)
	if err != nil {
		t.Fatal(err)
	}
	if err := liveDB.Ping(); err != nil {
		_ = liveDB.Close()
		t.Fatal(err)
	}
	if err := cleanupStaleIsolationDatabases(ctx, maintenanceDB, time.Now()); err != nil {
		_ = liveDB.Close()
		t.Fatal(err)
	}
	if err := maintenanceDB.QueryRow("SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", liveDatabase).Scan(&exists); err != nil {
		_ = liveDB.Close()
		t.Fatal(err)
	}
	if !exists {
		_ = liveDB.Close()
		t.Fatalf("live isolated database %q was removed", liveDatabase)
	}
	if err := liveDB.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cleanupStaleIsolationDatabases(ctx, maintenanceDB, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := maintenanceDB.QueryRow("SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", liveDatabase).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatalf("disconnected stale isolated database %q was not removed", liveDatabase)
	}
}
