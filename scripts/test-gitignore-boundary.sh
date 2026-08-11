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

cp "$ROOT/.gitignore" "$TEST_ROOT/.gitignore"
git -C "$TEST_ROOT" init -q

expect_visible() {
  local path=$1
  if git -C "$TEST_ROOT" check-ignore --no-index -q -- "$path"; then
    echo "Legitimate repository path is ignored: $path" >&2
    exit 1
  fi
}

expect_ignored() {
  local path=$1
  if ! git -C "$TEST_ROOT" check-ignore --no-index -q -- "$path"; then
    echo "Private or generated path is not ignored: $path" >&2
    exit 1
  fi
}

for path in \
  frontend/src/hotkey.ts \
  backend/internal/monkey_test.go \
  backend/internal/password_policy.go \
  docs/api_secret_rotation.md \
  frontend/src/keymap.tsx \
  certs/public.crt \
  .env.docker.example \
  backend/.env.example; do
  expect_visible "$path"
done

for path in \
  .env \
  .env.docker \
  backend/.env \
  frontend/.env.local \
  credentials/service.key \
  credentials/service.pem \
  data/messages.json \
  backend/storage/document.txt \
  frontend/node_modules/package/index.js; do
  expect_ignored "$path"
done

echo "Git ignore boundary checks passed."
