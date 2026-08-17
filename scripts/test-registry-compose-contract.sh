#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
COMPOSE_FILE="$ROOT/docker-compose.registry.yml"

test -f "$COMPOSE_FILE"
! grep -Eq '^\s+build:' "$COMPOSE_FILE"
test "$(grep -Fc 'image: ${DOCKERHUB_NAMESPACE:-gjhuo}/effchat:${EFFCHAT_VERSION:-v0.4.1-beta.1}' "$COMPOSE_FILE")" -eq 1
grep -Fq 'profiles: [bundled-db]' "$COMPOSE_FILE"
grep -Fq 'required: false' "$COMPOSE_FILE"
grep -Fq '${DATA_DIR:-./data}/postgres:/var/lib/postgresql/data' "$COMPOSE_FILE"
grep -Fq '${DATA_DIR:-./data}/storage:/app/storage' "$COMPOSE_FILE"
grep -Fq 'condition: service_completed_successfully' "$COMPOSE_FILE"
! grep -Fq ':/migrations:ro' "$COMPOSE_FILE"

for role in backend web extractor migrate; do
  grep -Fq "command: [$role]" "$COMPOSE_FILE"
done

echo "registry Compose contract checks passed."
