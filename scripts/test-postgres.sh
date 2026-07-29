#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
NAME="effchat-test-postgres-$$"
PORT="${EFFCHAT_TEST_POSTGRES_PORT:-55432}"
PASSWORD="effchat-test"
DATABASE="effchat_test"
PARALLELISM="${EFFCHAT_TEST_GO_PARALLELISM:-4}"

case "$PARALLELISM" in
  ''|*[!0-9]*|0)
    echo "EFFCHAT_TEST_GO_PARALLELISM must be a positive integer" >&2
    exit 2
    ;;
esac

cleanup() {
  docker rm -f "$NAME" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker run -d --rm --name "$NAME" \
  -e POSTGRES_USER=effchat_test \
  -e POSTGRES_PASSWORD="$PASSWORD" \
  -e POSTGRES_DB="$DATABASE" \
  -p "127.0.0.1:${PORT}:5432" postgres:17 >/dev/null

until docker exec "$NAME" pg_isready -U effchat_test -d "$DATABASE" >/dev/null 2>&1; do sleep 1; done
sleep 2
until docker exec "$NAME" pg_isready -U effchat_test -d "$DATABASE" >/dev/null 2>&1; do sleep 1; done

POSTGRES_CONTAINER="$NAME" DB_USER=effchat_test DB_PASSWORD="$PASSWORD" DB_NAME="$DATABASE" DB_SSLMODE=disable \
  "$ROOT/backend/migrations/init_db.sh"

cd "$ROOT/backend"
EFFCHAT_ALLOW_DESTRUCTIVE_TESTS=1 EFFCHAT_TEST_DATABASE_DSN="postgres://effchat_test:${PASSWORD}@127.0.0.1:${PORT}/${DATABASE}?sslmode=disable" go test -p "$PARALLELISM" ./...
