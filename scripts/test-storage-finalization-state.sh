#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
TEST_ROOT="$(mktemp -d)"
trap 'rm -rf -- "$TEST_ROOT"' EXIT

mkdir -p "$TEST_ROOT/bin" "$TEST_ROOT/data/storage" "$TEST_ROOT/data/storage-migration-backups/test"
FAKE_DOCKER_LOG="$TEST_ROOT/docker.log"
export FAKE_DOCKER_LOG TEST_DATA_DIR="$TEST_ROOT/data"

cat > "$TEST_ROOT/bin/docker" <<'FAKE_DOCKER'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$FAKE_DOCKER_LOG"

case " $* " in
  *" config --environment "*)
    printf '%s\n' \
      "DATA_DIR=$TEST_DATA_DIR" \
      'POSTGRES_USER=effchat' \
      'POSTGRES_DB=effchat' \
      'POSTGRES_PASSWORD=test-password' \
      'JWT_SECRET=test-jwt'
    ;;
  *" exec -T postgres pg_isready "*) ;;
  *" exec -T postgres psql "*)
    if [[ " $* " == *" -Atc "* ]] && [[ " $* " == *"count(*)"* ]]; then
      printf '0\n'
    elif [ ! -t 0 ]; then
      cat >/dev/null
    fi
    ;;
  *" stop web backend "*) ;;
  *)
    printf 'Unexpected fake docker call: %s\n' "$*" >&2
    exit 1
    ;;
esac
FAKE_DOCKER
chmod +x "$TEST_ROOT/bin/docker"

printf '%s\n' \
  'POSTGRES_PASSWORD=test-password' \
  'JWT_SECRET=test-jwt' \
  > "$TEST_ROOT/.env"

MARKER="$TEST_ROOT/data/storage/.layout-v1"
RESTORE_SQL="$TEST_ROOT/data/storage-migration-backups/test/restore-paths.sql"
printf 'BEGIN;\nCOMMIT;\n' > "$RESTORE_SQL"

write_marker() {
  local state="$1" migrated_at="$2"
  printf '%s\n' \
    'layout=v1' \
    "state=$state" \
    "migrated_at_epoch=$migrated_at" \
    "legacy_root=$TEST_ROOT/data/uploads" \
    "restore_sql=$RESTORE_SQL" \
    > "$MARKER"
}

run_layout() {
  PATH="$TEST_ROOT/bin:$PATH" ENV_FILE="$TEST_ROOT/.env" \
    "$ROOT/scripts/storage-layout.sh" "$@"
}

old_epoch="$(( $(date +%s) - 604801 ))"

mkdir -p "$TEST_ROOT/data/uploads/1"
printf 'legacy\n' > "$TEST_ROOT/data/uploads/1/file.txt"
write_marker migrated "$old_epoch"
CONFIRM_STORAGE_FINALIZE=DELETE_LEGACY_UPLOADS run_layout finalize
grep -Fqx 'state=finalized' "$MARKER"
grep -Eq '^finalized_at_epoch=[0-9]+$' "$MARKER"
test ! -e "$TEST_ROOT/data/uploads"

: > "$FAKE_DOCKER_LOG"
if run_layout rollback >"$TEST_ROOT/finalized-rollback.log" 2>&1; then
  echo "Expected finalized rollback rejection." >&2
  exit 1
fi
grep -Fq 'cannot be rolled back' "$TEST_ROOT/finalized-rollback.log"
grep -Fqx 'state=finalized' "$MARKER"
if grep -Eq ' stop web backend | exec -T postgres psql ' "$FAKE_DOCKER_LOG"; then
  echo "Finalized rollback reached a mutating Docker or database call." >&2
  exit 1
fi

mkdir -p "$TEST_ROOT/data/uploads/1"
printf 'legacy\n' > "$TEST_ROOT/data/uploads/1/file.txt"
write_marker migrated "$(date +%s)"
if CONFIRM_STORAGE_FINALIZE=DELETE_LEGACY_UPLOADS run_layout finalize \
  >"$TEST_ROOT/early-finalize.log" 2>&1; then
  echo "Expected retention-period rejection." >&2
  exit 1
fi
grep -Fq 'at least 7 days' "$TEST_ROOT/early-finalize.log"
grep -Fqx 'state=migrated' "$MARKER"
test -f "$TEST_ROOT/data/uploads/1/file.txt"

write_marker migrated "$old_epoch"
run_layout rollback
test ! -e "$MARKER"
test -f "$TEST_ROOT/data/uploads/1/file.txt"

printf '%s\n' \
  'layout=v1' \
  "migrated_at_epoch=$old_epoch" \
  "legacy_root=$TEST_ROOT/data/uploads" \
  "restore_sql=$RESTORE_SQL" \
  > "$MARKER"
grep -Fq 'status=migrated' < <(run_layout plan)

echo "Storage finalization state checks passed."
