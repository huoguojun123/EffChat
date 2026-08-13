#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
TMP_DIR="$(mktemp -d /tmp/effchat-export-test.XXXXXX)"
EXTERNAL_DIR="$(mktemp -d /tmp/effchat-export-external.XXXXXX)"
trap 'rm -rf "$TMP_DIR" "$EXTERNAL_DIR"' EXIT

hash_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

expect_failure() {
  if (cd "$TMP_DIR/public" && "$@") >/dev/null 2>&1; then
    echo "expected export failure: $*" >&2
    exit 1
  fi
}

assert_hash() {
  local expected="$1" file="$2"
  [ -f "$file" ] || { echo "missing sentinel: $file" >&2; exit 1; }
  [ "$(hash_file "$file")" = "$expected" ] || { echo "sentinel changed: $file" >&2; exit 1; }
}

mkdir -p "$TMP_DIR/public/scripts" "$TMP_DIR/runtime/data" "$TMP_DIR/runtime/src" "$TMP_DIR/internal"
cp "$SCRIPT_DIR/export-public-source.sh" "$TMP_DIR/public/scripts/export-public-source.sh"
chmod +x "$TMP_DIR/public/scripts/export-public-source.sh"
printf 'source-only\n' > "$TMP_DIR/public/README.md"

printf 'parent-sentinel\n' > "$TMP_DIR/runtime/data/sentinel.txt"
PARENT_HASH="$(hash_file "$TMP_DIR/runtime/data/sentinel.txt")"
expect_failure ./scripts/export-public-source.sh .
expect_failure ./scripts/export-public-source.sh ..
expect_failure ./scripts/export-public-source.sh "$TMP_DIR"
expect_failure ./scripts/export-public-source.sh ../runtime/data/export
assert_hash "$PARENT_HASH" "$TMP_DIR/runtime/data/sentinel.txt"

mkdir -p "$TMP_DIR/public/child"
expect_failure ./scripts/export-public-source.sh child
ln -s "$TMP_DIR" "$TMP_DIR/public/parent-link"
expect_failure ./scripts/export-public-source.sh parent-link

printf 'external-sentinel\n' > "$EXTERNAL_DIR/sentinel.txt"
EXTERNAL_HASH="$(hash_file "$EXTERNAL_DIR/sentinel.txt")"
expect_failure ./scripts/export-public-source.sh "$EXTERNAL_DIR"
assert_hash "$EXTERNAL_HASH" "$EXTERNAL_DIR/sentinel.txt"

(cd "$TMP_DIR/public" && ./scripts/export-public-source.sh ../effchat-public-source)
[ -f "$TMP_DIR/effchat-public-source/.effchat-public-source.marker" ]
printf 'stale\n' > "$TMP_DIR/effchat-public-source/stale.txt"
(cd "$TMP_DIR/public" && ./scripts/export-public-source.sh ../effchat-public-source)
[ ! -e "$TMP_DIR/effchat-public-source/stale.txt" ]

printf 'runtime-sentinel\n' > "$TMP_DIR/runtime/src/sentinel.txt"
RUNTIME_HASH="$(hash_file "$TMP_DIR/runtime/src/sentinel.txt")"
expect_failure ./scripts/export-public-source.sh ../runtime/src
assert_hash "$RUNTIME_HASH" "$TMP_DIR/runtime/src/sentinel.txt"
(cd "$TMP_DIR/public" && EFFCHAT_EXPORT_INITIALIZE=1 ./scripts/export-public-source.sh ../runtime/src)
[ -f "$TMP_DIR/runtime/src/.effchat-public-source.marker" ]

echo "export target safety tests passed"
