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

context="$TEST_ROOT/context"
output="$TEST_ROOT/output"
mkdir -p \
  "$context/backend" \
  "$context/frontend" \
  "$context/py-extractor" \
  "$context/data" \
  "$context/nested/storage" \
  "$context/uploads" \
  "$context/backups" \
  "$context/logs" \
  "$context/test-results" \
  "$context/playwright-report" \
  "$context/.serena" \
  "$context/.audit-fixtures"

cp "$ROOT/.dockerignore" "$context/.dockerignore"
cat > "$context/Dockerfile" <<'DOCKERFILE'
FROM scratch
COPY . /context
DOCKERFILE

printf 'module example.invalid/effchat-fixture\n' > "$context/backend/go.mod"
printf '{"lockfileVersion":3}\n' > "$context/frontend/package-lock.json"
printf 'fixture==1.0.0\n' > "$context/py-extractor/requirements.lock"
printf 'synthetic license fixture\n' > "$context/LICENSE"
printf 'POSTGRES_PASSWORD=replace-with-a-strong-password\n' > "$context/.env.docker.example"

dangerous=(
  data/runtime.txt
  nested/storage/managed.txt
  uploads/legacy.txt
  backups/database.backup
  logs/backend.log
  test-results/result.json
  playwright-report/index.html
  .serena/memory.md
  .audit-fixtures/evidence.txt
  database.dump
  nested-debug.log
)
for relative in "${dangerous[@]}"; do
  mkdir -p "$(dirname "$context/$relative")"
  printf 'synthetic private sentinel: %s\n' "$relative" > "$context/$relative"
done

docker buildx build \
  --progress plain \
  --no-cache \
  --output "type=local,dest=$output" \
  "$context" >/dev/null

for relative in \
  backend/go.mod \
  frontend/package-lock.json \
  py-extractor/requirements.lock \
  LICENSE \
  .env.docker.example; do
  test -f "$output/context/$relative" || {
    echo "Required build input was excluded: $relative" >&2
    exit 1
  }
done

for relative in "${dangerous[@]}"; do
  test ! -e "$output/context/$relative" || {
    echo "Private build-context sentinel was included: $relative" >&2
    exit 1
  }
done

if grep -R -n 'synthetic private sentinel' "$output" >/dev/null; then
  echo "Private sentinel content survived under an unexpected path." >&2
  exit 1
fi

echo "Docker build context boundary checks passed."
