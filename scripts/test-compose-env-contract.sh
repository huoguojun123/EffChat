#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
TEST_ROOT="$(mktemp -d)"
trap 'rm -rf -- "$TEST_ROOT"' EXIT

write_env() {
  local postgres_password="$1"
  local jwt_secret="$2"
  local data_dir="$3"

  printf '%s\n' \
    'COMPOSE_PROJECT_NAME=effchat-env-contract-test' \
    'POSTGRES_USER=effchat' \
    "POSTGRES_PASSWORD=$postgres_password" \
    'POSTGRES_DB=effchat' \
    "JWT_SECRET=$jwt_secret" \
    "DATA_DIR=$data_dir" \
    > "$TEST_ROOT/.env"
}

expect_secret_rejection() {
  local expected="$1"
  local output

  if output="$(ENV_FILE="$TEST_ROOT/.env" "$ROOT/scripts/docker-build.sh" up 2>&1)"; then
    echo "Expected placeholder secret rejection." >&2
    exit 1
  fi
  if ! grep -Fq -- "$expected" <<<"$output"; then
    printf 'Expected rejection containing %q, got:\n%s\n' "$expected" "$output" >&2
    exit 1
  fi
}

write_env '"change-this-postgres-password" # quoted placeholder' \
  '"a-valid-test-jwt=with#characters"' './data'
expect_secret_rejection 'POSTGRES_PASSWORD'

write_env "'a-valid-test-password=with#characters'" \
  "'your-secret-key-change-this-in-production' # quoted placeholder" './data'
expect_secret_rejection 'JWT_SECRET'

write_env "'a-valid-test-password'" '' './data'
expect_secret_rejection 'JWT_SECRET'

write_env "'a-valid-test-password=with#characters'" \
  '"a-valid-test-jwt=with#characters"' '"./test data#blue=1" # inline comment'

# The sourced helper consumes this caller-owned array.
# shellcheck disable=SC2034
COMPOSE=(docker compose --env-file "$TEST_ROOT/.env" -f "$ROOT/docker-compose.yml")
# shellcheck source=compose-env.sh
source "$ROOT/scripts/compose-env.sh"
test "$(env_value POSTGRES_PASSWORD)" = 'a-valid-test-password=with#characters'
test "$(env_value JWT_SECRET)" = 'a-valid-test-jwt=with#characters'
test "$(env_value DATA_DIR)" = './test data#blue=1'

config_output="$(ENV_FILE="$TEST_ROOT/.env" "$ROOT/scripts/docker-build.sh" config)"
expected_root="$ROOT/./test data#blue=1"
grep -Fq -- "$expected_root/postgres" <<<"$config_output"
grep -Fq -- "$expected_root/storage" <<<"$config_output"

config_output="$(DATA_DIR='./shell override=data#green' ENV_FILE="$TEST_ROOT/.env" \
  "$ROOT/scripts/docker-build.sh" config)"
grep -Fq -- "$ROOT/./shell override=data#green/postgres" <<<"$config_output"
grep -Fq -- "$ROOT/./shell override=data#green/storage" <<<"$config_output"

if POSTGRES_PASSWORD='change-this-postgres-password' \
  ENV_FILE="$TEST_ROOT/.env" "$ROOT/scripts/docker-build.sh" up \
  >"$TEST_ROOT/shell-placeholder.log" 2>&1; then
  echo "Expected shell-precedence placeholder rejection." >&2
  exit 1
fi
grep -Fq -- 'POSTGRES_PASSWORD' "$TEST_ROOT/shell-placeholder.log"

write_env "'a-valid-test-password'" "'a-valid-test-jwt'" "'./single quoted data'"
config_output="$(ENV_FILE="$TEST_ROOT/.env" "$ROOT/scripts/docker-build.sh" config)"
grep -Fq -- "$ROOT/./single quoted data/postgres" <<<"$config_output"
grep -Fq -- "$ROOT/./single quoted data/storage" <<<"$config_output"

echo "Compose environment contract checks passed."
