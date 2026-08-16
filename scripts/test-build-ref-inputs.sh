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

fixture="$TEST_ROOT/source"
mkdir -p \
  "$fixture/backend" \
  "$fixture/frontend" \
  "$fixture/py-extractor" \
  "$fixture/docker" \
  "$fixture/scripts/licenses"
cp "$ROOT/scripts/docker-build.sh" "$fixture/scripts/docker-build.sh"
cp "$ROOT/scripts/compose-env.sh" "$fixture/scripts/compose-env.sh"
chmod +x "$fixture/scripts/docker-build.sh"

printf 'package main\n' > "$fixture/backend/main.go"
printf 'export const fixture = true\n' > "$fixture/frontend/app.ts"
printf 'fixture = True\n' > "$fixture/py-extractor/app.py"
printf 'FROM scratch\n' > "$fixture/Dockerfile"
printf '#!/bin/sh\nexec "$@"\n' > "$fixture/docker/entrypoint.sh"
printf 'fixture license helper\n' > "$fixture/scripts/licenses/helper.py"
printf 'services: {}\n' > "$fixture/docker-compose.yml"
printf 'data/\n' > "$fixture/.dockerignore"
printf 'fixture license\n' > "$fixture/LICENSE"
printf 'fixture notice\n' > "$fixture/NOTICE"
printf 'fixture third-party notice\n' > "$fixture/THIRD_PARTY_NOTICES.md"

build_ref() {
  "$fixture/scripts/docker-build.sh" build-ref
}

baseline="$(build_ref)"
[[ "$baseline" =~ ^source-[0-9a-f]{12}$ ]] || {
  echo "Unexpected source BUILD_REF: $baseline" >&2
  exit 1
}

included=(
  backend/main.go
  frontend/app.ts
  py-extractor/app.py
  Dockerfile
  docker/entrypoint.sh
  scripts/licenses/helper.py
  docker-compose.yml
  .dockerignore
  LICENSE
  NOTICE
  THIRD_PARTY_NOTICES.md
)
for relative in "${included[@]}"; do
  original="$TEST_ROOT/original"
  cp "$fixture/$relative" "$original"
  printf '\nbuild-ref mutation\n' >> "$fixture/$relative"
  changed="$(build_ref)"
  [ "$changed" != "$baseline" ] || {
    echo "BUILD_REF ignored an image input: $relative" >&2
    exit 1
  }
  mv "$original" "$fixture/$relative"
done

mkdir -p "$fixture/data" "$fixture/frontend/dist"
printf 'synthetic secret\n' > "$fixture/.env.docker"
printf 'synthetic runtime data\n' > "$fixture/data/runtime.txt"
printf 'synthetic build output\n' > "$fixture/frontend/dist/bundle.js"
printf 'synthetic log\n' > "$fixture/backend/debug.log"
touch "$fixture/backend/main.go"
[ "$(build_ref)" = "$baseline" ] || {
  echo "BUILD_REF absorbed env, data, logs, build output, or timestamps." >&2
  exit 1
}

git -C "$fixture" init -q
git -C "$fixture" config user.name 'EffChat Fixture'
git -C "$fixture" config user.email 'fixture@example.invalid'
git -C "$fixture" add .
git -C "$fixture" commit -qm 'fixture baseline'
git_ref="$(git -C "$fixture" rev-parse --short HEAD)"
[ "$(build_ref)" = "$git_ref" ] || {
  echo "Clean Git source did not use its commit BUILD_REF." >&2
  exit 1
}

printf '\ndirty license\n' >> "$fixture/LICENSE"
dirty_ref="$(build_ref)"
[[ "$dirty_ref" =~ ^${git_ref}-dirty-[0-9a-f]{12}$ ]] || {
  echo "Root image input did not mark the Git BUILD_REF dirty: $dirty_ref" >&2
  exit 1
}
git -C "$fixture" restore LICENSE

printf '\nignored env mutation\n' >> "$fixture/.env.docker"
[ "$(build_ref)" = "$git_ref" ] || {
  echo "Ignored environment state marked the Git BUILD_REF dirty." >&2
  exit 1
}

echo "BUILD_REF input contract checks passed."
