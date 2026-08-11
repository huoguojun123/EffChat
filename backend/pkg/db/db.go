package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/huoguojun123/EffChat/pkg/config"
	_ "github.com/lib/pq"
)

const RequiredSchemaVersion = "052_remove_prompt_popularity.sql"

func VerifySchemaVersion(ctx context.Context, database *sql.DB) (string, error) {
	var compatible bool
	if err := database.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)
	`, RequiredSchemaVersion).Scan(&compatible); err != nil {
		return "", fmt.Errorf("query schema compatibility: %w", err)
	}
	if !compatible {
		return "", fmt.Errorf("required migration %s is missing", RequiredSchemaVersion)
	}

	var version string
	if err := database.QueryRowContext(ctx, `
		SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1
	`).Scan(&version); err != nil {
		return "", fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}

func Connect(cfg config.DatabaseConfig) (*sql.DB, error) {
	dsn := buildDSN(cfg)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// 测试连接
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// 设置连接池
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(10 * time.Minute)

	return db, nil
}

func buildDSN(cfg config.DatabaseConfig) string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		conninfoValue(cfg.Host),
		cfg.Port,
		conninfoValue(cfg.User),
		conninfoValue(cfg.Password),
		conninfoValue(cfg.DBName),
		conninfoValue(cfg.SSLMode),
	)
}

func conninfoValue(value string) string {
	var b strings.Builder
	b.Grow(len(value) + 2)
	b.WriteByte('\'')
	for _, r := range value {
		if r == '\'' || r == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('\'')
	return b.String()
}
