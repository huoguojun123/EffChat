#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PRODUCTION_DIR="${MIGRATIONS_PRODUCTION_DIR:-$SCRIPT_DIR/production}"
LEGACY_CHECKSUMS_FILE="${MIGRATIONS_LEGACY_CHECKSUMS_FILE:-$SCRIPT_DIR/legacy-checksums.txt}"

hash_file() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
        return
    fi
    if command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{print $1}'
        return
    fi
    echo "A SHA-256 command is required to build the migration script." >&2
    exit 1
}

hash_stdin() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum | awk '{print $1}'
        return
    fi
    if command -v shasum >/dev/null 2>&1; then
        shasum -a 256 | awk '{print $1}'
        return
    fi
    echo "A SHA-256 command is required to build the migration script." >&2
    exit 1
}

sql_literal() {
    local value="${1//\'/\'\'}"
    printf "'%s'" "$value"
}

migration_checksum() {
    local version="$1"
    local file="$2"
    if [ "$version" = "001_schema.sql" ]; then
        # 001 includes init.sql. Hash both immutable inputs so a future edit to
        # either file is rejected instead of silently changing the baseline.
        {
            printf '%s\n' 'EffChat migration baseline v1: 001_schema.sql'
            cat "$file"
            printf '%s\n' 'EffChat migration baseline v1: init.sql'
            cat "$SCRIPT_DIR/init.sql"
        } | hash_stdin
        return
    fi
    hash_file "$file"
}

legacy_checksum_predicate() {
    local version="$1"
    local predicates="checksum = ''"
    local legacy
    [ -f "$LEGACY_CHECKSUMS_FILE" ] || {
        printf '%s' "$predicates"
        return
    }
    while read -r recorded_version legacy || [ -n "${recorded_version:-}" ]; do
        case "${recorded_version:-}" in
            ''|'#'*) continue ;;
        esac
        [ "$recorded_version" = "$version" ] || continue
        predicates="$predicates OR checksum = $(sql_literal "$legacy")"
    done < "$LEGACY_CHECKSUMS_FILE"
    printf '%s' "$predicates"
}

validate_migration_transaction_ownership() {
    local file="$1"
    if grep -Eiq '^[[:space:]]*(BEGIN|COMMIT|ROLLBACK)([[:space:]].*)?;[[:space:]]*(--.*)?$' "$file"; then
        echo "Migration $(basename "$file") contains transaction control; the runner owns BEGIN/COMMIT." >&2
        exit 1
    fi
    if grep -Eiq '^[[:space:]]*CREATE[[:space:]]+(UNIQUE[[:space:]]+)?INDEX[[:space:]]+CONCURRENTLY([[:space:]]|$)' "$file"; then
        echo "Migration $(basename "$file") uses CREATE INDEX CONCURRENTLY, which cannot run in the atomic migration transaction." >&2
        exit 1
    fi
}

shopt -s nullglob
migrations=("$PRODUCTION_DIR"/*.sql)
shopt -u nullglob
[ ${#migrations[@]} -gt 0 ] || {
    echo "No production migrations found in $PRODUCTION_DIR" >&2
    exit 1
}

cat <<'SQL'
SELECT pg_advisory_lock(823764219);
CREATE TABLE IF NOT EXISTS schema_migrations (
  version TEXT PRIMARY KEY,
  applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  checksum TEXT NOT NULL DEFAULT ''
);
ALTER TABLE schema_migrations ADD COLUMN IF NOT EXISTS checksum TEXT NOT NULL DEFAULT '';
SQL

for migration in "${migrations[@]}"; do
    validate_migration_transaction_ownership "$migration"
    version="$(basename "$migration")"
    checksum="$(migration_checksum "$version" "$migration")"
    reconcile="$(legacy_checksum_predicate "$version")"
    version_sql="$(sql_literal "$version")"
    checksum_sql="$(sql_literal "$checksum")"
    cat <<SQL
UPDATE schema_migrations
SET checksum = $checksum_sql
WHERE version = $version_sql AND ($reconcile);
SELECT NOT EXISTS (
  SELECT 1 FROM schema_migrations
  WHERE version = $version_sql AND checksum <> $checksum_sql
) AS migration_checksum_matches \gset
\if :migration_checksum_matches
\else
\echo migration checksum mismatch: $version
SELECT 1 / 0;
\endif
SELECT NOT EXISTS (
  SELECT 1 FROM schema_migrations WHERE version = $version_sql
) AS apply_migration \gset
\if :apply_migration
\echo apply migration $version
BEGIN;
SQL
    if [ "$version" = "001_schema.sql" ]; then
        cat "$SCRIPT_DIR/init.sql"
    else
        cat "$migration"
    fi
    cat <<SQL
INSERT INTO schema_migrations (version, checksum) VALUES ($version_sql, $checksum_sql);
COMMIT;
\else
\echo skip migration $version
\endif
SQL
done

cat <<'SQL'
SELECT pg_advisory_unlock(823764219);
SQL
