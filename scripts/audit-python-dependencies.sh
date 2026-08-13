#!/bin/sh

set -eu

ROOT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
LOCK_FILE=${1:-"$ROOT_DIR/py-extractor/requirements.lock"}
PIP_AUDIT_BIN=${PIP_AUDIT_BIN:-pip-audit}

if [ ! -f "$LOCK_FILE" ]; then
  echo "Python dependency lock file not found: $LOCK_FILE" >&2
  exit 2
fi

exec "$PIP_AUDIT_BIN" --require-hashes -r "$LOCK_FILE"
