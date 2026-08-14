#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
TARGET_DIR="${1:-"${ROOT_DIR}/../effchat-public-source"}"
MARKER_NAME=".effchat-public-source.marker"
MARKER_VALUE="effchat-public-source-v1"
WORKSPACE_ROOT="$(cd "${ROOT_DIR}/.." && pwd -P)"

fail() {
  echo "Refusing unsafe export target: $1" >&2
  exit 1
}

normalize_lexical_path() {
  local path="$1" part result="/"
  local -a parts=() stack=()
  IFS='/' read -r -a parts <<< "$path"
  for part in "${parts[@]}"; do
    case "$part" in
      ""|.) ;;
      ..)
        [ "${#stack[@]}" -gt 0 ] && unset "stack[$((${#stack[@]} - 1))]"
        ;;
      *) stack+=("$part") ;;
    esac
  done
  for part in "${stack[@]}"; do result="${result%/}/$part"; done
  printf '%s\n' "$result"
}

canonicalize_path() {
  local raw="$1" absolute probe suffix="" base parent base_real
  case "$raw" in
    /*) absolute="$raw" ;;
    *) absolute="$(pwd -P)/$raw" ;;
  esac
  probe="$absolute"
  while [ ! -e "$probe" ] && [ ! -L "$probe" ]; do
    base="$(basename "$probe")"
    suffix="/$base$suffix"
    parent="$(dirname "$probe")"
    [ "$parent" != "$probe" ] || return 1
    probe="$parent"
  done
  [ -d "$probe" ] || return 1
  base_real="$(cd "$probe" && pwd -P)"
  normalize_lexical_path "$base_real$suffix"
}

path_is_within() {
  local child="$1" parent="$2"
  if [ "$parent" = "/" ]; then
    [[ "$child" == /* ]]
  else
    [[ "$child" == "$parent" || "$child" == "$parent/"* ]]
  fi
}

ROOT_DIR="$(canonicalize_path "$ROOT_DIR")" || fail "cannot resolve source root"
TARGET_REAL="$(canonicalize_path "$TARGET_DIR")" || fail "cannot resolve target path: $TARGET_DIR"

if path_is_within "$TARGET_REAL" "$ROOT_DIR" || path_is_within "$ROOT_DIR" "$TARGET_REAL"; then
  fail "target must not equal, contain, or be contained by the source tree: $TARGET_DIR"
fi

# In the EffChat three-line workspace, only runtime/src and the documented
# sibling export snapshot are valid destinations inside the workspace. This
# prevents an empty data, backup, internal, or workspace directory from being
# accidentally claimed by rsync --delete.
if [ -d "$WORKSPACE_ROOT/runtime" ] && [ -d "$WORKSPACE_ROOT/internal" ] && path_is_within "$TARGET_REAL" "$WORKSPACE_ROOT"; then
  case "$TARGET_REAL" in
    "$WORKSPACE_ROOT/runtime/src"|"$WORKSPACE_ROOT/effchat-public-source") ;;
    *) fail "target is inside the EffChat workspace but is not an approved export destination: $TARGET_DIR" ;;
  esac
fi

if [ -e "$TARGET_REAL" ] && [ ! -d "$TARGET_REAL" ]; then
  fail "target is not a directory: $TARGET_DIR"
fi

mkdir -p "$TARGET_REAL"
MARKER_PATH="$TARGET_REAL/$MARKER_NAME"
if [ -L "$MARKER_PATH" ] || { [ -e "$MARKER_PATH" ] && [ ! -f "$MARKER_PATH" ]; }; then
  fail "export marker is not a regular file: $MARKER_PATH"
fi

if [ -f "$MARKER_PATH" ]; then
  [ "$(cat "$MARKER_PATH")" = "$MARKER_VALUE" ] || fail "export marker is invalid: $MARKER_PATH"
else
  has_entries=0
  if find "$TARGET_REAL" -mindepth 1 -maxdepth 1 -print -quit | grep -q .; then has_entries=1; fi
  if [ "$has_entries" -eq 1 ]; then
    expected_runtime_src="$WORKSPACE_ROOT/runtime/src"
    if [ "${EFFCHAT_EXPORT_INITIALIZE:-0}" != "1" ] || [ "$TARGET_REAL" != "$expected_runtime_src" ]; then
      fail "non-empty target has no EffChat export marker: $TARGET_DIR"
    fi
  fi
fi

rsync -a --delete --delete-excluded \
  --exclude="$MARKER_NAME" \
  --exclude='.git/' \
  --exclude='.claude/' \
  --exclude='.serena/' \
  --exclude='.superpowers/' \
  --exclude='AGENTS.md' \
  --exclude='CLAUDE.md' \
  --exclude='.DS_Store' \
  --exclude='node_modules/' \
  --exclude='frontend/node_modules/' \
  --exclude='ui-baseline/' \
  --exclude='frontend/dist/' \
  --exclude='frontend/test-results/' \
  --exclude='frontend/playwright-report/' \
  --exclude='frontend/blob-report/' \
  --exclude='backend/bin/' \
  --exclude='backend/uploads/' \
  --exclude='backend/data/' \
  --exclude='data/' \
  --exclude='uploads/' \
  --exclude='logs/' \
  --exclude='*.log' \
  --exclude='.env' \
  --exclude='.env.docker' \
  --exclude='.env.docker.local' \
  --exclude='.env.local' \
  --exclude='.env.*.local' \
  --exclude='*.env' \
  "${ROOT_DIR}/" "${TARGET_REAL}/"

rm -f \
  "${TARGET_REAL}/backend/API.md" \
  "${TARGET_REAL}/backend/PROGRESS.md" \
  "${TARGET_REAL}/backend/test_api.sh" \
  "${TARGET_REAL}/backend/test_stream.sh"

printf '%s\n' "$MARKER_VALUE" > "$MARKER_PATH"

echo "Public source exported to: ${TARGET_REAL}"
echo "Next: run the release leak-scan checklist before publishing."
