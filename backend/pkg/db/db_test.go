package db

import (
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/huoguojun123/EffChat/pkg/config"
	"github.com/lib/pq"
)

func TestRequiredSchemaVersionMatchesLatestProductionMigration(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file path")
	}
	paths, err := filepath.Glob(filepath.Join(filepath.Dir(file), "..", "..", "migrations", "production", "*.sql"))
	if err != nil {
		t.Fatalf("list production migrations: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no production migrations found")
	}
	sort.Strings(paths)
	if latest := filepath.Base(paths[len(paths)-1]); RequiredSchemaVersion != latest {
		t.Fatalf("required schema version = %q, want latest production migration %q", RequiredSchemaVersion, latest)
	}
}

func TestBuildDSNQuotesKeywordValues(t *testing.T) {
	dsn := buildDSN(config.DatabaseConfig{
		Host:     "db host",
		Port:     5432,
		User:     "user name",
		Password: "pa ss'\\word",
		DBName:   "f chat",
		SSLMode:  "disable",
	})

	for _, want := range []string{
		"host='db host'",
		"user='user name'",
		"password='pa ss\\'\\\\word'",
		"dbname='f chat'",
		"sslmode='disable'",
	} {
		if !strings.Contains(dsn, want) {
			t.Fatalf("dsn %q does not contain %q", dsn, want)
		}
	}
	if _, err := pq.NewConnector(dsn); err != nil {
		t.Fatalf("pq.NewConnector rejected quoted dsn %q: %v", dsn, err)
	}
}

func TestBuildDSNPrefersDatabaseURL(t *testing.T) {
	const databaseURL = "postgres://fixture:secret@db.example/effchat?sslmode=require"
	dsn := buildDSN(config.DatabaseConfig{
		URL:      databaseURL,
		Host:     "ignored.example",
		Port:     5432,
		User:     "ignored",
		Password: "ignored",
		DBName:   "ignored",
		SSLMode:  "disable",
	})
	if dsn != databaseURL {
		t.Fatalf("buildDSN() = %q, want DATABASE_URL", dsn)
	}
}
