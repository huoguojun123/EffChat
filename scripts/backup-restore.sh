#!/usr/bin/env bash
set -euo pipefail
umask 077

SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
if [ "$(basename "$SRC_DIR")" = "src" ]; then
  DEPLOY_ROOT="$(cd "$SRC_DIR/.." && pwd -P)"
else
  DEPLOY_ROOT="$SRC_DIR"
fi
ENV_FILE="${ENV_FILE:-$DEPLOY_ROOT/.env.docker}"
EXAMPLE_ENV="$DEPLOY_ROOT/.env.docker.example"
COMPOSE=(docker compose --env-file "$ENV_FILE" -f "$SRC_DIR/docker-compose.yml")
# shellcheck source=compose-env.sh
source "$SRC_DIR/scripts/compose-env.sh"

usage() {
  cat <<'USAGE'
Usage:
  scripts/backup-restore.sh backup
  scripts/backup-restore.sh verify BACKUP_DIR
  scripts/backup-restore.sh restore BACKUP_DIR RESTORE_ROOT RESTORE_ENV_FILE
  scripts/backup-restore.sh stop-restore RESTORE_ROOT RESTORE_ENV_FILE

Environment:
  ENV_FILE=/path/to/.env.docker       Active deployment environment
  BACKUP_ROOT=/path/to/backups        Override the backup output directory

Backup artifacts never contain `.env.docker` or deployment secrets. Restore
always creates an isolated Compose project in an empty target and leaves it
running for application-level acceptance.
USAGE
}

require_env_file() {
  if [ ! -f "$ENV_FILE" ]; then
    echo "Missing $ENV_FILE" >&2
    echo "Create it with: cp $EXAMPLE_ENV $ENV_FILE" >&2
    exit 1
  fi
}

data_dir() {
  local value
  value="$(env_value DATA_DIR)"
  if [ -z "$value" ]; then
    if [ "$(basename "$SRC_DIR")" = "src" ]; then
      value="../data"
    else
      value="./data"
    fi
  fi
  case "$value" in
    /*) printf '%s\n' "$value" ;;
    *) printf '%s/%s\n' "$SRC_DIR" "$value" ;;
  esac
}

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  elif command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 "$1" | awk '{print $NF}'
  else
    echo "A SHA-256 command is required." >&2
    return 1
  fi
}

sha256_path_manifest() {
  local root="$1" output="$2" file relative unsorted
  unsorted="${output}.unsorted"
  : > "$unsorted"
  while IFS= read -r -d '' file; do
    relative="${file#"$root"/}"
    printf '%s\t%s\n' "$relative" "$(sha256_file "$file")" >> "$unsorted"
  done < <(find "$root" -type f ! -name .layout-v1 -print0)
  LC_ALL=C sort "$unsorted" > "$output"
  rm -f -- "$unsorted"
}

validate_storage_tree() {
  local root="$1" path relative
  while IFS= read -r -d '' path; do
    relative="${path#"$root"/}"
    case "$relative" in
      *$'\n'*|*$'\t'*)
        echo "Storage path contains a tab or newline; refusing backup." >&2
        return 1
        ;;
    esac
    if [ -L "$path" ]; then
      echo "Managed storage contains a symbolic link; refusing backup." >&2
      return 1
    fi
  done < <(find "$root" -mindepth 1 -print0)
}

prepare_backup_root() {
  local requested="$1" parent name resolved
  if [ -e "$requested" ]; then
    [ -d "$requested" ] || { echo "BACKUP_ROOT is not a directory: $requested" >&2; return 1; }
    resolved="$(cd "$requested" && pwd -P)"
  else
    parent="$(dirname "$requested")"
    name="$(basename "$requested")"
    [ -d "$parent" ] || {
      echo "BACKUP_ROOT parent must already exist: $parent" >&2
      return 1
    }
    parent="$(cd "$parent" && pwd -P)"
    resolved="$parent/$name"
  fi
  [ "$resolved" != "/" ] || { echo "BACKUP_ROOT must not be the filesystem root." >&2; return 1; }
  printf '%s\n' "$resolved"
}

manifest_value() {
  local manifest="$1" key="$2"
  awk -F= -v key="$key" '
    $1 == key { count++; value = substr($0, length($1) + 2) }
    END { if (count != 1 || value == "") exit 1; print value }
  ' "$manifest"
}

write_active_migration_manifest() {
  local output="$1"
  "${COMPOSE[@]}" exec -T postgres sh -ec \
    'PGPASSWORD="$POSTGRES_PASSWORD" psql -v ON_ERROR_STOP=1 -At -F "$(printf "\t")" -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT version, checksum FROM schema_migrations ORDER BY version;"' \
    > "$output"
}

ensure_safe_data_dir() {
  local dir="$1"
  if [ -z "$dir" ] || [ "$dir" = "/" ] || [ -L "$dir" ]; then
    echo "Refusing unsafe DATA_DIR: $dir" >&2
    return 1
  fi
  [ -d "$dir" ] || {
    echo "DATA_DIR does not exist: $dir" >&2
    return 1
  }
}

schema_version() {
  "${COMPOSE[@]}" exec -T postgres sh -ec \
    'PGPASSWORD="$POSTGRES_PASSWORD" psql -Atq -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1;"'
}

health_value() {
  local json="$1" key="$2"
  printf '%s\n' "$json" | sed -n \
    "s/.*\"$key\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p"
}

backup() {
  require_env_file
  local data_root backup_root requested_backup_root timestamp target temp db_dump storage_archive storage_manifest migration_manifest
  data_root="$(data_dir)"
  ensure_safe_data_dir "$data_root"
  [ -d "$data_root/storage" ] || {
    echo "Managed storage directory is missing: $data_root/storage" >&2
    return 1
  }
  [ ! -L "$data_root/storage" ] || {
    echo "Managed storage directory is a symbolic link; refusing backup." >&2
    return 1
  }

  validate_storage_tree "$data_root/storage"

  data_root="$(cd "$data_root" && pwd -P)"
  requested_backup_root="${BACKUP_ROOT:-$data_root/backups}"
  case "$requested_backup_root" in
    /*) ;;
    *) requested_backup_root="$DEPLOY_ROOT/$requested_backup_root" ;;
  esac
  backup_root="$(prepare_backup_root "$requested_backup_root")"
  case "$backup_root/" in
    "$data_root/postgres/"*|"$data_root/storage/"*)
      echo "BACKUP_ROOT must not be inside an active data directory." >&2
      return 1
      ;;
  esac
  mkdir -p "$backup_root"
  timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
  target="$backup_root/effchat-$timestamp"
  temp="$backup_root/.effchat-$timestamp.tmp.$$"
  [ ! -e "$target" ] && [ ! -e "$temp" ] || {
    echo "Backup target already exists: $target" >&2
    return 1
  }

  local stopped=0 status=0 health_json version build_ref lock_dir lock_owned=0
  local -a running_services=()
  lock_dir="$backup_root/.effchat-backup.lock"
  cleanup() {
    status=$?
    set +e
    if [ "$stopped" -eq 1 ] && [ "${#running_services[@]}" -gt 0 ]; then
      "${COMPOSE[@]}" start "${running_services[@]}" >/dev/null 2>&1 || status=1
    fi
    if [ -d "$temp" ]; then
      rm -rf -- "$temp"
    fi
    if [ "$lock_owned" -eq 1 ]; then
      rmdir -- "$lock_dir" >/dev/null 2>&1 || status=1
    fi
    exit "$status"
  }
  trap cleanup EXIT
  if ! mkdir "$lock_dir"; then
    echo "Another backup is active or left a stale lock: $lock_dir" >&2
    return 1
  fi
  lock_owned=1
  mkdir -p "$temp"
  db_dump="$temp/database.dump"
  storage_archive="$temp/storage.tar"
  storage_manifest="$temp/storage.manifest.tsv"
  migration_manifest="$temp/migrations.manifest.tsv"

  health_json="$("${COMPOSE[@]}" exec -T backend wget -Y off -qO- http://127.0.0.1:8080/health)"
  version="$(health_value "$health_json" version)"
  build_ref="$(health_value "$health_json" build_ref)"
  [ -n "$version" ] && [ -n "$build_ref" ] || {
    echo "Backend health response is missing version/build_ref." >&2
    return 1
  }

  while IFS= read -r service; do
    case "$service" in
      backend|web|py-extractor) running_services+=("$service") ;;
    esac
  done < <("${COMPOSE[@]}" ps --status running --services)
  [ "${#running_services[@]}" -gt 0 ] || {
    echo "No running application services were found." >&2
    return 1
  }

  # Stop the application writers gracefully. Pausing containers can freeze an
  # in-flight database/storage transaction halfway through and therefore does
  # not provide a clean cross-resource backup boundary.
  stopped=1
  "${COMPOSE[@]}" stop "${running_services[@]}"
  "${COMPOSE[@]}" exec -T postgres sh -ec \
    'PGPASSWORD="$POSTGRES_PASSWORD" pg_dump -Fc -U "$POSTGRES_USER" -d "$POSTGRES_DB"' > "$db_dump"
  tar -C "$data_root" -cf "$storage_archive" storage
  sha256_path_manifest "$data_root/storage" "$storage_manifest"
  write_active_migration_manifest "$migration_manifest"

  local schema postgres_major
  schema="$(schema_version | tr -d '[:space:]')"
  [ -n "$schema" ] || { echo "Schema version is empty." >&2; return 1; }
  postgres_major="$("${COMPOSE[@]}" exec -T postgres postgres --version | sed -n 's/.* \([0-9][0-9]*\)\..*/\1/p')"
  [ -n "$postgres_major" ] || { echo "PostgreSQL major version is unavailable." >&2; return 1; }
  {
    printf 'format=effchat-backup-v1\n'
    printf 'created_at=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    printf 'app_version=%s\n' "$version"
    printf 'postgres_major=%s\n' "$postgres_major"
    printf 'schema=%s\n' "$schema"
    printf 'build_ref=%s\n' "$build_ref"
    printf 'compose_sha256=%s\n' "$(sha256_file "$SRC_DIR/docker-compose.yml")"
    printf 'database_dump=database.dump\n'
    printf 'database_dump_sha256=%s\n' "$(sha256_file "$db_dump")"
    printf 'storage_archive=storage.tar\n'
    printf 'storage_archive_sha256=%s\n' "$(sha256_file "$storage_archive")"
    printf 'storage_manifest=storage.manifest.tsv\n'
    printf 'storage_manifest_sha256=%s\n' "$(sha256_file "$storage_manifest")"
    printf 'storage_file_count=%s\n' "$(wc -l < "$storage_manifest" | tr -d ' ')"
    printf 'migration_manifest=migrations.manifest.tsv\n'
    printf 'migration_manifest_sha256=%s\n' "$(sha256_file "$migration_manifest")"
    printf 'migration_count=%s\n' "$(wc -l < "$migration_manifest" | tr -d ' ')"
  } > "$temp/manifest"
  verify_backup "$temp" >/dev/null

  "${COMPOSE[@]}" start "${running_services[@]}"
  stopped=0
  rmdir -- "$lock_dir"
  lock_owned=0
  mv -- "$temp" "$target"
  temp=""
  trap - EXIT
  echo "Backup created: $target"
}

verify_backup() {
  local backup_dir="$1" manifest db_dump storage_archive storage_manifest migration_manifest expected actual expected_count actual_count entry relative
  [ -d "$backup_dir" ] || { echo "Backup directory is missing: $backup_dir" >&2; return 1; }
  manifest="$backup_dir/manifest"
  [ -f "$manifest" ] || { echo "Backup manifest is missing: $manifest" >&2; return 1; }
  [ "$(manifest_value "$manifest" format)" = 'effchat-backup-v1' ] || {
    echo "Unsupported backup format." >&2
    return 1
  }
  [ "$(manifest_value "$manifest" database_dump)" = 'database.dump' ] &&
    [ "$(manifest_value "$manifest" storage_archive)" = 'storage.tar' ] &&
    [ "$(manifest_value "$manifest" storage_manifest)" = 'storage.manifest.tsv' ] &&
    [ "$(manifest_value "$manifest" migration_manifest)" = 'migrations.manifest.tsv' ] || {
      echo "Backup manifest contains unsupported artifact names." >&2
      return 1
    }
  manifest_value "$manifest" created_at >/dev/null
  manifest_value "$manifest" app_version >/dev/null
  manifest_value "$manifest" build_ref >/dev/null
  manifest_value "$manifest" schema >/dev/null
  manifest_value "$manifest" postgres_major >/dev/null
  expected="$(manifest_value "$manifest" compose_sha256)"
  [[ "$expected" =~ ^[0-9a-f]{64}$ ]] || { echo "Compose checksum is malformed." >&2; return 1; }
  db_dump="$backup_dir/database.dump"
  storage_archive="$backup_dir/storage.tar"
  storage_manifest="$backup_dir/storage.manifest.tsv"
  migration_manifest="$backup_dir/migrations.manifest.tsv"
  [ -f "$db_dump" ] && [ -f "$storage_archive" ] && [ -f "$storage_manifest" ] && [ -f "$migration_manifest" ] || {
    echo "Backup artifacts are incomplete." >&2
    return 1
  }
  expected="$(manifest_value "$manifest" database_dump_sha256)"
  actual="$(sha256_file "$db_dump")"
  [ "$expected" = "$actual" ] || { echo "Database dump hash mismatch." >&2; return 1; }
  expected="$(manifest_value "$manifest" storage_archive_sha256)"
  actual="$(sha256_file "$storage_archive")"
  [ "$expected" = "$actual" ] || { echo "Storage archive hash mismatch." >&2; return 1; }
  expected="$(manifest_value "$manifest" storage_manifest_sha256)"
  actual="$(sha256_file "$storage_manifest")"
  [ "$expected" = "$actual" ] || { echo "Storage manifest hash mismatch." >&2; return 1; }
  expected_count="$(manifest_value "$manifest" storage_file_count)"
  actual_count="$(wc -l < "$storage_manifest" | tr -d ' ')"
  [ "$expected_count" = "$actual_count" ] || { echo "Storage manifest file count mismatch." >&2; return 1; }
  awk -F '\t' '
    NF != 2 || $1 == "" || $1 ~ /^\// || $1 ~ /(^|\/)\.\.?($|\/)/ || $2 !~ /^[0-9a-f]{64}$/ { exit 1 }
  ' "$storage_manifest" || {
    echo "Storage manifest is malformed." >&2
    return 1
  }
  expected="$(manifest_value "$manifest" migration_manifest_sha256)"
  actual="$(sha256_file "$migration_manifest")"
  [ "$expected" = "$actual" ] || { echo "Migration manifest hash mismatch." >&2; return 1; }
  expected_count="$(manifest_value "$manifest" migration_count)"
  actual_count="$(wc -l < "$migration_manifest" | tr -d ' ')"
  [ "$expected_count" = "$actual_count" ] || { echo "Migration manifest count mismatch." >&2; return 1; }
  awk -F '\t' 'NF != 2 || $1 == "" || $2 !~ /^[0-9a-f]{64}$/ { exit 1 }' "$migration_manifest" || {
    echo "Migration manifest is malformed." >&2
    return 1
  }
  tar -tvf "$storage_archive" | awk 'substr($1, 1, 1) != "-" && substr($1, 1, 1) != "d" { exit 1 }' || {
    echo "Storage archive contains a link or unsupported entry type." >&2
    return 1
  }
  while IFS= read -r entry; do
    case "$entry" in
      storage|storage/) continue ;;
      storage/*) relative="${entry#storage/}" ;;
      *) echo "Storage archive contains a path outside storage/." >&2; return 1 ;;
    esac
    case "/$relative/" in
      *'/../'*|*'/./'*)
        echo "Storage archive contains an unsafe path." >&2
        return 1
        ;;
    esac
  done < <(tar -tf "$storage_archive")
  echo "Backup verification passed: $backup_dir"
}

# shellcheck source=backup-restore/isolated-restore.sh
source "$SRC_DIR/scripts/backup-restore/isolated-restore.sh"

case "${1:-}" in
  backup) backup ;;
  verify)
    [ "$#" -eq 2 ] || { usage >&2; exit 1; }
    verify_backup "$2"
    ;;
  restore)
    [ "$#" -eq 4 ] || { usage >&2; exit 1; }
    RESTORE_ENV_FILE="$4"
    restore_backup "$2" "$3"
    ;;
  stop-restore)
    [ "$#" -eq 3 ] || { usage >&2; exit 1; }
    RESTORE_ENV_FILE="$3"
    stop_restore "$2"
    ;;
  -h|--help|help) usage ;;
  *) usage >&2; exit 1 ;;
esac
