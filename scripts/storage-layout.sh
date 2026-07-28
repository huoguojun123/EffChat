#!/usr/bin/env bash
set -euo pipefail

SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
if [ "$(basename "$SRC_DIR")" = "src" ]; then
  DEPLOY_ROOT="$(cd "$SRC_DIR/.." && pwd -P)"
else
  DEPLOY_ROOT="$SRC_DIR"
fi
ENV_FILE="${ENV_FILE:-$DEPLOY_ROOT/.env.docker}"
COMPOSE=(docker compose --env-file "$ENV_FILE" -f "$SRC_DIR/docker-compose.yml")

if [ "${1:-}" = "-h" ] || [ "${1:-}" = "--help" ] || [ "${1:-}" = "help" ]; then
  cat <<'USAGE'
Usage:
  scripts/storage-layout.sh plan
  scripts/storage-layout.sh apply
  scripts/storage-layout.sh verify
  scripts/storage-layout.sh rollback
  CONFIRM_STORAGE_FINALIZE=DELETE_LEGACY_UPLOADS scripts/storage-layout.sh finalize
USAGE
  exit 0
fi

if [ ! -f "$ENV_FILE" ]; then
  echo "Missing deployment environment file: $ENV_FILE" >&2
  exit 1
fi

env_value() {
  local key="$1"
  awk -F= -v key="$key" '
    $0 !~ /^[[:space:]]*#/ && $1 == key {
      sub(/^[^=]*=/, "")
      gsub(/^[[:space:]]+|[[:space:]]+$/, "")
      print
      exit
    }
  ' "$ENV_FILE"
}

env_value_or() {
  local value
  value="$(env_value "$1")"
  if [ -n "$value" ]; then
    printf '%s\n' "$value"
  else
    printf '%s\n' "$2"
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

DATA_ROOT="$(data_dir)"
LEGACY_ROOT="$DATA_ROOT/uploads"
STORAGE_ROOT="$DATA_ROOT/storage"
MARKER="$STORAGE_ROOT/.layout-v1"
BACKUP_ROOT="$DATA_ROOT/storage-migration-backups"
DB_USER="$(env_value_or POSTGRES_USER effchat)"
DB_NAME="$(env_value_or POSTGRES_DB effchat)"
WRITERS_STOPPED=0
DB_REWRITTEN=0
RESTORE_SQL=""

resume_legacy_on_error() {
  local status=$?
  trap - ERR
  set +e
  if [ "$DB_REWRITTEN" -eq 1 ] && [ -n "$RESTORE_SQL" ] && [ -f "$RESTORE_SQL" ]; then
    db_psql -f - < "$RESTORE_SQL" >/dev/null
    echo "Storage migration failed; database paths were restored." >&2
  fi
  if [ "$WRITERS_STOPPED" -eq 1 ]; then
    "${COMPOSE[@]}" start backend web >/dev/null 2>&1
  fi
  exit "$status"
}

trap resume_legacy_on_error ERR

db_psql() {
  "${COMPOSE[@]}" exec -T postgres psql -v ON_ERROR_STOP=1 -U "$DB_USER" -d "$DB_NAME" "$@"
}

ensure_postgres() {
  if "${COMPOSE[@]}" exec -T postgres pg_isready -U "$DB_USER" -d "$DB_NAME" >/dev/null 2>&1; then
    return
  fi
  "${COMPOSE[@]}" up -d --wait postgres >/dev/null
}

prepare_storage() {
  mkdir -p \
    "$STORAGE_ROOT/attachments/originals" \
    "$STORAGE_ROOT/attachments/extracted" \
    "$STORAGE_ROOT/attachments/ocr-staging" \
    "$STORAGE_ROOT/avatars" \
    "$STORAGE_ROOT/fonts" \
    "$STORAGE_ROOT/skills" \
    "$BACKUP_ROOT"
}

legacy_file_count() {
  if [ -d "$LEGACY_ROOT" ]; then
    find "$LEGACY_ROOT" -type f ! -name .DS_Store | wc -l | tr -d ' '
  else
    printf '0\n'
  fi
}

storage_file_count() {
  if [ -d "$STORAGE_ROOT" ]; then
    find "$STORAGE_ROOT" -type f ! -name .layout-v1 | wc -l | tr -d ' '
  else
    printf '0\n'
  fi
}

legacy_db_count() {
  db_psql -Atc "
    SELECT
      (SELECT count(*) FROM files
       WHERE file_path ~ '^\\.?/?uploads/'
          OR extracted_text_path ~ '^\\.?/?uploads/'
          OR ocr_source_path ~ '^\\.?/?uploads/')
      + (SELECT count(*) FROM font_assets WHERE file_path ~ '^\\.?/?uploads/')
      + (SELECT count(*) FROM skill_files WHERE storage_path ~ '^\\.?/?uploads/');
  "
}

validate_legacy_tree() {
  [ -d "$LEGACY_ROOT" ] || return 0
  if find "$LEGACY_ROOT" -type l -print -quit | grep -q .; then
    echo "Legacy uploads contains a symbolic link; refusing migration." >&2
    return 1
  fi
  local path name
  for path in "$LEGACY_ROOT"/* "$LEGACY_ROOT"/.[!.]* "$LEGACY_ROOT"/..?*; do
    [ -e "$path" ] || continue
    name="$(basename "$path")"
    case "$name" in
      .DS_Store|avatars|extracted|fonts|skills) ;;
      *)
        if [ ! -d "$path" ] || ! [[ "$name" =~ ^[0-9]+$ ]]; then
          echo "Unknown legacy upload entry: $path" >&2
          return 1
        fi
        ;;
    esac
  done
}

copy_tree() {
  local source="$1" target="$2"
  [ -d "$source" ] || return 0
  mkdir -p "$target"
  cp -Rp "$source/." "$target/"
}

compare_tree() {
  local source="$1" target="$2" file rel
  [ -d "$source" ] || return 0
  while IFS= read -r -d '' file; do
    rel="${file#"$source"/}"
    if [ ! -f "$target/$rel" ] || ! cmp -s "$file" "$target/$rel"; then
      echo "Copied file verification failed: $file" >&2
      return 1
    fi
  done < <(find "$source" -type f ! -name .DS_Store -print0)
}

copy_legacy_tree() {
  [ -d "$LEGACY_ROOT" ] || return 0
  copy_tree "$LEGACY_ROOT/extracted" "$STORAGE_ROOT/attachments/extracted"
  copy_tree "$LEGACY_ROOT/avatars" "$STORAGE_ROOT/avatars"
  copy_tree "$LEGACY_ROOT/fonts" "$STORAGE_ROOT/fonts"
  copy_tree "$LEGACY_ROOT/skills" "$STORAGE_ROOT/skills"

  local path name
  for path in "$LEGACY_ROOT"/*; do
    [ -d "$path" ] || continue
    name="$(basename "$path")"
    if [[ "$name" =~ ^[0-9]+$ ]]; then
      copy_tree "$path" "$STORAGE_ROOT/attachments/originals/$name"
    fi
  done

  compare_tree "$LEGACY_ROOT/extracted" "$STORAGE_ROOT/attachments/extracted"
  compare_tree "$LEGACY_ROOT/avatars" "$STORAGE_ROOT/avatars"
  compare_tree "$LEGACY_ROOT/fonts" "$STORAGE_ROOT/fonts"
  compare_tree "$LEGACY_ROOT/skills" "$STORAGE_ROOT/skills"
  for path in "$LEGACY_ROOT"/*; do
    [ -d "$path" ] || continue
    name="$(basename "$path")"
    if [[ "$name" =~ ^[0-9]+$ ]]; then
      compare_tree "$path" "$STORAGE_ROOT/attachments/originals/$name"
    fi
  done
}

copy_ocr_sources() {
  local old_path rel source target original_copy old_paths
  old_paths="$(db_psql -Atc "SELECT ocr_source_path FROM files WHERE ocr_source_path ~ '^\\.?/?uploads/' ORDER BY id;")"
  while IFS= read -r old_path; do
    [ -n "$old_path" ] || continue
    rel="${old_path#./}"
    rel="${rel#uploads/}"
    if [ "$rel" = "$old_path" ]; then
      continue
    fi
    source="$LEGACY_ROOT/$rel"
    target="$STORAGE_ROOT/attachments/ocr-staging/$rel"
    if [ ! -f "$source" ]; then
      echo "OCR source is missing before migration: $old_path" >&2
      return 1
    fi
    mkdir -p "$(dirname "$target")"
    cp -p "$source" "$target"
    if ! cmp -s "$source" "$target"; then
      echo "OCR source verification failed: $old_path" >&2
      return 1
    fi
    original_copy="$STORAGE_ROOT/attachments/originals/$rel"
    if [ -f "$original_copy" ] && cmp -s "$source" "$original_copy"; then
      rm -f "$original_copy"
    fi
  done <<< "$old_paths"
}

write_restore_sql() {
  local target="$1"
  {
    printf 'BEGIN;\n'
    db_psql -Atc "
      SELECT format(
        'UPDATE files SET file_path = %L, extracted_text_path = %L, ocr_source_path = %L WHERE id = %s;',
        file_path, extracted_text_path, ocr_source_path, id
      )
      FROM files
      WHERE file_path ~ '^\\.?/?uploads/'
         OR extracted_text_path ~ '^\\.?/?uploads/'
         OR ocr_source_path ~ '^\\.?/?uploads/'
      ORDER BY id;
      SELECT format('UPDATE font_assets SET file_path = %L WHERE id = %s;', file_path, id)
      FROM font_assets WHERE file_path ~ '^\\.?/?uploads/' ORDER BY id;
      SELECT format(
        'UPDATE skill_files SET storage_path = %L WHERE skill_id = %L AND relative_path = %L;',
        storage_path, skill_id, relative_path
      )
      FROM skill_files WHERE storage_path ~ '^\\.?/?uploads/' ORDER BY skill_id, relative_path;
    "
    printf 'COMMIT;\n'
  } > "$target"
}

rewrite_database_paths() {
  db_psql <<'SQL'
BEGIN;
SELECT pg_advisory_xact_lock(823764220);

UPDATE files
SET file_path = CASE
      WHEN file_path ~ '^(\./)?uploads/extracted/' THEN
        regexp_replace(file_path, '^(\./)?uploads/extracted/', 'storage/attachments/extracted/')
      WHEN file_path ~ '^(\./)?uploads/[0-9]+/' THEN
        regexp_replace(file_path, '^(\./)?uploads/', 'storage/attachments/originals/')
      ELSE file_path
    END,
    extracted_text_path = CASE
      WHEN extracted_text_path ~ '^(\./)?uploads/extracted/' THEN
        regexp_replace(extracted_text_path, '^(\./)?uploads/extracted/', 'storage/attachments/extracted/')
      WHEN extracted_text_path ~ '^(\./)?uploads/[0-9]+/' THEN
        regexp_replace(extracted_text_path, '^(\./)?uploads/', 'storage/attachments/originals/')
      ELSE extracted_text_path
    END,
    ocr_source_path = CASE
      WHEN ocr_source_path ~ '^(\./)?uploads/' THEN
        regexp_replace(ocr_source_path, '^(\./)?uploads/', 'storage/attachments/ocr-staging/')
      ELSE ocr_source_path
    END
WHERE file_path ~ '^(\./)?uploads/'
   OR extracted_text_path ~ '^(\./)?uploads/'
   OR ocr_source_path ~ '^(\./)?uploads/';

UPDATE font_assets
SET file_path = regexp_replace(file_path, '^(\./)?uploads/fonts/', 'storage/fonts/')
WHERE file_path ~ '^(\./)?uploads/fonts/';

UPDATE skill_files
SET storage_path = regexp_replace(storage_path, '^(\./)?uploads/skills/', 'storage/skills/')
WHERE storage_path ~ '^(\./)?uploads/skills/';

COMMIT;
SQL
}

verify_layout() {
  local legacy_count managed_paths font_paths missing=0 path host_path
  legacy_count="$(legacy_db_count | tr -d '[:space:]')"
  if [ "$legacy_count" != "0" ]; then
    echo "Database still contains $legacy_count legacy storage path(s)." >&2
    return 1
  fi

  managed_paths="$(db_psql -Atc "
    SELECT path
    FROM (
      SELECT file_path AS path FROM files WHERE status <> 'storage_removed'
      UNION
      SELECT extracted_text_path FROM files WHERE status <> 'storage_removed' AND extracted_text_path IS NOT NULL
      UNION
      SELECT ocr_source_path FROM files WHERE status <> 'storage_removed' AND ocr_source_path IS NOT NULL
      UNION
      SELECT storage_path FROM skill_files
      UNION
      SELECT 'storage/avatars/' || substring(avatar_url FROM '^/api/v1/avatars/(.+)$')
      FROM users
      WHERE avatar_url ~ '^/api/v1/avatars/[0-9a-fA-F-]+\.(jpg|png|gif)$'
    ) managed_paths
    WHERE path IS NOT NULL AND path <> ''
    ORDER BY path;
  ")"
  while IFS= read -r path; do
    [ -n "$path" ] || continue
    case "$path" in
      storage/*) ;;
      *)
        echo "Database path is outside managed storage: $path" >&2
        missing=$((missing + 1))
        continue
        ;;
    esac
    host_path="$DATA_ROOT/$path"
    if [ ! -f "$host_path" ]; then
      echo "Database path is missing on disk: $path" >&2
      missing=$((missing + 1))
    fi
  done <<< "$managed_paths"

  if [ "$missing" -ne 0 ]; then
    echo "Storage verification failed with $missing invalid or missing path(s)." >&2
    return 1
  fi

  font_paths="$(db_psql -Atc "SELECT file_path FROM font_assets WHERE file_path IS NOT NULL AND file_path <> '' ORDER BY file_path;")"
  while IFS= read -r path; do
    [ -n "$path" ] || continue
    case "$path" in
      storage/*) host_path="$DATA_ROOT/$path" ;;
      *)
        echo "Warning: font asset path is outside managed storage: $path" >&2
        continue
        ;;
    esac
    if [ ! -f "$host_path" ]; then
      echo "Warning: font asset file is missing and remains unavailable: $path" >&2
    fi
  done <<< "$font_paths"
}

prune_unreferenced_storage() {
  local manifest path relative removed=0
  manifest="$(mktemp)"

  db_psql -Atc "
    SELECT path
    FROM (
      SELECT file_path AS path FROM files WHERE status <> 'storage_removed'
      UNION
      SELECT extracted_text_path FROM files
        WHERE status <> 'storage_removed' AND extracted_text_path IS NOT NULL
      UNION
      SELECT ocr_source_path FROM files
        WHERE status <> 'storage_removed' AND ocr_source_path IS NOT NULL
      UNION
      SELECT file_path FROM font_assets
      UNION
      SELECT storage_path FROM skill_files
      UNION
      SELECT 'storage/avatars/' || substring(avatar_url FROM '^/api/v1/avatars/(.+)$')
      FROM users
      WHERE avatar_url ~ '^/api/v1/avatars/[0-9a-fA-F-]+\\.(jpg|png|gif)$'
    ) managed_paths
    WHERE path ~ '^\\.?/?storage/'
    ORDER BY path;
  " | sed 's#^\./##' > "$manifest"

  while IFS= read -r -d '' path; do
    relative="${path#"$DATA_ROOT"/}"
    if ! grep -Fqx -- "$relative" "$manifest"; then
      rm -f -- "$path"
      removed=$((removed + 1))
    fi
  done < <(find "$STORAGE_ROOT" -type f ! -path "$MARKER" -print0)

  rm -f "$manifest"
  printf 'Unreferenced storage files removed: %s\n' "$removed"
}

plan() {
  local database_paths
  ensure_postgres
  validate_legacy_tree
  database_paths="$(legacy_db_count | tr -d '[:space:]')"
  printf 'data_root=%s\n' "$DATA_ROOT"
  printf 'legacy_root=%s\n' "$LEGACY_ROOT"
  printf 'storage_root=%s\n' "$STORAGE_ROOT"
  printf 'legacy_files=%s\n' "$(legacy_file_count)"
  printf 'storage_files=%s\n' "$(storage_file_count)"
  printf 'legacy_database_paths=%s\n' "$database_paths"
  if [ -f "$MARKER" ]; then
    printf 'status=migrated\n'
  else
    printf 'status=pending\n'
  fi
}

apply_layout() {
  ensure_postgres
  "${COMPOSE[@]}" stop web backend >/dev/null 2>&1 || true
  WRITERS_STOPPED=1
  prepare_storage
  if [ -f "$MARKER" ]; then
    verify_layout
    prune_unreferenced_storage
    verify_layout
    WRITERS_STOPPED=0
    echo "Storage layout is already migrated."
    return
  fi
  validate_legacy_tree

  local timestamp backup_dir restore_sql
  timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
  backup_dir="$BACKUP_ROOT/$timestamp"
  restore_sql="$backup_dir/restore-paths.sql"
  RESTORE_SQL="$restore_sql"
  mkdir -p "$backup_dir"

  copy_legacy_tree
  copy_ocr_sources
  write_restore_sql "$restore_sql"
  rewrite_database_paths
  DB_REWRITTEN=1
  if ! verify_layout; then
    db_psql -f - < "$restore_sql"
    DB_REWRITTEN=0
    echo "Storage migration failed verification; database paths were restored." >&2
    return 1
  fi
  prune_unreferenced_storage
  verify_layout

  {
    printf 'layout=v1\n'
    printf 'migrated_at_epoch=%s\n' "$(date +%s)"
    printf 'legacy_root=%s\n' "$LEGACY_ROOT"
    printf 'restore_sql=%s\n' "$restore_sql"
  } > "$MARKER"
  DB_REWRITTEN=0
  WRITERS_STOPPED=0
  echo "Storage migration completed. Legacy uploads were retained at $LEGACY_ROOT."
}

rollback_layout() {
  ensure_postgres
  "${COMPOSE[@]}" stop web backend >/dev/null 2>&1 || true
  WRITERS_STOPPED=1
  if [ ! -f "$MARKER" ]; then
    echo "Storage layout marker not found." >&2
    return 1
  fi
  local restore_sql
  restore_sql="$(awk -F= '$1 == "restore_sql" {sub(/^[^=]*=/, ""); print; exit}' "$MARKER")"
  if [ -z "$restore_sql" ] || [ ! -f "$restore_sql" ]; then
    echo "Restore SQL is missing." >&2
    return 1
  fi
  db_psql -f - < "$restore_sql"
  rm -f "$MARKER"
  WRITERS_STOPPED=0
  echo "Database paths were restored. Start the previous release before serving traffic."
}

finalize_layout() {
  ensure_postgres
  verify_layout
  if [ ! -f "$MARKER" ]; then
    echo "Storage layout marker not found." >&2
    return 1
  fi
  if [ "${CONFIRM_STORAGE_FINALIZE:-}" != "DELETE_LEGACY_UPLOADS" ]; then
    echo "Set CONFIRM_STORAGE_FINALIZE=DELETE_LEGACY_UPLOADS to remove legacy uploads." >&2
    return 1
  fi
  local migrated_at now
  migrated_at="$(awk -F= '$1 == "migrated_at_epoch" {print $2; exit}' "$MARKER")"
  now="$(date +%s)"
  if [ -z "$migrated_at" ] || [ $((now - migrated_at)) -lt 604800 ]; then
    echo "Legacy uploads must be retained for at least 7 days." >&2
    return 1
  fi
  rm -rf "$LEGACY_ROOT"
  echo "Legacy uploads removed after verification and retention."
}

usage() {
  cat <<'USAGE'
Usage:
  scripts/storage-layout.sh plan
  scripts/storage-layout.sh apply
  scripts/storage-layout.sh verify
  scripts/storage-layout.sh rollback
  CONFIRM_STORAGE_FINALIZE=DELETE_LEGACY_UPLOADS scripts/storage-layout.sh finalize
USAGE
}

case "${1:-plan}" in
  plan) plan ;;
  apply) apply_layout ;;
  verify)
    ensure_postgres
    verify_layout
    echo "Storage layout verification passed."
    ;;
  rollback) rollback_layout ;;
  finalize) finalize_layout ;;
  -h|--help|help) usage ;;
  *)
    usage
    exit 1
    ;;
esac
