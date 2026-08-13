#!/bin/sh

set -eu

ROOT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
TMP_DIR=$(mktemp -d)
PROJECT="effchat-e2e-$$"
ENV_FILE="$TMP_DIR/e2e.env"
COMPOSE="docker compose --env-file $ENV_FILE -f $ROOT_DIR/docker-compose.yml -f $ROOT_DIR/frontend/e2e/docker-compose.e2e.yml"
WEB_PORT=${E2E_WEB_PORT:-28088}
BACKEND_PORT=${E2E_BACKEND_PORT:-28080}

cleanup() {
  status=$?
  if [ "$status" -ne 0 ]; then
    mkdir -p "$ROOT_DIR/frontend/test-results"
    $COMPOSE logs --no-color > "$ROOT_DIR/frontend/test-results/compose.log" 2>&1 || true
    $COMPOSE exec -T postgres psql -U effchat_e2e -d effchat_e2e -X -A -F '|' -c \
      "SELECT task_key, status, error_type, error_message FROM model_task_runs ORDER BY id" \
      > "$ROOT_DIR/frontend/test-results/model-task-runs.log" 2>&1 || true
  fi
  $COMPOSE down --remove-orphans >/dev/null 2>&1 || true
  if docker ps -aq --filter "label=com.docker.compose.project=$PROJECT" | grep -q .; then
    echo "isolated E2E containers remain for project $PROJECT" >&2
    status=1
  fi
  # Compose bind mounts are written by root inside Linux containers. Remove
  # their isolated contents as container root, then let the host own the final
  # empty-directory removal. The path is always created by mktemp above.
  docker run --rm --entrypoint sh -v "$TMP_DIR:/cleanup" postgres:17 \
    -ec 'find /cleanup -mindepth 1 -delete' >/dev/null 2>&1 || status=1
  rmdir "$TMP_DIR" >/dev/null 2>&1 || status=1
  exit "$status"
}
trap cleanup EXIT INT TERM

cat > "$ENV_FILE" <<EOF
COMPOSE_PROJECT_NAME=$PROJECT
DOCKER_NETWORK=${PROJECT}_net
DATA_DIR=$TMP_DIR/data
WEB_PORT=$WEB_PORT
BACKEND_PORT=$BACKEND_PORT
POSTGRES_USER=effchat_e2e
POSTGRES_PASSWORD=fixture-postgres-password
POSTGRES_DB=effchat_e2e
JWT_SECRET=fixture-jwt-secret-with-sufficient-length
SERVER_MODE=release
COMPRESSION_MAX_TOKENS=32000
EOF

$COMPOSE up -d --build --wait

BASE_URL="http://127.0.0.1:$WEB_PORT"
REGISTER=$(curl --fail --silent --show-error -H 'Content-Type: application/json' \
  -d '{"username":"e2e_admin","password":"fixture-password-42"}' \
  "$BASE_URL/api/v1/auth/register")
TOKEN=$(printf '%s' "$REGISTER" | python3 -c 'import json,sys; print(json.load(sys.stdin)["token"])')
AUTH="Authorization: Bearer $TOKEN"

curl --fail --silent --show-error -H "$AUTH" -H 'Content-Type: application/json' \
  -d '{"key":"e2e","display_name":"E2E Stub","adapter":"openai_compatible","base_url":"http://model-stub:8091/v1","api_key":"fixture-key","enabled":true}' \
  "$BASE_URL/api/v1/admin/channels" >/dev/null
curl --fail --silent --show-error -H "$AUTH" -H 'Content-Type: application/json' \
  -d '{"id":"effchat-e2e","display_name":"EffChat E2E","provider":"e2e","thinking_format":"none","context_window":128000,"max_output":8192,"enabled":true,"catalog_source":"manual","lifecycle_status":"active","temperature_policy":"omit"}' \
  "$BASE_URL/api/v1/admin/models" >/dev/null
curl --fail --silent --show-error -X PATCH -H "$AUTH" -H 'Content-Type: application/json' \
  -d '{"value":"effchat-e2e"}' "$BASE_URL/api/v1/admin/config/default_model_id" >/dev/null

E2E_BASE_URL="$BASE_URL" E2E_USERNAME=e2e_admin E2E_PASSWORD=fixture-password-42 E2E_REQUIRE_STACK=1 \
  npm --prefix "$ROOT_DIR/frontend" run e2e -- \
    e2e/upload-attachment.spec.ts e2e/stop-generation.spec.ts e2e/compaction-undo.spec.ts
