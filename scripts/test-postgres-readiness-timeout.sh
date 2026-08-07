#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
TEST_ROOT="$(mktemp -d)"
cleanup() {
  local status=$?
  rm -rf -- "$TEST_ROOT"
  exit "$status"
}
trap cleanup EXIT

mkdir -p "$TEST_ROOT/bin"
export FAKE_DOCKER_LOG="$TEST_ROOT/docker.log"
cat > "$TEST_ROOT/bin/docker" <<'FAKE_DOCKER'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$FAKE_DOCKER_LOG"
case " $* " in
  *' run -d '*) printf 'fixture-container-id\n' ;;
  *' exec effchat-test-postgres-'*) exit 1 ;;
  *' logs effchat-test-postgres-'*) printf 'synthetic postgres startup failure\n' ;;
  *' rm -f '*) ;;
  *) ;;
esac
FAKE_DOCKER
chmod +x "$TEST_ROOT/bin/docker"

if PATH="$TEST_ROOT/bin:$PATH" \
  EFFCHAT_TEST_POSTGRES_READY_ATTEMPTS=2 \
  EFFCHAT_TEST_POSTGRES_READY_INTERVAL_SECONDS=0 \
  "$ROOT/scripts/test-postgres.sh" >"$TEST_ROOT/output.log" 2>&1; then
  echo "Expected PostgreSQL readiness timeout." >&2
  exit 1
fi

grep -Fq 'PostgreSQL readiness timed out during startup after 2 attempt(s).' "$TEST_ROOT/output.log"
grep -Fq 'synthetic postgres startup failure' "$TEST_ROOT/output.log"
test "$(grep -c 'exec effchat-test-postgres-' "$FAKE_DOCKER_LOG")" -eq 2
grep -Fq 'rm -f effchat-test-postgres-' "$FAKE_DOCKER_LOG"

echo "PostgreSQL readiness timeout checks passed."
