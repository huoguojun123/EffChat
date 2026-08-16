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

# verify-release-ref.sh reads completed check runs through the GitHub API, so
# the verify job must explicitly retain read access when job permissions narrow
# the workflow-level token.
verify_permissions="$({
  awk '
    /^  verify:$/ { in_verify = 1 }
    in_verify && /^    steps:$/ { exit }
    in_verify { print }
  ' "$WORKFLOW"
})"
if ! grep -Fxq '      checks: read' <<<"$verify_permissions"; then
  echo "release verify job must grant checks: read" >&2
  exit 1
fi

# One Buildx invocation publishes the same unified staging manifest to both
# registries. Promotion happens only after that multi-architecture build finishes.
require_count 1 '            ghcr.io/huoguojun123/effchat'
require_count 1 '            ${{ secrets.DOCKERHUB_USERNAME }}/effchat'
require_count 1 '          DOCKERHUB_USERNAME: ${{ secrets.DOCKERHUB_USERNAME }}'
require_count 1 '          dockerhub_source="${DOCKERHUB_USERNAME}/effchat:${SOURCE_TAG}"'
require_count 1 '            --tag "${DOCKERHUB_USERNAME}/effchat:${RELEASE_TAG}" \'
require_count 1 '            --tag "${DOCKERHUB_USERNAME}/effchat:sha-${GITHUB_SHA}" \'

if grep -Eq -- '(^|[[:space:]:])latest([[:space:]]|$)' "$WORKFLOW"; then
  echo "pre-release workflow must not publish latest" >&2
  exit 1
fi

echo "release image registry contract checks passed"
