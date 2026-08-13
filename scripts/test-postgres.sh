#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
NAME="effchat-test-postgres-$$"
PORT="${EFFCHAT_TEST_POSTGRES_PORT:-55432}"
PASSWORD="effchat-test"
DATABASE="effchat_test"
PARALLELISM="${EFFCHAT_TEST_GO_PARALLELISM:-4}"
READY_ATTEMPTS="${EFFCHAT_TEST_POSTGRES_READY_ATTEMPTS:-60}"
READY_INTERVAL_SECONDS="${EFFCHAT_TEST_POSTGRES_READY_INTERVAL_SECONDS:-1}"

case "$PARALLELISM" in
  ''|*[!0-9]*|0)
    echo "EFFCHAT_TEST_GO_PARALLELISM must be a positive integer" >&2
    exit 2
    ;;
esac
case "$READY_ATTEMPTS" in
  ''|*[!0-9]*|0)
    echo "EFFCHAT_TEST_POSTGRES_READY_ATTEMPTS must be a positive integer" >&2
    exit 2
    ;;
esac
case "$READY_INTERVAL_SECONDS" in
  ''|*[!0-9]*)
    echo "EFFCHAT_TEST_POSTGRES_READY_INTERVAL_SECONDS must be a non-negative integer" >&2
    exit 2
    ;;
esac

cleanup() {
  docker rm -f "$NAME" >/dev/null 2>&1 || true
}
trap cleanup EXIT

wait_for_postgres() {
  local phase="$1" attempt
  for ((attempt = 1; attempt <= READY_ATTEMPTS; attempt++)); do
    if docker exec "$NAME" pg_isready -U effchat_test -d "$DATABASE" >/dev/null 2>&1; then
      return 0
    fi
    if [ "$attempt" -lt "$READY_ATTEMPTS" ] && [ "$READY_INTERVAL_SECONDS" -gt 0 ]; then
      sleep "$READY_INTERVAL_SECONDS"
    fi
  done

  echo "PostgreSQL readiness timed out during $phase after $READY_ATTEMPTS attempt(s)." >&2
  docker logs "$NAME" >&2 || true
  return 1
}

docker run -d --rm --name "$NAME" \
  -e POSTGRES_USER=effchat_test \
  -e POSTGRES_PASSWORD="$PASSWORD" \
  -e POSTGRES_DB="$DATABASE" \
  -p "127.0.0.1:${PORT}:5432" postgres:17 >/dev/null

wait_for_postgres startup
sleep 2
wait_for_postgres stability-check

POSTGRES_CONTAINER="$NAME" DB_USER=effchat_test "$ROOT/scripts/test-migrations.sh"

POSTGRES_CONTAINER="$NAME" DB_USER=effchat_test DB_PASSWORD="$PASSWORD" DB_NAME="$DATABASE" DB_SSLMODE=disable \
  "$ROOT/backend/migrations/init_db.sh"

cd "$ROOT/backend"
EFFCHAT_ALLOW_DESTRUCTIVE_TESTS=1 EFFCHAT_TEST_DATABASE_DSN="postgres://effchat_test:${PASSWORD}@127.0.0.1:${PORT}/${DATABASE}?sslmode=disable" go test -p "$PARALLELISM" ./...
