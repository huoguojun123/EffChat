#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
DOCKERFILE="$ROOT/Dockerfile"
ENTRYPOINT="$ROOT/docker/entrypoint.sh"
E2E_COMPOSE="$ROOT/frontend/e2e/docker-compose.e2e.yml"

test -f "$DOCKERFILE"
test -x "$ENTRYPOINT"
test -f "$E2E_COMPOSE"

for role in backend web extractor migrate; do
  grep -Eq "^[[:space:]]*${role}\)" "$ENTRYPOINT"
done

grep -Fq 'COPY backend/migrations ./migrations' "$DOCKERFILE"
grep -Fq 'COPY --from=backend-builder /out/effchat-server ./effchat-server' "$DOCKERFILE"
grep -Fq 'COPY --from=frontend-builder /app/dist /usr/share/nginx/html' "$DOCKERFILE"
grep -Fq 'COPY py-extractor/app ./app' "$DOCKERFILE"
grep -Fq 'python -m uvicorn app.main:app' "$ENTRYPOINT"
if grep -Fq 'extractor.app.main' "$DOCKERFILE" "$ENTRYPOINT" "$ROOT/.github/workflows/ci.yml"; then
  echo "unified image must preserve the extractor app package layout" >&2
  exit 1
fi
grep -Fq 'ENTRYPOINT ["/usr/local/bin/effchat-entrypoint"]' "$DOCKERFILE"
grep -Fq 'image: effchat:local' "$E2E_COMPOSE"

if grep -Fq 'effchat-py-extractor:local' "$E2E_COMPOSE"; then
  echo "isolated Playwright must reuse the unified application image" >&2
  exit 1
fi

if grep -Eiq 'supervisor|s6-overlay' "$DOCKERFILE" "$ENTRYPOINT"; then
  echo "unified image must keep one process role per container" >&2
  exit 1
fi

echo "unified image contract checks passed"
