#!/bin/sh
set -eu

role="${1:-backend}"
if [ "$#" -gt 0 ]; then
  shift
fi

database_ready() {
  attempts="${DATABASE_READY_ATTEMPTS:-30}"
  delay="${DATABASE_READY_DELAY_SECONDS:-2}"
  case "$attempts:$delay" in
    *[!0-9:]*|:*|*:)
      echo "invalid database readiness configuration" >&2
      return 2
      ;;
  esac

  count=1
  while [ "$count" -le "$attempts" ]; do
    if [ -n "${DATABASE_URL:-}" ]; then
      if pg_isready --dbname "$DATABASE_URL" >/dev/null 2>&1; then
        return 0
      fi
    elif PGHOST="${DB_HOST:-postgres}" \
      PGPORT="${DB_PORT:-5432}" \
      PGUSER="${DB_USER:-effchat}" \
      pg_isready >/dev/null 2>&1; then
      return 0
    fi
    sleep "$delay"
    count=$((count + 1))
  done

  echo "PostgreSQL did not become ready before the migration deadline" >&2
  return 1
}

case "$role" in
  backend)
    mkdir -p \
      /app/storage/attachments/originals \
      /app/storage/attachments/extracted \
      /app/storage/attachments/ocr-staging \
      /app/storage/avatars \
      /app/storage/fonts \
      /app/storage/skills
    chown -R app:app /app/storage
    chmod -R go-rwx /app/storage/attachments
    exec gosu app /app/effchat-server "$@"
    ;;
  extractor)
    exec gosu app python -m uvicorn app.main:app \
      --host "${EXTRACTOR_HOST:-0.0.0.0}" \
      --port "${EXTRACTOR_PORT:-8090}" \
      --workers 1 "$@"
    ;;
  migrate)
    database_ready
    /app/migrations/build_migration_script.sh > /tmp/apply-migrations.sql
    if [ -n "${DATABASE_URL:-}" ]; then
      exec gosu app psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f /tmp/apply-migrations.sql "$@"
    fi
    export PGHOST="${DB_HOST:-postgres}"
    export PGPORT="${DB_PORT:-5432}"
    export PGUSER="${DB_USER:-effchat}"
    export PGPASSWORD="${DB_PASSWORD:-}"
    export PGDATABASE="${DB_NAME:-effchat}"
    exec gosu app psql -v ON_ERROR_STOP=1 -f /tmp/apply-migrations.sql "$@"
    ;;
  web)
    # shellcheck source=/dev/null
    . /app/web/15-effchat-upload-limit.envsh
    envsubst '$EFFCHAT_NGINX_MAX_BODY_BYTES' \
      < /app/web/default.conf.template \
      > /etc/nginx/conf.d/default.conf
    nginx -t
    exec nginx -g 'daemon off;' "$@"
    ;;
  *)
    exec "$role" "$@"
    ;;
esac
