#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
TEST_ROOT="$(mktemp -d)"
trap 'rm -rf -- "$TEST_ROOT"' EXIT

mkdir -p "$TEST_ROOT/bin" "$TEST_ROOT/data/storage/attachments/extracted/1"
export TEST_DATA_DIR="$TEST_ROOT/data"

cat > "$TEST_ROOT/bin/docker" <<'FAKE_DOCKER'
#!/usr/bin/env bash
set -euo pipefail

case " $* " in
  *" config --environment "*)
    printf '%s\n' \
      "DATA_DIR=$TEST_DATA_DIR" \
      'POSTGRES_USER=effchat' \
      'POSTGRES_DB=effchat' \
      'POSTGRES_PASSWORD=test-password' \
      'JWT_SECRET=test-jwt'
    ;;
  *" exec -T postgres pg_isready "*) ;;
  *" exec -T postgres psql "*)
    if [[ " $* " == *"status IN ('staged', 'formal')"* ]]; then
      printf '%s\n' "${FAKE_ACTIVE_PATHS:-}"
    elif [[ " $* " == *"status = 'cleanup_claimed'"* ]]; then
      printf '%s\n' "${FAKE_DEFERRED_PATHS:-}"
    elif [[ " $* " == *"count(*)"* ]]; then
      printf '0\n'
    fi
    ;;
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

run_verify() {
  PATH="$TEST_ROOT/bin:$PATH" ENV_FILE="$TEST_ROOT/.env" \
    "$ROOT/scripts/storage-layout.sh" verify
}

printf 'active\n' > "$TEST_ROOT/data/storage/attachments/extracted/1/中文附件.txt"
FAKE_ACTIVE_PATHS='storage/attachments/extracted/1/中文附件.txt' run_verify
FAKE_DEFERRED_PATHS='storage/attachments/extracted/1/deleted.txt' run_verify

if FAKE_ACTIVE_PATHS='storage/attachments/extracted/1/active.txt' run_verify \
  >"$TEST_ROOT/active-missing.log" 2>&1; then
  echo "Active managed paths must still exist." >&2
  exit 1
fi
grep -Fq 'Database path is missing on disk: storage/attachments/extracted/1/active.txt' \
  "$TEST_ROOT/active-missing.log"

if FAKE_DEFERRED_PATHS='../outside/deleted.txt' run_verify \
  >"$TEST_ROOT/deferred-outside.log" 2>&1; then
  echo "Deferred cleanup paths must remain inside managed storage." >&2
  exit 1
fi
grep -Fq 'Database path is outside managed storage: ../outside/deleted.txt' \
  "$TEST_ROOT/deferred-outside.log"

echo "Storage verify lifecycle checks passed."
