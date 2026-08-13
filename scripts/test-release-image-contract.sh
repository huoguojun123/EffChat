#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
WORKFLOW="$ROOT/.github/workflows/release-images.yml"

require_count() {
  local expected="$1"
  local pattern="$2"
  local actual
  actual="$(grep -Fxc -- "$pattern" "$WORKFLOW")"
  if [ "$actual" -ne "$expected" ]; then
    echo "release image contract expected $expected occurrence(s), found $actual: $pattern" >&2
    exit 1
  fi
}

# Both the parallel build jobs and the atomic promotion job must authenticate
# to Docker Hub. The token stays in Actions secrets and is never passed to a
# shell environment or written to logs.
require_count 2 '          username: ${{ secrets.DOCKERHUB_USERNAME }}'
require_count 2 '          password: ${{ secrets.DOCKERHUB_TOKEN }}'

# One Buildx invocation publishes the same staging manifest to both
# registries. Promotion happens only after all three component builds finish.
require_count 1 '            ghcr.io/huoguojun123/${{ matrix.component.image }}'
require_count 1 '            ${{ secrets.DOCKERHUB_USERNAME }}/${{ matrix.component.image }}'
require_count 1 '          DOCKERHUB_USERNAME: ${{ secrets.DOCKERHUB_USERNAME }}'
require_count 1 '            dockerhub_source="${DOCKERHUB_USERNAME}/${image}:${SOURCE_TAG}"'
require_count 1 '              --tag "${DOCKERHUB_USERNAME}/${image}:${RELEASE_TAG}" \'
require_count 1 '              --tag "${DOCKERHUB_USERNAME}/${image}:sha-${GITHUB_SHA}" \'

if grep -Eq -- '(^|[[:space:]:])latest([[:space:]]|$)' "$WORKFLOW"; then
  echo "pre-release workflow must not publish latest" >&2
  exit 1
fi

echo "release image registry contract checks passed"
