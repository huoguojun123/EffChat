#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
TEST_ROOT="$(mktemp -d)"
trap 'rm -rf -- "$TEST_ROOT"' EXIT

mkdir -p \
  "$TEST_ROOT/bin" \
  "$TEST_ROOT/data/postgres" \
  "$TEST_ROOT/data/storage/attachments/originals/1" \
  "$TEST_ROOT/data/storage/fonts"
printf 'example attachment\n' > "$TEST_ROOT/data/storage/attachments/originals/1/example.txt"
printf 'example font\n' > "$TEST_ROOT/data/storage/fonts/example.woff2"

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
  *" exec -T backend wget "*)
    printf '%s\n' '{"status":"ok","version":"pre-release 0.3.4","build_ref":"fixture-build","schema_version":"051_fixture.sql"}'
    ;;
  *" ps --status running --services "*)
    printf '%s\n' backend web py-extractor
    ;;
  *" stop backend web py-extractor "*)
    if [ "${FAKE_STOP_FAIL:-0}" = 1 ]; then exit 43; fi
    ;;
  *" start backend web py-extractor "*) ;;
  *" exec -T postgres sh -ec "*"pg_dump "*)
    if [ "${FAKE_DUMP_FAIL:-0}" = 1 ]; then exit 42; fi
    printf 'fake custom-format dump\n'
    ;;
  *" exec -T postgres sh -ec "*"SELECT version, checksum FROM schema_migrations "*)
    printf '050_fixture.sql\t%s\n' 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
    printf '051_fixture.sql\t%s\n' 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'
    ;;
  *" exec -T postgres sh -ec "*"psql "*)
    printf '051_fixture.sql\n'
    ;;
  *" exec -T postgres postgres --version "*)
    printf 'postgres (PostgreSQL) 17.5\n'
    ;;
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

run_backup() {
  PATH="$TEST_ROOT/bin:$PATH" \
    ENV_FILE="$TEST_ROOT/.env" \
    BACKUP_ROOT="$1" \
    "$ROOT/scripts/backup-restore.sh" "${@:2}"
}

backup_root="$TEST_ROOT/backups"
output="$(run_backup "$backup_root" backup)"
backup_dir="${output#Backup created: }"
test -d "$backup_dir"
run_backup "$backup_root" verify "$backup_dir"

grep -Fqx 'format=effchat-backup-v1' "$backup_dir/manifest"
grep -Fqx 'app_version=pre-release 0.3.4' "$backup_dir/manifest"
grep -Fqx 'build_ref=fixture-build' "$backup_dir/manifest"
grep -Fqx 'schema=051_fixture.sql' "$backup_dir/manifest"
grep -Fqx 'postgres_major=17' "$backup_dir/manifest"
grep -Fqx 'storage_file_count=2' "$backup_dir/manifest"
grep -Eq '^storage_manifest_sha256=[0-9a-f]{64}$' "$backup_dir/manifest"
grep -Eq '^compose_sha256=[0-9a-f]{64}$' "$backup_dir/manifest"
grep -Fqx 'migration_count=2' "$backup_dir/manifest"
grep -Eq '^migration_manifest_sha256=[0-9a-f]{64}$' "$backup_dir/manifest"
grep -Fq $'attachments/originals/1/example.txt\t' "$backup_dir/storage.manifest.tsv"
grep -Fq $'fonts/example.woff2\t' "$backup_dir/storage.manifest.tsv"
if rg -n 'test-password|test-jwt' "$backup_dir" >/dev/null; then
  echo "Backup artifact leaked deployment secrets." >&2
  exit 1
fi

tampered="$TEST_ROOT/tampered"
cp -R "$backup_dir" "$tampered"
printf 'tampered\n' >> "$tampered/database.dump"
if run_backup "$backup_root" verify "$tampered" >"$TEST_ROOT/tampered.log" 2>&1; then
  echo "Expected tampered backup rejection." >&2
  exit 1
fi
grep -Fq 'Database dump hash mismatch' "$TEST_ROOT/tampered.log"

tampered_manifest="$TEST_ROOT/tampered-manifest"
cp -R "$backup_dir" "$tampered_manifest"
printf 'malformed\n' >> "$tampered_manifest/storage.manifest.tsv"
if run_backup "$backup_root" verify "$tampered_manifest" >"$TEST_ROOT/tampered-manifest.log" 2>&1; then
  echo "Expected tampered storage manifest rejection." >&2
  exit 1
fi
grep -Fq 'Storage manifest hash mismatch' "$TEST_ROOT/tampered-manifest.log"

locked_root="$TEST_ROOT/locked-backups"
mkdir -p "$locked_root/.effchat-backup.lock"
if run_backup "$locked_root" backup >"$TEST_ROOT/locked.log" 2>&1; then
  echo "Expected concurrent backup lock rejection." >&2
  exit 1
fi
test -d "$locked_root/.effchat-backup.lock"
grep -Fq 'Another backup is active or left a stale lock' "$TEST_ROOT/locked.log"
rmdir "$locked_root/.effchat-backup.lock"

unsafe_root="$TEST_ROOT/data/storage/backup-output"
if run_backup "$unsafe_root" backup >"$TEST_ROOT/unsafe-root.log" 2>&1; then
  echo "Expected active storage BACKUP_ROOT rejection." >&2
  exit 1
fi
test ! -e "$unsafe_root"
grep -Fq 'BACKUP_ROOT must not be inside an active data directory' "$TEST_ROOT/unsafe-root.log"

printf 'unsafe path\n' > "$TEST_ROOT/data/storage/attachments/unsafe"$'\n'"name.txt"
if run_backup "$backup_root" backup >"$TEST_ROOT/unsafe-path.log" 2>&1; then
  echo "Expected unsafe storage path rejection." >&2
  exit 1
fi
rm -f -- "$TEST_ROOT/data/storage/attachments/unsafe"$'\n'"name.txt"
grep -Fq 'Storage path contains a tab or newline' "$TEST_ROOT/unsafe-path.log"

failed_root="$TEST_ROOT/failed-backups"
: > "$FAKE_DOCKER_LOG"
if FAKE_DUMP_FAIL=1 run_backup "$failed_root" backup >"$TEST_ROOT/dump-failure.log" 2>&1; then
  echo "Expected database dump failure." >&2
  exit 1
fi
test -d "$failed_root"
test -z "$(find "$failed_root" -mindepth 1 -maxdepth 1 -print -quit)"
grep -Fq ' stop backend web py-extractor' "$FAKE_DOCKER_LOG"
grep -Fq ' start backend web py-extractor' "$FAKE_DOCKER_LOG"

: > "$FAKE_DOCKER_LOG"
if FAKE_STOP_FAIL=1 run_backup "$failed_root" backup >"$TEST_ROOT/stop-failure.log" 2>&1; then
  echo "Expected application stop failure." >&2
  exit 1
fi
grep -Fq ' stop backend web py-extractor' "$FAKE_DOCKER_LOG"
grep -Fq ' start backend web py-extractor' "$FAKE_DOCKER_LOG"

echo "Backup artifact contract checks passed."
