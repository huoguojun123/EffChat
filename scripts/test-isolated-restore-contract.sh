#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
TEST_ROOT="$(mktemp -d)"
cleanup_test_root() {
  local status=$?
  if [ "${DEBUG_KEEP_TEST_ROOT:-0}" = 1 ]; then
    echo "DEBUG_TEST_ROOT=$TEST_ROOT" >&2
  else
    rm -rf -- "$TEST_ROOT"
  fi
  exit "$status"
}
trap cleanup_test_root EXIT

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

mkdir -p "$TEST_ROOT/bin" "$TEST_ROOT/active-data/postgres" "$TEST_ROOT/active-data/storage"
printf 'active sentinel\n' > "$TEST_ROOT/active-data/storage/sentinel.txt"
ACTIVE_SENTINEL_HASH="$(sha256_file "$TEST_ROOT/active-data/storage/sentinel.txt")"
export TEST_ROOT ACTIVE_SENTINEL_HASH FAKE_DOCKER_LOG="$TEST_ROOT/docker.log"

cat > "$TEST_ROOT/bin/docker" <<'FAKE_DOCKER'
#!/usr/bin/env bash
set -euo pipefail
printf 'project=%s network=%s data=%s args=%s\n' \
  "${COMPOSE_PROJECT_NAME:-}" "${DOCKER_NETWORK:-}" "${DATA_DIR:-}" "$*" >> "$FAKE_DOCKER_LOG"

if [ "${1:-}" = network ] && [ "${2:-}" = inspect ]; then
  exit 1
fi

case " $* " in
  *" config --environment "*)
    printf '%s\n' \
      "COMPOSE_PROJECT_NAME=${COMPOSE_PROJECT_NAME:-effchat}" \
      "DOCKER_NETWORK=${DOCKER_NETWORK:-effchat_net}" \
      "DATA_DIR=${DATA_DIR:-$TEST_ROOT/active-data}" \
      "WEB_PORT=${WEB_PORT:-8088}" \
      "BACKEND_PORT=${BACKEND_PORT:-18080}" \
      'POSTGRES_USER=effchat' \
      'POSTGRES_DB=effchat' \
      'POSTGRES_PASSWORD=fixture-password' \
      'JWT_SECRET=fixture-jwt'
    ;;
  *" config "*)
    if [ -n "${FAKE_CONFIG_READY_FILE:-}" ]; then
      : > "$FAKE_CONFIG_READY_FILE"
      while [ ! -f "${FAKE_CONFIG_RELEASE_FILE:?}" ]; do sleep 0.01; done
    fi
    ;;
  *" ps -q "*) ;;
  *" up -d --no-build --wait postgres "*) ;;
  *" exec -T postgres postgres --version "*)
    printf 'postgres (PostgreSQL) %s.5\n' "${FAKE_POSTGRES_MAJOR:-17}"
    ;;
  *" exec -T postgres sh -ec "*"SELECT count(*) FROM pg_class "*)
    printf '0\n'
    ;;
  *" exec -T postgres sh -ec "*"pg_restore "*)
    cat >/dev/null
    if [ "${FAKE_PG_RESTORE_FAIL:-0}" = 1 ]; then
      echo "fixture pg_restore failed" >&2
      exit 44
    fi
    ;;
  *" exec -T postgres sh -ec "*"SELECT version, checksum FROM schema_migrations "*)
    if [ "${FAKE_LEDGER_DRIFT:-0}" = 1 ]; then
      printf '051_fixture.sql\t%s\n' 'cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc'
    else
      printf '051_fixture.sql\t%s\n' 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'
    fi
    ;;
  *" run --rm --no-deps migrate "*) ;;
  *" exec -T postgres sh -ec "*"psql -v ON_ERROR_STOP=1 -At -U "*)
    cat >/dev/null
    if [ "${FAKE_MISSING_REFERENCE:-0}" = 1 ]; then
      printf 'storage/attachments/originals/1/missing.txt\n'
    else
      printf 'storage/attachments/originals/1/example.txt\n'
    fi
    ;;
  *" up -d --no-build --wait "*) ;;
  *" ps --status running --services "*)
    printf '%s\n' postgres py-extractor backend web
    ;;
  *" exec -T postgres sh -ec "*"pg_isready "*) ;;
  *" exec -T py-extractor python -c "*) ;;
  *" exec -T backend wget "*)
    printf '%s\n' '{"status":"ok","version":"pre-release 0.3.4","build_ref":"fixture-build","schema_version":"051_fixture.sql"}'
    ;;
  *" exec -T web wget "*) ;;
  *" port web 80 "*) printf '127.0.0.1:49180\n' ;;
  *" port backend 8080 "*) printf '127.0.0.1:49181\n' ;;
  *" down --remove-orphans "*) ;;
  *)
    printf 'Unexpected fake docker call: %s\n' "$*" >&2
    exit 1
    ;;
esac
FAKE_DOCKER
chmod +x "$TEST_ROOT/bin/docker"

printf '%s\n' \
  'COMPOSE_PROJECT_NAME=effchat' \
  'DOCKER_NETWORK=effchat_net' \
  "DATA_DIR=$TEST_ROOT/active-data" \
  'WEB_PORT=8088' \
  'BACKEND_PORT=18080' \
  'POSTGRES_USER=effchat' \
  'POSTGRES_DB=effchat' \
  'POSTGRES_PASSWORD=fixture-password' \
  'JWT_SECRET=fixture-jwt' \
  > "$TEST_ROOT/.env"

make_backup() {
  local backup="$1" fixture="$TEST_ROOT/storage-fixture" storage_hash
  rm -rf -- "$fixture"
  mkdir -p "$backup" "$fixture/storage/attachments/originals/1"
  printf 'restored attachment\n' > "$fixture/storage/attachments/originals/1/example.txt"
  printf 'fake custom dump\n' > "$backup/database.dump"
  tar -C "$fixture" -cf "$backup/storage.tar" storage
  storage_hash="$(sha256_file "$fixture/storage/attachments/originals/1/example.txt")"
  printf 'attachments/originals/1/example.txt\t%s\n' "$storage_hash" > "$backup/storage.manifest.tsv"
  printf '051_fixture.sql\t%s\n' 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb' \
    > "$backup/migrations.manifest.tsv"
  {
    printf 'format=effchat-backup-v1\n'
    printf 'created_at=2026-08-07T00:00:00Z\n'
    printf 'app_version=pre-release 0.3.4\n'
    printf 'postgres_major=17\n'
    printf 'schema=051_fixture.sql\n'
    printf 'build_ref=fixture-build\n'
    printf 'compose_sha256=%s\n' "$(sha256_file "$ROOT/docker-compose.yml")"
    printf 'database_dump=database.dump\n'
    printf 'database_dump_sha256=%s\n' "$(sha256_file "$backup/database.dump")"
    printf 'storage_archive=storage.tar\n'
    printf 'storage_archive_sha256=%s\n' "$(sha256_file "$backup/storage.tar")"
    printf 'storage_manifest=storage.manifest.tsv\n'
    printf 'storage_manifest_sha256=%s\n' "$(sha256_file "$backup/storage.manifest.tsv")"
    printf 'storage_file_count=1\n'
    printf 'migration_manifest=migrations.manifest.tsv\n'
    printf 'migration_manifest_sha256=%s\n' "$(sha256_file "$backup/migrations.manifest.tsv")"
    printf 'migration_count=1\n'
  } > "$backup/manifest"
}

run_restore() {
  PATH="$TEST_ROOT/bin:$PATH" ENV_FILE="$TEST_ROOT/.env" \
    "$ROOT/scripts/backup-restore.sh" restore "$1" "$2" "$TEST_ROOT/.env"
}

backup="$TEST_ROOT/backup"
make_backup "$backup"
restore_root="$TEST_ROOT/restore-success"
output="$(run_restore "$backup" "$restore_root")"
grep -Fq 'Isolated restore is running: http://127.0.0.1:49180' <<< "$output"
grep -Fqx 'format=effchat-isolated-restore-v1' "$restore_root/restore-manifest"
grep -Fqx 'web_url=http://127.0.0.1:49180' "$restore_root/restore-manifest"
grep -Fqx 'schema=051_fixture.sql' "$restore_root/restore-manifest"
grep -Fqx 'unreferenced_storage_files=0' "$restore_root/restore-manifest"
test -f "$restore_root/data/storage/attachments/originals/1/example.txt"
grep -Eq 'project=effchat-restore-[0-9]+-[0-9]+ network=effchat-restore-[0-9]+-[0-9]+_net data=.*/restore-success/data args=.* up -d --no-build --wait' "$FAKE_DOCKER_LOG"
if rg -n 'down -v|down --volumes' "$FAKE_DOCKER_LOG" >/dev/null; then
  echo "Restore used a volume-destructive Compose command." >&2
  exit 1
fi
PATH="$TEST_ROOT/bin:$PATH" ENV_FILE="$TEST_ROOT/.env" \
  "$ROOT/scripts/backup-restore.sh" stop-restore "$restore_root" "$TEST_ROOT/.env" \
  > "$TEST_ROOT/stop-success.log"
grep -Fq 'Isolated restore stopped; data retained at' "$TEST_ROOT/stop-success.log"
grep -Fq ' down --remove-orphans' "$FAKE_DOCKER_LOG"
test -f "$restore_root/data/storage/attachments/originals/1/example.txt"

nonempty="$TEST_ROOT/nonempty"
mkdir -p "$nonempty"
printf 'keep\n' > "$nonempty/keep.txt"
if run_restore "$backup" "$nonempty" >"$TEST_ROOT/nonempty.log" 2>&1; then
  echo "Expected non-empty restore target rejection." >&2
  exit 1
fi
grep -Fq 'RESTORE_ROOT must be empty' "$TEST_ROOT/nonempty.log"
grep -Fqx 'keep' "$nonempty/keep.txt"

ln -s "$TEST_ROOT/active-data" "$TEST_ROOT/restore-link"
if run_restore "$backup" "$TEST_ROOT/restore-link" >"$TEST_ROOT/symlink.log" 2>&1; then
  echo "Expected restore symlink rejection." >&2
  exit 1
fi
grep -Fq 'RESTORE_ROOT must not be a symbolic link' "$TEST_ROOT/symlink.log"

newline_root="$TEST_ROOT/restore"$'\n''newline'
if run_restore "$backup" "$newline_root" >"$TEST_ROOT/newline.log" 2>&1; then
  echo "Expected restore path control-character rejection." >&2
  exit 1
fi
grep -Fq 'RESTORE_ROOT must not contain a tab or newline' "$TEST_ROOT/newline.log"

concurrent_root="$TEST_ROOT/concurrent-restore"
ready="$TEST_ROOT/concurrent.ready"
release="$TEST_ROOT/concurrent.release"
FAKE_CONFIG_READY_FILE="$ready" FAKE_CONFIG_RELEASE_FILE="$release" \
  run_restore "$backup" "$concurrent_root" >"$TEST_ROOT/concurrent-first.log" 2>&1 &
first_pid=$!
for _ in $(seq 1 200); do
  [ -f "$ready" ] && break
  sleep 0.01
done
if [ ! -f "$ready" ]; then
  echo "Timed out waiting for the first restore to acquire its target." >&2
  exit 1
fi
if run_restore "$backup" "$concurrent_root" >"$TEST_ROOT/concurrent-second.log" 2>&1; then
  echo "Expected concurrent restore target rejection." >&2
  exit 1
fi
grep -Fq 'RESTORE_ROOT must be empty' "$TEST_ROOT/concurrent-second.log"
: > "$release"
wait "$first_pid"
test -f "$concurrent_root/restore-manifest"

if run_restore "$backup" "$TEST_ROOT/active-data/restore" >"$TEST_ROOT/active.log" 2>&1; then
  echo "Expected active DATA_DIR overlap rejection." >&2
  exit 1
fi
grep -Fq 'RESTORE_ROOT must be isolated from the active DATA_DIR' "$TEST_ROOT/active.log"

failure_case() {
  local name="$1" expected="$2"
  shift 2
  : > "$FAKE_DOCKER_LOG"
  if (export "$@"; run_restore "$backup" "$TEST_ROOT/$name") >"$TEST_ROOT/$name.log" 2>&1; then
    echo "Expected restore failure: $name" >&2
    exit 1
  fi
  grep -Fq "$expected" "$TEST_ROOT/$name.log"
  test ! -e "$TEST_ROOT/$name" || test -z "$(find "$TEST_ROOT/$name" -mindepth 1 -print -quit)"
  grep -Fq ' down --remove-orphans' "$FAKE_DOCKER_LOG"
}

failure_case major-mismatch 'PostgreSQL major mismatch' FAKE_POSTGRES_MAJOR=16
failure_case ledger-drift 'Restored migration ledger does not match the backup' FAKE_LEDGER_DRIFT=1
failure_case pg-restore-failure 'fixture pg_restore failed' FAKE_PG_RESTORE_FAIL=1
failure_case missing-reference 'Restored database path is missing on disk' FAKE_MISSING_REFERENCE=1

tampered="$TEST_ROOT/tampered-storage"
cp -R "$backup" "$tampered"
mkdir -p "$TEST_ROOT/tampered-fixture/storage/attachments/originals/1"
printf 'restored attachment\n' > "$TEST_ROOT/tampered-fixture/storage/attachments/originals/1/example.txt"
printf 'unexpected\n' > "$TEST_ROOT/tampered-fixture/storage/attachments/originals/1/unexpected.txt"
tar -C "$TEST_ROOT/tampered-fixture" -cf "$tampered/storage.tar" storage
sed -i.bak "s/^storage_archive_sha256=.*/storage_archive_sha256=$(sha256_file "$tampered/storage.tar")/" "$tampered/manifest"
rm -f "$tampered/manifest.bak"
if run_restore "$tampered" "$TEST_ROOT/tampered-restore" >"$TEST_ROOT/tampered-restore.log" 2>&1; then
  echo "Expected extracted storage manifest mismatch." >&2
  exit 1
fi
grep -Fq 'Extracted storage does not match the backup manifest' "$TEST_ROOT/tampered-restore.log"

test "$(sha256_file "$TEST_ROOT/active-data/storage/sentinel.txt")" = "$ACTIVE_SENTINEL_HASH"
echo "Isolated restore contract checks passed."
