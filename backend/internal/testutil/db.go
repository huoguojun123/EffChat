package testutil

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
)

const (
	isolationDatabasePrefix     = "fchat_test_isolated_"
	isolationDatabaseLock       = int64(0x4643484154544553)
	isolationDatabaseTTL        = 5 * time.Second
	isolationDatabaseLockTTL    = 30 * time.Second
	isolationDatabaseCloneTTL   = 10 * time.Second
	isolationDatabaseStaleAfter = time.Hour
)

func OpenPostgresTestDB(t testing.TB) *sql.DB {
	t.Helper()

	dsn := postgresTestDSN()
	if dsn == "" {
		t.Skip("set FCHAT_TEST_DATABASE_DSN to run PostgreSQL integration tests")
	}
	if os.Getenv("FCHAT_ALLOW_DESTRUCTIVE_TESTS") != "1" {
		t.Fatalf("set FCHAT_ALLOW_DESTRUCTIVE_TESTS=1 to enable PostgreSQL integration tests")
	}
	if err := validateTestDSN(dsn); err != nil {
		t.Fatalf("unsafe PostgreSQL test DSN: %v", err)
	}
	templateDatabase := postgresDatabaseName(dsn)
	maintenanceDSN, err := withPostgresDatabase(dsn, "postgres")
	if err != nil {
		t.Fatalf("build PostgreSQL maintenance DSN: %v", err)
	}
	maintenanceDB, err := sql.Open("postgres", maintenanceDSN)
	if err != nil {
		t.Fatalf("open PostgreSQL maintenance database: %v", err)
	}

	maintenanceCtx, maintenanceCancel := context.WithTimeout(context.Background(), isolationDatabaseTTL)
	err = maintenanceDB.PingContext(maintenanceCtx)
	maintenanceCancel()
	if err != nil {
		_ = maintenanceDB.Close()
		t.Fatalf("connect to PostgreSQL maintenance database: test role must CONNECT to postgres and CREATE DATABASE: %v", err)
	}

	isolationDatabase, err := nextIsolationDatabaseName()
	if err != nil {
		_ = maintenanceDB.Close()
		t.Fatalf("create isolated PostgreSQL test database name: %v", err)
	}
	err = createIsolationDatabase(maintenanceDB, templateDatabase, isolationDatabase)
	if err != nil {
		_ = maintenanceDB.Close()
		t.Fatalf("create isolated PostgreSQL test database: test role must be able to clone %q: %v", templateDatabase, err)
	}
	isolationDSN, err := withPostgresDatabase(dsn, isolationDatabase)
	if err != nil {
		_ = dropIsolationDatabaseBounded(maintenanceDB, isolationDatabase)
		_ = maintenanceDB.Close()
		t.Fatalf("build isolated PostgreSQL test DSN: %v", err)
	}
	db, err := sql.Open("postgres", isolationDSN)
	if err != nil {
		_ = dropIsolationDatabaseBounded(maintenanceDB, isolationDatabase)
		_ = maintenanceDB.Close()
		t.Fatalf("open isolated PostgreSQL test database: %v", err)
	}
	isolationCtx, isolationCancel := context.WithTimeout(context.Background(), isolationDatabaseTTL)
	err = db.PingContext(isolationCtx)
	isolationCancel()
	if err != nil {
		_ = db.Close()
		_ = dropIsolationDatabaseBounded(maintenanceDB, isolationDatabase)
		_ = maintenanceDB.Close()
		t.Fatalf("ping isolated PostgreSQL test database: %v", err)
	}
	resetCtx, resetCancel := context.WithTimeout(context.Background(), isolationDatabaseTTL)
	err = resetIsolationDatabase(resetCtx, db)
	resetCancel()
	if err != nil {
		_ = db.Close()
		_ = dropIsolationDatabaseBounded(maintenanceDB, isolationDatabase)
		_ = maintenanceDB.Close()
		t.Fatalf("reset isolated PostgreSQL test database: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		if err := dropIsolationDatabaseBounded(maintenanceDB, isolationDatabase); err != nil {
			t.Errorf("drop isolated PostgreSQL test database %q: %v", isolationDatabase, err)
		}
		_ = maintenanceDB.Close()
	})

	return db
}

func resetIsolationDatabase(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		DO $$
		DECLARE
			tables TEXT;
		BEGIN
			SELECT string_agg(format('%I.%I', schemaname, tablename), ', ')
			INTO tables
			FROM pg_tables
			WHERE schemaname = 'public'
			  AND tablename <> 'schema_migrations';
			IF tables IS NOT NULL THEN
				EXECUTE 'TRUNCATE TABLE ' || tables || ' RESTART IDENTITY CASCADE';
			END IF;
		END
		$$;
	`)
	if err != nil {
		return fmt.Errorf("truncate isolated test data: %w", err)
	}
	return nil
}

func nextIsolationDatabaseName() (string, error) {
	entropy := make([]byte, 8)
	if _, err := rand.Read(entropy); err != nil {
		return "", fmt.Errorf("read isolation database entropy: %w", err)
	}
	return isolationDatabaseName(time.Now(), hex.EncodeToString(entropy)), nil
}

func isolationDatabaseName(createdAt time.Time, token string) string {
	return fmt.Sprintf("%s%d_%s", isolationDatabasePrefix, createdAt.UnixNano(), token)
}

func isolationDatabaseCreatedAt(database string) (time.Time, bool) {
	suffix, ok := strings.CutPrefix(database, isolationDatabasePrefix)
	if !ok {
		return time.Time{}, false
	}
	parts := strings.SplitN(suffix, "_", 2)
	if len(parts) != 2 || len(parts[1]) != 16 {
		return time.Time{}, false
	}
	if _, err := hex.DecodeString(parts[1]); err != nil {
		return time.Time{}, false
	}
	nanoseconds, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(0, nanoseconds), true
}

func createIsolationDatabase(maintenanceDB *sql.DB, templateDatabase, isolationDatabase string) error {
	connectionCtx, connectionCancel := context.WithTimeout(context.Background(), isolationDatabaseTTL)
	conn, err := maintenanceDB.Conn(connectionCtx)
	connectionCancel()
	if err != nil {
		return fmt.Errorf("open maintenance connection: %w", err)
	}
	defer conn.Close()
	lockCtx, lockCancel := context.WithTimeout(context.Background(), isolationDatabaseLockTTL)
	_, err = conn.ExecContext(lockCtx, "SELECT pg_advisory_lock($1)", isolationDatabaseLock)
	lockCancel()
	if err != nil {
		return fmt.Errorf("lock database clone: %w", err)
	}
	defer func() {
		unlockCtx, unlockCancel := context.WithTimeout(context.Background(), isolationDatabaseTTL)
		defer unlockCancel()
		_, _ = conn.ExecContext(unlockCtx, "SELECT pg_advisory_unlock($1)", isolationDatabaseLock)
	}()
	staleCleanupCtx, staleCleanupCancel := context.WithTimeout(context.Background(), isolationDatabaseCloneTTL)
	err = cleanupStaleIsolationDatabases(staleCleanupCtx, maintenanceDB, time.Now())
	staleCleanupCancel()
	if err != nil {
		return err
	}
	cloneCtx, cloneCancel := context.WithTimeout(context.Background(), isolationDatabaseCloneTTL)
	_, err = conn.ExecContext(cloneCtx, fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s", pq.QuoteIdentifier(isolationDatabase), pq.QuoteIdentifier(templateDatabase)))
	cloneCancel()
	if err != nil {
		return fmt.Errorf("clone %q from %q: %w", isolationDatabase, templateDatabase, err)
	}
	return nil
}

func cleanupStaleIsolationDatabases(ctx context.Context, maintenanceDB *sql.DB, now time.Time) error {
	rows, err := maintenanceDB.QueryContext(ctx, `
		SELECT database.datname
		FROM pg_database AS database
		WHERE database.datname LIKE $1
		  AND NOT EXISTS (
				SELECT 1
				FROM pg_stat_activity AS activity
				WHERE activity.datname = database.datname
			  )`, isolationDatabasePrefix+"%")
	if err != nil {
		return fmt.Errorf("list stale isolated databases: %w", err)
	}
	defer rows.Close()

	var staleDatabases []string
	for rows.Next() {
		var database string
		if err := rows.Scan(&database); err != nil {
			return fmt.Errorf("read stale isolated database: %w", err)
		}
		createdAt, ok := isolationDatabaseCreatedAt(database)
		if ok && now.Sub(createdAt) > isolationDatabaseStaleAfter {
			staleDatabases = append(staleDatabases, database)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate stale isolated databases: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close stale isolated database rows: %w", err)
	}
	for _, database := range staleDatabases {
		if err := dropStaleIsolationDatabase(ctx, maintenanceDB, database); err != nil {
			if isDatabaseInUse(err) {
				continue
			}
			return fmt.Errorf("remove stale isolated database %q: %w", database, err)
		}
	}
	return nil
}

func dropIsolationDatabaseBounded(maintenanceDB *sql.DB, database string) error {
	ctx, cancel := context.WithTimeout(context.Background(), isolationDatabaseTTL)
	defer cancel()
	return dropIsolationDatabase(ctx, maintenanceDB, database)
}

func dropIsolationDatabase(ctx context.Context, maintenanceDB *sql.DB, database string) error {
	_, err := maintenanceDB.ExecContext(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", pq.QuoteIdentifier(database)))
	if err != nil {
		return fmt.Errorf("drop database: %w", err)
	}
	return nil
}

func dropStaleIsolationDatabase(ctx context.Context, maintenanceDB *sql.DB, database string) error {
	_, err := maintenanceDB.ExecContext(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s", pq.QuoteIdentifier(database)))
	if err != nil {
		return fmt.Errorf("drop database: %w", err)
	}
	return nil
}

func isDatabaseInUse(err error) bool {
	var postgresError *pq.Error
	return errors.As(err, &postgresError) && postgresError.Code == "55006"
}

func validateTestDSN(dsn string) error {
	_, name, err := parsePostgresTestURL(dsn)
	if err != nil {
		return err
	}
	if !strings.Contains(strings.ToLower(name), "test") {
		return fmt.Errorf("database name %q must contain test", name)
	}
	return nil
}

func postgresDatabaseName(dsn string) string {
	_, name, err := parsePostgresTestURL(dsn)
	if err != nil {
		return ""
	}
	return name
}

func withPostgresDatabase(dsn, database string) (string, error) {
	if strings.TrimSpace(database) == "" {
		return "", fmt.Errorf("database name is required")
	}
	parsed, _, err := parsePostgresTestURL(dsn)
	if err != nil {
		return "", err
	}
	parsed.Path = "/" + database
	parsed.RawPath = ""
	return parsed.String(), nil
}

func parsePostgresTestURL(dsn string) (*url.URL, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(dsn))
	if err != nil {
		return nil, "", fmt.Errorf("parse PostgreSQL test DSN: %w", err)
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return nil, "", fmt.Errorf("PostgreSQL test DSN must use postgres:// or postgresql:// URL form")
	}
	if parsed.Hostname() == "" || parsed.User == nil || parsed.User.Username() == "" {
		return nil, "", fmt.Errorf("PostgreSQL test DSN must include an explicit host and user")
	}
	database := strings.TrimPrefix(parsed.Path, "/")
	if database == "" || strings.Contains(database, "/") {
		return nil, "", fmt.Errorf("PostgreSQL test DSN must contain exactly one database path")
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return nil, "", fmt.Errorf("parse PostgreSQL test DSN query: %w", err)
	}
	for key := range query {
		switch strings.ToLower(key) {
		case "dbname", "database", "host", "hostaddr", "port", "user", "password", "service", "passfile":
			return nil, "", fmt.Errorf("PostgreSQL test DSN query must not override %q", key)
		}
	}
	return parsed, database, nil
}

func postgresTestDSN() string {
	for _, key := range []string{"FCHAT_TEST_DATABASE_DSN", "TEST_DATABASE_DSN"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}
