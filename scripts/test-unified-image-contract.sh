#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
DOCKERFILE="$ROOT/Dockerfile"
ENTRYPOINT="$ROOT/docker/entrypoint.sh"

test -f "$DOCKERFILE"
test -x "$ENTRYPOINT"

for role in backend web extractor migrate; do
  grep -Eq "^[[:space:]]*${role}\)" "$ENTRYPOINT"
done

grep -Fq 'COPY backend/migrations ./migrations' "$DOCKERFILE"
grep -Fq 'COPY --from=backend-builder /out/effchat-server ./effchat-server' "$DOCKERFILE"
grep -Fq 'COPY --from=frontend-builder /app/dist /usr/share/nginx/html' "$DOCKERFILE"
grep -Fq 'COPY py-extractor/app ./extractor/app' "$DOCKERFILE"
grep -Fq 'ENTRYPOINT ["/usr/local/bin/effchat-entrypoint"]' "$DOCKERFILE"

if grep -Eiq 'supervisor|s6-overlay' "$DOCKERFILE" "$ENTRYPOINT"; then
  echo "unified image must keep one process role per container" >&2
  exit 1
fi

echo "unified image contract checks passed"
