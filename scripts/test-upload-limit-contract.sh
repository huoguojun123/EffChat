#!/bin/sh
set -eu

ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
LIMIT_ENVSH="$ROOT/frontend/docker-entrypoint.d/15-effchat-upload-limit.envsh"

render_limit() {
  PY_EXTRACTOR_MAX_UPLOAD_BYTES="$1" sh -c '. "$1"; printf "%s" "$EFFCHAT_NGINX_MAX_BODY_BYTES"' sh "$LIMIT_ENVSH"
}

[ "$(render_limit 26214400)" = "27262976" ]
[ "$(render_limit 10485760)" = "11534336" ]

for invalid in invalid 0 524288 1572864; do
  if PY_EXTRACTOR_MAX_UPLOAD_BYTES="$invalid" sh -c '. "$1"' sh "$LIMIT_ENVSH" >/dev/null 2>&1; then
    echo "invalid extractor upload limit must fail Nginx startup: $invalid" >&2
    exit 1
  fi
done

grep -Fq 'client_max_body_size ${EFFCHAT_NGINX_MAX_BODY_BYTES};' "$ROOT/frontend/nginx.conf"
grep -Fq 'PY_EXTRACTOR_MAX_UPLOAD_BYTES: ${PY_EXTRACTOR_MAX_UPLOAD_BYTES:-26214400}' "$ROOT/docker-compose.yml"
grep -Fq 'NGINX_ENVSUBST_FILTER: ^EFFCHAT_' "$ROOT/docker-compose.yml"

echo "upload limit contract tests passed"
