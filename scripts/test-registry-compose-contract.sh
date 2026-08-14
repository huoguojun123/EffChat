#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
COMPOSE_FILE="$ROOT/docker-compose.registry.yml"

test -f "$COMPOSE_FILE"
! grep -Eq '^\s+build:' "$COMPOSE_FILE"
grep -Fq 'image: ${DOCKERHUB_NAMESPACE:-gjhuo}/effchat-backend:${EFFCHAT_VERSION:-v0.4.0-beta.1}' "$COMPOSE_FILE"
grep -Fq 'image: ${DOCKERHUB_NAMESPACE:-gjhuo}/effchat-web:${EFFCHAT_VERSION:-v0.4.0-beta.1}' "$COMPOSE_FILE"
grep -Fq 'image: ${DOCKERHUB_NAMESPACE:-gjhuo}/effchat-py-extractor:${EFFCHAT_VERSION:-v0.4.0-beta.1}' "$COMPOSE_FILE"
grep -Fq '${MIGRATIONS_DIR:-./backend/migrations}:/migrations:ro' "$COMPOSE_FILE"
grep -Fq '${DATA_DIR:-../data}/postgres:/var/lib/postgresql/data' "$COMPOSE_FILE"
grep -Fq '${DATA_DIR:-../data}/storage:/app/storage' "$COMPOSE_FILE"
grep -Fq 'condition: service_completed_successfully' "$COMPOSE_FILE"

echo "registry Compose contract checks passed."
