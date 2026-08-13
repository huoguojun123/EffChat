#!/bin/sh

set -eu

ROOT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT INT TERM

cat > "$TMP_DIR/pip-audit" <<'EOF'
#!/bin/sh
printf '%s\n' "$@" > "$PIP_AUDIT_ARGS"
exit 1
EOF
chmod +x "$TMP_DIR/pip-audit"

if PIP_AUDIT_ARGS="$TMP_DIR/args" PIP_AUDIT_BIN="$TMP_DIR/pip-audit" \
  "$ROOT_DIR/scripts/audit-python-dependencies.sh" "$ROOT_DIR/py-extractor/requirements.lock"; then
  echo "Python dependency audit unexpectedly ignored the scanner failure" >&2
  exit 1
fi

cat > "$TMP_DIR/expected" <<EOF
--require-hashes
-r
$ROOT_DIR/py-extractor/requirements.lock
EOF

cmp "$TMP_DIR/expected" "$TMP_DIR/args"

if PIP_AUDIT_BIN="$TMP_DIR/pip-audit" \
  "$ROOT_DIR/scripts/audit-python-dependencies.sh" "$TMP_DIR/missing.lock"; then
  echo "Python dependency audit unexpectedly accepted a missing lock file" >&2
  exit 1
fi
