#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONTAINER="${POSTGRES_CONTAINER:?Set POSTGRES_CONTAINER to an isolated PostgreSQL container}"
DB_USER="${DB_USER:-effchat_test}"
ATOMIC_DB="effchat_migration_atomic_$$"
CONCURRENT_DB="effchat_migration_concurrent_$$"
ATOMIC_DIR="$(mktemp -d)"
CONCURRENT_DIR="$(mktemp -d)"
VALIDATION_DIR="$(mktemp -d)"
CONCURRENT_SCRIPT="$(mktemp)"
PRODUCTION_COUNT="$(find "$ROOT/backend/migrations/production" -maxdepth 1 -name '*.sql' | wc -l | tr -d ' ')"
CONCURRENT_COUNT="$((PRODUCTION_COUNT + 1))"

psql_admin() {
    docker exec -i "$CONTAINER" psql -X -v ON_ERROR_STOP=1 -U "$DB_USER" -d postgres "$@"
}

psql_db() {
    local database="$1"
    shift
    docker exec -i "$CONTAINER" psql -X -v ON_ERROR_STOP=1 -U "$DB_USER" -d "$database" "$@"
}

drop_database() {
    local database="$1"
    psql_admin -c "DROP DATABASE IF EXISTS $database WITH (FORCE);" >/dev/null 2>&1 || true
}

cleanup() {
    drop_database "$ATOMIC_DB"
    drop_database "$CONCURRENT_DB"
    rm -rf "$ATOMIC_DIR" "$CONCURRENT_DIR" "$VALIDATION_DIR"
    rm -f "$CONCURRENT_SCRIPT"
}
trap cleanup EXIT

link_production_migrations() {
    local target="$1" migration
    for migration in "$ROOT"/backend/migrations/production/*.sql; do
        ln -s "$migration" "$target/$(basename "$migration")"
    done
}

run_migrations() {
    local database="$1" migration_dir="$2"
    MIGRATIONS_PRODUCTION_DIR="$migration_dir" \
        "$ROOT/backend/migrations/build_migration_script.sh" | psql_db "$database"
}

# 001 executes init.sql and is the immutable production baseline. Fresh-schema
# tests alone cannot detect an accidental edit because they would simply record
# the new checksum; pinning the deployed checksum catches that drift before an
# existing installation reaches the migration runner.
BASELINE_CHECKSUM="$(
    "$ROOT/backend/migrations/build_migration_script.sh" |
        awk '/^SET checksum = / && checksum == "" { checksum=$4; gsub(/'\''/, "", checksum) } END { print checksum }'
)"
[ "$BASELINE_CHECKSUM" = "d8ec8e907836f21bbea686e084b1eecc0081e1989c8ff063b3c4f7bb77739f7a" ] || {
    echo "immutable 001/init baseline checksum changed: $BASELINE_CHECKSUM" >&2
    exit 1
}

ln -s "$ROOT/backend/migrations/testdata/997_nested_transaction.sql" \
    "$VALIDATION_DIR/997_nested_transaction.sql"
if MIGRATIONS_PRODUCTION_DIR="$VALIDATION_DIR" \
    "$ROOT/backend/migrations/build_migration_script.sh" >/dev/null 2>&1; then
    echo "migration-owned transaction control was not rejected" >&2
    exit 1
fi

link_production_migrations "$ATOMIC_DIR"
ln -s "$ROOT/backend/migrations/testdata/999_atomic_failure.sql" \
    "$ATOMIC_DIR/999_atomic_failure.sql"

psql_admin -c "CREATE DATABASE $ATOMIC_DB;" >/dev/null
for attempt in 1 2; do
    if run_migrations "$ATOMIC_DB" "$ATOMIC_DIR" >/dev/null 2>&1; then
        echo "atomic failure attempt $attempt unexpectedly succeeded" >&2
        exit 1
    fi
    result="$(psql_db "$ATOMIC_DB" -At -v expected_migrations="$PRODUCTION_COUNT" <<'SQL'
SELECT to_regclass('public.migration_atomicity_probe') IS NULL
   AND NOT EXISTS (
       SELECT 1 FROM schema_migrations WHERE version = '999_atomic_failure.sql'
   )
   AND (SELECT count(*) FROM schema_migrations) = :expected_migrations;
SQL
)"
    [ "$result" = "t" ] || {
        echo "atomic failure attempt $attempt left schema or ledger residue" >&2
        exit 1
    }
done

result="$(psql_db "$ATOMIC_DB" -At <<'SQL'
SELECT
    to_regclass('public.idx_files_ocr_task') IS NOT NULL
    AND to_regclass('public.idx_files_ocr_status') IS NOT NULL
    AND EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'model_usage_events'::regclass
          AND conname = 'model_usage_events_kind_check'
          AND pg_get_constraintdef(oid) LIKE '%timeline_compaction%'
    )
    AND EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'model_task_runs'::regclass
          AND conname = 'model_task_runs_task_key_check'
          AND pg_get_constraintdef(oid) LIKE '%timeline_compaction%'
    )
    AND (
        SELECT count(*) FROM pg_constraint c
        WHERE c.conrelid = 'chat_run_reservations'::regclass
          AND c.contype = 'f'
          AND c.conkey = ARRAY[(
              SELECT attnum FROM pg_attribute
              WHERE attrelid = c.conrelid AND attname = 'user_message_id'
          )]::smallint[]
    ) = 1
    AND (
        SELECT count(*) FROM pg_constraint c
        WHERE c.conrelid = 'chat_run_reservations'::regclass
          AND c.contype = 'f'
          AND c.conkey = ARRAY[(
              SELECT attnum FROM pg_attribute
              WHERE attrelid = c.conrelid AND attname = 'terminal_message_id'
          )]::smallint[]
    ) = 1
    AND (
        SELECT count(*) FROM information_schema.columns
        WHERE table_schema = 'public' AND table_name = 'v_active_sessions'
          AND column_name IN ('folder_id', 'pinned_at', 'answer_selection_revision')
    ) = 3
    AND (
        SELECT timeout_seconds FROM tool_configs WHERE tool_key = 'web_extract'
    ) = 30
    AND (
        SELECT count(*) FROM tool_configs
    ) = 9
    AND NOT EXISTS (
        SELECT 1 FROM tool_configs
        WHERE tool_key NOT IN (
            'memory', 'file_list', 'file_search', 'file_read',
            'skill_list', 'skill_search', 'skill_read',
            'web_search', 'web_extract'
        )
    )
    AND EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'tool_configs'::regclass
          AND conname = 'tool_configs_tool_key_check'
    )
    AND (
        SELECT count(*) FROM information_schema.tables
        WHERE table_schema = 'public'
          AND table_name IN ('governance_events', 'skill_import_record_files')
    ) = 2
    AND EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'governance_events'::regclass
          AND pg_get_constraintdef(oid) LIKE '%actor_type%system%actor_user_id%'
    )
    AND EXISTS (
        SELECT 1 FROM pg_indexes
        WHERE schemaname = 'public'
          AND tablename = 'governance_events'
          AND indexname = 'idx_governance_events_rollback_of'
          AND indexdef LIKE '%UNIQUE INDEX%'
    )
    AND EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'skill_import_records'::regclass
          AND conname = 'skill_import_records_source_type_check'
          AND pg_get_constraintdef(oid) LIKE '%builtin%'
    )
    AND EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'files'
          AND column_name = 'ocr_lease_generation'
          AND data_type = 'bigint'
          AND is_nullable = 'NO'
          AND column_default = '0'
    )
    AND (
        SELECT count(*) FROM user_groups WHERE is_default = true
    ) = 1
    AND EXISTS (
        SELECT 1 FROM user_groups
        WHERE name = 'Default'
          AND level = 0
          AND is_default = true
          AND daily_message_limit = 0
          AND daily_token_limit = 0
          AND concurrent_run_limit = 0
          AND daily_tool_call_limit = 0
          AND daily_web_search_limit = 0
          AND daily_web_extract_limit = 0
          AND daily_ocr_file_limit = 0
          AND daily_ocr_page_limit = 0
    );
SQL
)"
[ "$result" = "t" ] || {
    echo "fresh schema does not satisfy the current catalog and OCR fencing contracts" >&2
    exit 1
}

# Migration 053 only repairs a missing default. It must neither replace an
# administrator-selected default nor overwrite an existing same-name group's
# access level and quotas when promoting it into the missing role.
psql_db "$ATOMIC_DB" >/dev/null <<'SQL'
INSERT INTO user_groups (name, level, description, is_default, daily_message_limit)
VALUES ('Configured', 23, 'administrator configured', false, 37);
UPDATE user_groups SET is_default = false WHERE name = 'Default';
UPDATE user_groups SET is_default = true WHERE name = 'Configured';
SQL
psql_db "$ATOMIC_DB" < "$ROOT/backend/migrations/production/053_default_user_group_baseline.sql" >/dev/null
result="$(psql_db "$ATOMIC_DB" -At <<'SQL'
SELECT
    (SELECT count(*) FROM user_groups WHERE is_default = true) = 1
    AND (SELECT is_default FROM user_groups WHERE name = 'Configured')
    AND NOT (SELECT is_default FROM user_groups WHERE name = 'Default');
SQL
)"
[ "$result" = "t" ] || {
    echo "default user group migration replaced an existing administrator default" >&2
    exit 1
}

psql_db "$ATOMIC_DB" >/dev/null <<'SQL'
UPDATE user_groups SET is_default = false WHERE is_default = true;
UPDATE user_groups
SET level = 17, description = 'preserve this configuration', daily_message_limit = 29
WHERE name = 'Default';
SQL
psql_db "$ATOMIC_DB" < "$ROOT/backend/migrations/production/053_default_user_group_baseline.sql" >/dev/null
result="$(psql_db "$ATOMIC_DB" -At <<'SQL'
SELECT
    (SELECT count(*) FROM user_groups WHERE is_default = true) = 1
    AND EXISTS (
        SELECT 1 FROM user_groups
        WHERE name = 'Default'
          AND is_default = true
          AND level = 17
          AND description = 'preserve this configuration'
          AND daily_message_limit = 29
    );
SQL
)"
[ "$result" = "t" ] || {
    echo "default user group migration overwrote an existing same-name group" >&2
    exit 1
}

if psql_db "$ATOMIC_DB" -c \
    "INSERT INTO tool_configs (tool_key, display_name) VALUES ('unregistered_tool', 'Unregistered');" \
    >/dev/null 2>&1; then
    echo "tool catalog constraint accepted an unregistered key" >&2
    exit 1
fi

if psql_db "$ATOMIC_DB" -c \
    "INSERT INTO governance_events (resource_type, resource_key, action, actor_type, reason, after_state) VALUES ('tool', 'memory', 'update', 'admin', 'missing actor', '{}'::jsonb);" \
    >/dev/null 2>&1; then
    echo "governance audit accepted an admin event without an actor" >&2
    exit 1
fi

if psql_db "$ATOMIC_DB" -c \
    "INSERT INTO governance_events (resource_type, resource_key, action, actor_type, reason, after_state) VALUES ('tool', 'memory', 'rollback', 'system', 'missing source', '{}'::jsonb);" \
    >/dev/null 2>&1; then
    echo "governance audit accepted a rollback without a source event" >&2
    exit 1
fi

psql_db "$ATOMIC_DB" >/dev/null <<'SQL'
UPDATE schema_migrations SET checksum = 'legacy-baseline-v1' WHERE version = '001_schema.sql';
UPDATE schema_migrations SET checksum = '' WHERE version = '002_skills.sql';
UPDATE schema_migrations SET checksum = '1de49ac514319061043a420024d9dd06f50c5b2e7c5057573a1ce8099d0edebc'
WHERE version = '032_user_group_default_invariant.sql';
UPDATE schema_migrations SET checksum = '092ecf033d705ab8a823d810421d1f4d22fc2a0cf351bab49042d2f90475e04d'
WHERE version = '041_conversation_search.sql';
SQL
run_migrations "$ATOMIC_DB" "$ROOT/backend/migrations/production" >/dev/null

result="$(psql_db "$ATOMIC_DB" -At <<'SQL'
SELECT count(*) = 0 FROM schema_migrations
WHERE checksum IN (
    '',
    'legacy-baseline-v1',
    '1de49ac514319061043a420024d9dd06f50c5b2e7c5057573a1ce8099d0edebc',
    '092ecf033d705ab8a823d810421d1f4d22fc2a0cf351bab49042d2f90475e04d'
);
SQL
)"
[ "$result" = "t" ] || {
    echo "known legacy checksums were not reconciled" >&2
    exit 1
}

psql_db "$ATOMIC_DB" -c \
    "UPDATE schema_migrations SET checksum = 'unknown-checksum-must-fail' WHERE version = '032_user_group_default_invariant.sql';" \
    >/dev/null
if run_migrations "$ATOMIC_DB" "$ROOT/backend/migrations/production" >/dev/null 2>&1; then
    echo "unknown migration checksum unexpectedly succeeded" >&2
    exit 1
fi

link_production_migrations "$CONCURRENT_DIR"
ln -s "$ROOT/backend/migrations/testdata/998_concurrency_probe.sql" \
    "$CONCURRENT_DIR/998_concurrency_probe.sql"
MIGRATIONS_PRODUCTION_DIR="$CONCURRENT_DIR" \
    "$ROOT/backend/migrations/build_migration_script.sh" > "$CONCURRENT_SCRIPT"
psql_admin -c "CREATE DATABASE $CONCURRENT_DB;" >/dev/null

set +e
psql_db "$CONCURRENT_DB" < "$CONCURRENT_SCRIPT" >/dev/null 2>&1 &
first_pid=$!
psql_db "$CONCURRENT_DB" < "$CONCURRENT_SCRIPT" >/dev/null 2>&1 &
second_pid=$!
wait "$first_pid"
first_exit=$?
wait "$second_pid"
second_exit=$?
set -e
[ "$first_exit" -eq 0 ] && [ "$second_exit" -eq 0 ] || {
    echo "concurrent migration runners did not both succeed" >&2
    exit 1
}

result="$(psql_db "$CONCURRENT_DB" -At -v expected_migrations="$CONCURRENT_COUNT" <<'SQL'
SELECT to_regclass('public.migration_concurrency_probe') IS NOT NULL
   AND (SELECT count(*) FROM schema_migrations WHERE version = '998_concurrency_probe.sql') = 1
   AND (SELECT count(*) FROM schema_migrations) = :expected_migrations;
SQL
)"
[ "$result" = "t" ] || {
    echo "concurrent runners did not converge on one schema and ledger state" >&2
    exit 1
}

echo "migration contract tests passed"
