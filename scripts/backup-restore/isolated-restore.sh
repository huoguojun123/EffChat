#!/usr/bin/env bash
# Isolated restore lifecycle. This file is sourced by backup-restore.sh after
# the shared manifest, path, Compose, and hashing helpers are defined.

RESTORE_ENV_FILE=""
RESTORE_PROJECT=""
RESTORE_NETWORK=""
RESTORE_DATA_ROOT=""
RESTORE_CLEANUP_ROOT=""
RESTORE_CLEANUP_ROOT_CREATED=0
RESTORE_STACK_OWNED=0
RESTORE_LOCK_DIR=""
RESTORE_LOCK_OWNED=0

restore_compose() {
  COMPOSE_PROJECT_NAME="$RESTORE_PROJECT" \
    DOCKER_NETWORK="$RESTORE_NETWORK" \
    DATA_DIR="$RESTORE_DATA_ROOT" \
    WEB_PORT=0 \
    BACKEND_PORT=0 \
    docker compose --env-file "$RESTORE_ENV_FILE" -f "$SRC_DIR/docker-compose.yml" "$@"
}

restore_env_value() {
  local key="$1"
  restore_compose config --environment | awk -v key="$key" '
    index($0, key "=") == 1 {
      print substr($0, length(key) + 2)
      exit
    }
  '
}

prepare_empty_restore_root() {
  local requested="$1" parent name resolved
  case "$requested" in
    *$'\n'*|*$'\t'*)
      echo "RESTORE_ROOT must not contain a tab or newline." >&2
      return 1
      ;;
  esac
  [ ! -L "$requested" ] || { echo "RESTORE_ROOT must not be a symbolic link." >&2; return 1; }
  if [ -e "$requested" ]; then
    [ -d "$requested" ] || { echo "RESTORE_ROOT is not a directory: $requested" >&2; return 1; }
    [ -z "$(find "$requested" -mindepth 1 -maxdepth 1 -print -quit)" ] || {
      echo "RESTORE_ROOT must be empty: $requested" >&2
      return 1
    }
    resolved="$(cd "$requested" && pwd -P)"
  else
    parent="$(dirname "$requested")"
    name="$(basename "$requested")"
    [ -d "$parent" ] || { echo "RESTORE_ROOT parent must already exist: $parent" >&2; return 1; }
    parent="$(cd "$parent" && pwd -P)"
    resolved="$parent/$name"
  fi
  [ "$resolved" != "/" ] || { echo "RESTORE_ROOT must not be the filesystem root." >&2; return 1; }
  printf '%s\n' "$resolved"
}

paths_overlap() {
  local left="$1" right="$2"
  [ "$left" = "$right" ] && return 0
  case "$left/" in "$right/"*) return 0 ;; esac
  case "$right/" in "$left/"*) return 0 ;; esac
  return 1
}

write_restore_migration_manifest() {
  local output="$1"
  restore_compose exec -T postgres sh -ec \
    'PGPASSWORD="$POSTGRES_PASSWORD" psql -v ON_ERROR_STOP=1 -At -F "$(printf "\t")" -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT version, checksum FROM schema_migrations ORDER BY version;"' \
    > "$output"
}

verify_restored_storage() {
  local restore_root="$1" backup_storage_manifest="$2" references relative path missing=0 orphan_count=0
  references="$restore_root/.restore-references"
  restore_compose exec -T postgres sh -ec \
    'PGPASSWORD="$POSTGRES_PASSWORD" psql -v ON_ERROR_STOP=1 -At -U "$POSTGRES_USER" -d "$POSTGRES_DB"' \
    > "$references" <<'SQL'
SELECT DISTINCT path
FROM (
  SELECT file_path AS path FROM files WHERE status <> 'storage_removed'
  UNION ALL
  SELECT extracted_text_path FROM files WHERE status <> 'storage_removed' AND extracted_text_path IS NOT NULL
  UNION ALL
  SELECT ocr_source_path FROM files WHERE status <> 'storage_removed' AND ocr_source_path IS NOT NULL
  UNION ALL
  SELECT file_path FROM font_assets WHERE file_path IS NOT NULL AND file_path <> ''
  UNION ALL
  SELECT storage_path FROM skill_files
  UNION ALL
  SELECT storage_path FROM skill_import_record_files
  UNION ALL
  SELECT 'storage/avatars/' || substring(avatar_url FROM '^/api/v1/avatars/(.+)$')
  FROM users
  WHERE avatar_url ~ '^/api/v1/avatars/[0-9a-fA-F-]+\.(jpg|png|gif)$'
) referenced_paths
WHERE path IS NOT NULL AND path <> ''
ORDER BY path;
SQL

  while IFS= read -r path; do
    [ -n "$path" ] || continue
    case "$path" in
      storage/*) relative="${path#storage/}" ;;
      *) echo "Restored database path is outside managed storage: $path" >&2; missing=$((missing + 1)); continue ;;
    esac
    case "/$relative/" in
      *'/../'*|*'/./'*)
        echo "Restored database contains an unsafe managed path: $path" >&2
        missing=$((missing + 1))
        continue
        ;;
    esac
    if [ ! -f "$RESTORE_DATA_ROOT/storage/$relative" ]; then
      echo "Restored database path is missing on disk: $path" >&2
      missing=$((missing + 1))
    fi
  done < "$references"
  [ "$missing" -eq 0 ] || { echo "Restored storage has $missing invalid or missing reference(s)." >&2; return 1; }

  sed 's#^storage/##' "$references" | LC_ALL=C sort -u > "$references.relative"
  while IFS=$'\t' read -r relative _; do
    if ! grep -Fqx -- "$relative" "$references.relative"; then
      orphan_count=$((orphan_count + 1))
    fi
  done < "$backup_storage_manifest"
  printf '%s\n' "$orphan_count"
}

restore_backup() {
  local backup_dir="$1" requested_root="$2" restore_root restore_root_created=0 active_data active_project active_network active_web_port active_backend_port
  local staging generated_manifest restored_ledger expected_major actual_major relation_count status=0
  local services health_json web_binding backend_binding web_url backend_url orphan_count compose_matches=false

  require_env_file
  [ -f "$RESTORE_ENV_FILE" ] || { echo "Restore environment file is missing: $RESTORE_ENV_FILE" >&2; return 1; }
  case "$backup_dir" in
    *$'\n'*|*$'\t'*)
      echo "BACKUP_DIR must not contain a tab or newline." >&2
      return 1
      ;;
  esac
  [ ! -L "$backup_dir" ] || { echo "BACKUP_DIR must not be a symbolic link." >&2; return 1; }
  backup_dir="$(cd "$backup_dir" && pwd -P)"
  verify_backup "$backup_dir" >/dev/null

  case "$requested_root" in
    /*) ;;
    *) requested_root="$DEPLOY_ROOT/$requested_root" ;;
  esac
  [ ! -e "$requested_root" ] && restore_root_created=1
  restore_root="$(prepare_empty_restore_root "$requested_root")"
  RESTORE_CLEANUP_ROOT="$restore_root"
  RESTORE_CLEANUP_ROOT_CREATED="$restore_root_created"
  RESTORE_STACK_OWNED=0
  RESTORE_LOCK_DIR="$restore_root/.effchat-restore.lock"
  RESTORE_LOCK_OWNED=0
  active_data="$(data_dir)"
  ensure_safe_data_dir "$active_data"
  active_data="$(cd "$active_data" && pwd -P)"
  if paths_overlap "$restore_root" "$active_data"; then
    echo "RESTORE_ROOT must be isolated from the active DATA_DIR." >&2
    return 1
  fi

  active_project="$(env_value COMPOSE_PROJECT_NAME)"
  active_network="$(env_value DOCKER_NETWORK)"
  active_web_port="$(env_value WEB_PORT)"
  active_backend_port="$(env_value BACKEND_PORT)"
  active_project="${active_project:-effchat}"
  active_network="${active_network:-effchat_net}"
  active_web_port="${active_web_port:-8088}"
  active_backend_port="${active_backend_port:-18080}"
  RESTORE_PROJECT="effchat-restore-$(date -u +%Y%m%d%H%M%S)-$$"
  RESTORE_NETWORK="${RESTORE_PROJECT}_net"
  RESTORE_DATA_ROOT="$restore_root/data"
  [ "$RESTORE_PROJECT" != "$active_project" ] && [ "$RESTORE_NETWORK" != "$active_network" ] || {
    echo "Restore project or network is not isolated from the active deployment." >&2
    return 1
  }

  cleanup_restore() {
    status=$?
    set +e
    if [ "$RESTORE_STACK_OWNED" -eq 1 ]; then
      restore_compose down --remove-orphans >/dev/null 2>&1
    fi
    rm -rf -- \
      "$RESTORE_CLEANUP_ROOT/data" \
      "$RESTORE_CLEANUP_ROOT/.restore-staging" \
      "$RESTORE_CLEANUP_ROOT/.restore-storage.manifest.tsv" \
      "$RESTORE_CLEANUP_ROOT/.restore-migrations.manifest.tsv" \
      "$RESTORE_CLEANUP_ROOT/.restore-references" \
      "$RESTORE_CLEANUP_ROOT/.restore-references.relative" \
      "$RESTORE_CLEANUP_ROOT/restore-manifest"
    if [ "$RESTORE_LOCK_OWNED" -eq 1 ]; then
      rmdir -- "$RESTORE_LOCK_DIR" >/dev/null 2>&1 || status=1
    fi
    if [ "$RESTORE_CLEANUP_ROOT_CREATED" -eq 1 ]; then
      rmdir -- "$RESTORE_CLEANUP_ROOT" >/dev/null 2>&1 || true
    fi
    exit "$status"
  }

  mkdir -p "$restore_root"
  if ! mkdir "$RESTORE_LOCK_DIR"; then
    echo "Another restore is active in RESTORE_ROOT: $restore_root" >&2
    return 1
  fi
  RESTORE_LOCK_OWNED=1
  trap cleanup_restore EXIT
  restore_compose config >/dev/null
  [ "$(restore_env_value COMPOSE_PROJECT_NAME)" = "$RESTORE_PROJECT" ] &&
    [ "$(restore_env_value DOCKER_NETWORK)" = "$RESTORE_NETWORK" ] &&
    [ "$(restore_env_value DATA_DIR)" = "$RESTORE_DATA_ROOT" ] &&
    [ "$(restore_env_value WEB_PORT)" = "0" ] &&
    [ "$(restore_env_value BACKEND_PORT)" = "0" ] || {
      echo "Compose did not preserve the isolated restore overrides." >&2
      return 1
    }
  [ -z "$(restore_compose ps -q)" ] || { echo "Restore Compose project already owns containers." >&2; return 1; }
  if docker network inspect "$RESTORE_NETWORK" >/dev/null 2>&1; then
    echo "Restore Docker network already exists: $RESTORE_NETWORK" >&2
    return 1
  fi

  staging="$restore_root/.restore-staging"
  mkdir -p "$staging"
  tar -xf "$backup_dir/storage.tar" -C "$staging"
  [ -d "$staging/storage" ] && [ ! -L "$staging/storage" ] || {
    echo "Restored storage root is missing or unsafe." >&2
    return 1
  }
  validate_storage_tree "$staging/storage"
  chmod -R go-rwx "$staging/storage"
  generated_manifest="$restore_root/.restore-storage.manifest.tsv"
  sha256_path_manifest "$staging/storage" "$generated_manifest"
  cmp -s "$backup_dir/storage.manifest.tsv" "$generated_manifest" || {
    echo "Extracted storage does not match the backup manifest." >&2
    return 1
  }
  mkdir -p "$RESTORE_DATA_ROOT/postgres"
  mv -- "$staging/storage" "$RESTORE_DATA_ROOT/storage"
  rmdir -- "$staging"

  RESTORE_STACK_OWNED=1
  restore_compose up -d --no-build --wait postgres
  expected_major="$(manifest_value "$backup_dir/manifest" postgres_major)"
  actual_major="$(restore_compose exec -T postgres postgres --version | sed -n 's/.* \([0-9][0-9]*\)\..*/\1/p')"
  [ "$expected_major" = "$actual_major" ] || {
    echo "PostgreSQL major mismatch: backup=$expected_major restore=$actual_major" >&2
    return 1
  }
  relation_count="$(restore_compose exec -T postgres sh -ec \
    'PGPASSWORD="$POSTGRES_PASSWORD" psql -Atq -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = '\''public'\'' AND c.relkind IN ('\''r'\'', '\''p'\'', '\''v'\'', '\''m'\'', '\''S'\'');"' | tr -d '[:space:]')"
  [ "$relation_count" = "0" ] || { echo "Restore database is not empty." >&2; return 1; }
  restore_compose exec -T postgres sh -ec \
    'PGPASSWORD="$POSTGRES_PASSWORD" pg_restore --exit-on-error --no-owner --no-privileges -U "$POSTGRES_USER" -d "$POSTGRES_DB"' \
    < "$backup_dir/database.dump"

  restored_ledger="$restore_root/.restore-migrations.manifest.tsv"
  write_restore_migration_manifest "$restored_ledger"
  cmp -s "$backup_dir/migrations.manifest.tsv" "$restored_ledger" || {
    echo "Restored migration ledger does not match the backup." >&2
    return 1
  }
  restore_compose run --rm --no-deps migrate
  orphan_count="$(verify_restored_storage "$restore_root" "$backup_dir/storage.manifest.tsv")"

  restore_compose up -d --no-build --wait
  services="$(restore_compose ps --status running --services)"
  for service in postgres py-extractor backend web; do
    printf '%s\n' "$services" | grep -Fqx "$service" || {
      echo "Restore service is not running: $service" >&2
      return 1
    }
  done
  restore_compose exec -T postgres sh -ec 'pg_isready -U "$POSTGRES_USER" -d "$POSTGRES_DB"' >/dev/null
  restore_compose exec -T py-extractor python -c \
    "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8090/health', timeout=2).read()"
  health_json="$(restore_compose exec -T backend wget -Y off -qO- http://127.0.0.1:8080/health)"
  restore_compose exec -T web wget -Y off -qO- http://127.0.0.1/health >/dev/null

  web_binding="$(restore_compose port web 80)"
  backend_binding="$(restore_compose port backend 8080)"
  [ -n "$web_binding" ] && [ -n "$backend_binding" ] || { echo "Dynamic restore ports are unavailable." >&2; return 1; }
  case "$web_binding" in 127.0.0.1:*) ;; *) echo "Restore web port is not loopback-only: $web_binding" >&2; return 1 ;; esac
  case "$backend_binding" in 127.0.0.1:*) ;; *) echo "Restore backend port is not loopback-only: $backend_binding" >&2; return 1 ;; esac
  [ "${web_binding##*:}" != "$active_web_port" ] && [ "${backend_binding##*:}" != "$active_backend_port" ] || {
    echo "Restore port allocation overlaps the active deployment." >&2
    return 1
  }
  web_url="http://$web_binding"
  backend_url="http://$backend_binding"
  [ -n "$(health_value "$health_json" version)" ] &&
    [ -n "$(health_value "$health_json" build_ref)" ] &&
    [ -n "$(health_value "$health_json" schema_version)" ] || {
      echo "Restored backend health response is missing version/build_ref/schema_version." >&2
      return 1
    }
  if [ "$(manifest_value "$backup_dir/manifest" compose_sha256)" = "$(sha256_file "$SRC_DIR/docker-compose.yml")" ]; then
    compose_matches=true
  fi
  {
    printf 'format=effchat-isolated-restore-v1\n'
    printf 'backup_dir=%s\n' "$backup_dir"
    printf 'compose_project=%s\n' "$RESTORE_PROJECT"
    printf 'docker_network=%s\n' "$RESTORE_NETWORK"
    printf 'data_dir=%s\n' "$RESTORE_DATA_ROOT"
    printf 'web_url=%s\n' "$web_url"
    printf 'backend_url=%s\n' "$backend_url"
    printf 'app_version=%s\n' "$(health_value "$health_json" version)"
    printf 'build_ref=%s\n' "$(health_value "$health_json" build_ref)"
    printf 'schema=%s\n' "$(health_value "$health_json" schema_version)"
    printf 'backup_compose_matches_current=%s\n' "$compose_matches"
    printf 'unreferenced_storage_files=%s\n' "$orphan_count"
  } > "$restore_root/restore-manifest"

  rm -f -- \
    "$generated_manifest" \
    "$restored_ledger" \
    "$restore_root/.restore-references" \
    "$restore_root/.restore-references.relative"
  rmdir -- "$RESTORE_LOCK_DIR"
  RESTORE_LOCK_OWNED=0

  trap - EXIT
  echo "Isolated restore is running: $web_url"
  echo "Restore manifest: $restore_root/restore-manifest"
  if [ "$orphan_count" != "0" ]; then
    echo "Warning: restored storage contains $orphan_count unreferenced file(s); review retention state before cleanup." >&2
  fi
}

stop_restore() {
  local requested_root="$1" restore_root manifest active_data active_project active_network
  [ -f "$RESTORE_ENV_FILE" ] || { echo "Restore environment file is missing: $RESTORE_ENV_FILE" >&2; return 1; }
  [ ! -L "$requested_root" ] || { echo "RESTORE_ROOT must not be a symbolic link." >&2; return 1; }
  [ -d "$requested_root" ] || { echo "RESTORE_ROOT is missing: $requested_root" >&2; return 1; }
  restore_root="$(cd "$requested_root" && pwd -P)"
  manifest="$restore_root/restore-manifest"
  [ -f "$manifest" ] || { echo "Restore manifest is missing: $manifest" >&2; return 1; }
  [ "$(manifest_value "$manifest" format)" = 'effchat-isolated-restore-v1' ] || {
    echo "Unsupported restore manifest." >&2
    return 1
  }
  RESTORE_PROJECT="$(manifest_value "$manifest" compose_project)"
  RESTORE_NETWORK="$(manifest_value "$manifest" docker_network)"
  RESTORE_DATA_ROOT="$(manifest_value "$manifest" data_dir)"
  [[ "$RESTORE_PROJECT" =~ ^effchat-restore-[0-9]{14}-[0-9]+$ ]] &&
    [ "$RESTORE_NETWORK" = "${RESTORE_PROJECT}_net" ] &&
    [ "$RESTORE_DATA_ROOT" = "$restore_root/data" ] || {
      echo "Restore manifest isolation fields are invalid." >&2
      return 1
    }

  require_env_file
  active_data="$(data_dir)"
  ensure_safe_data_dir "$active_data"
  active_data="$(cd "$active_data" && pwd -P)"
  active_project="$(env_value COMPOSE_PROJECT_NAME)"
  active_network="$(env_value DOCKER_NETWORK)"
  active_project="${active_project:-effchat}"
  active_network="${active_network:-effchat_net}"
  if paths_overlap "$restore_root" "$active_data" ||
    [ "$RESTORE_PROJECT" = "$active_project" ] ||
    [ "$RESTORE_NETWORK" = "$active_network" ]; then
    echo "Restore manifest overlaps the active deployment." >&2
    return 1
  fi
  restore_compose down --remove-orphans
  echo "Isolated restore stopped; data retained at $RESTORE_DATA_ROOT"
}
