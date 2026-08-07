#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
TEST_ROOT="$(mktemp -d)"
trap 'rm -rf -- "$TEST_ROOT"' EXIT

mkdir -p "$TEST_ROOT/bin"
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
  *" build "*)
    if [ "${FAKE_BUILD_FAIL:-0}" = 1 ]; then exit 42; fi
    ;;
  *" exec -T postgres pg_isready "*) ;;
  *" exec -T postgres psql "*)
    if [[ " $* " == *" -Atc "* ]] && [[ " $* " == *"count(*)"* ]]; then
      printf '0\n'
    elif [ ! -t 0 ]; then
      cat >/dev/null
    fi
    ;;
  *" up -d --wait postgres "*) ;;
  *" run --rm --no-deps migrate "*) ;;
  *" stop web backend "*) ;;
  *" up -d --no-build --wait "*)
    if [ "${FAKE_FINAL_HEALTH_FAIL:-0}" = 1 ]; then exit 43; fi
    ;;
  *" down "*) ;;
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

run_build() {
  PATH="$TEST_ROOT/bin:$PATH" ENV_FILE="$TEST_ROOT/.env" \
    "$ROOT/scripts/docker-build.sh" "$@"
}

assert_order() {
  local before="$1" after="$2"
  local before_line after_line
  before_line="$(grep -n -m1 -F -- "$before" "$FAKE_DOCKER_LOG" | cut -d: -f1)"
  after_line="$(grep -n -m1 -F -- "$after" "$FAKE_DOCKER_LOG" | cut -d: -f1)"
  test "$before_line" -lt "$after_line"
}

run_build up
assert_order ' build' ' up -d --wait postgres'
assert_order ' up -d --wait postgres' ' run --rm --no-deps migrate'
assert_order ' run --rm --no-deps migrate' ' up -d --no-build --wait'
if grep -Fq -- ' up -d --build' "$FAKE_DOCKER_LOG"; then
  echo "Final service switch must not rebuild images." >&2
  exit 1
fi

: > "$FAKE_DOCKER_LOG"
rm -rf -- "$TEST_ROOT/data"
if FAKE_BUILD_FAIL=1 run_build up >"$TEST_ROOT/build-failure.log" 2>&1; then
  echo "Expected build failure." >&2
  exit 1
fi
if grep -Eq ' up -d --wait postgres| run --rm --no-deps migrate| stop web backend| up -d --no-build --wait' "$FAKE_DOCKER_LOG"; then
  echo "Build failure reached a deployment mutation." >&2
  exit 1
fi
test ! -e "$TEST_ROOT/data"

: > "$FAKE_DOCKER_LOG"
FAKE_FINAL_HEALTH_FAIL=1 run_build up >"$TEST_ROOT/health-failure.log" 2>&1 || true
grep -Fq -- ' up -d --no-build --wait' "$FAKE_DOCKER_LOG"
assert_order ' build' ' up -d --wait postgres'
assert_order ' run --rm --no-deps migrate' ' up -d --no-build --wait'

: > "$FAKE_DOCKER_LOG"
mkdir -p "$TEST_ROOT/data/postgres"
printf 'keep\n' > "$TEST_ROOT/data/postgres/sentinel"
if FAKE_BUILD_FAIL=1 CONFIRM_RESET=DELETE_EFFCHAT_DATA run_build reset-db \
  >"$TEST_ROOT/reset-build-failure.log" 2>&1; then
  echo "Expected reset build failure." >&2
  exit 1
fi
test -f "$TEST_ROOT/data/postgres/sentinel"
if grep -Eq ' down | up -d --no-build --wait' "$FAKE_DOCKER_LOG"; then
  echo "Reset build failure deleted or restarted the target." >&2
  exit 1
fi

: > "$FAKE_DOCKER_LOG"
CONFIRM_RESET=DELETE_EFFCHAT_DATA run_build reset-db
assert_order ' build' ' down'
assert_order ' down' ' up -d --no-build --wait'
test -d "$TEST_ROOT/data/postgres"
test -d "$TEST_ROOT/data/storage"

echo "Deployment stage ordering checks passed."
