#!/usr/bin/env bash

set -euo pipefail

: "${GITHUB_REF_NAME:?GITHUB_REF_NAME is required}"
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
: "${GITHUB_SHA:?GITHUB_SHA is required}"

case "${GITHUB_REF_NAME}" in
  v*) ;;
  *) echo "release workflow only accepts v* tags" >&2; exit 1 ;;
esac

# The release event only proves that a tag exists. The commit must also belong
# to the reviewed main history; otherwise a feature or stale commit could
# bypass the normal CI path and publish images directly.
if ! git merge-base --is-ancestor "${GITHUB_SHA}" refs/remotes/origin/main; then
  echo "release tag ${GITHUB_REF_NAME} does not point to a commit reachable from origin/main" >&2
  exit 1
fi

if [ -n "${RELEASE_CHECK_RUNS_FILE:-}" ]; then
  check_runs="$(<"${RELEASE_CHECK_RUNS_FILE}")"
else
  : "${GH_TOKEN:?GH_TOKEN is required when RELEASE_CHECK_RUNS_FILE is not set}"
  check_runs="$(gh api --paginate \
    -H 'Accept: application/vnd.github+json' \
    "repos/${GITHUB_REPOSITORY}/commits/${GITHUB_SHA}/check-runs" \
    --jq '.check_runs[] | [.name, .status, (.conclusion // "")] | @tsv')"
fi

required_checks=(
  "Backend"
  "Frontend"
  "Python extractor"
  "PostgreSQL integration"
  "Isolated Playwright"
  "Compose and containers"
  "Gitleaks"
)

for required in "${required_checks[@]}"; do
  matching="$(printf '%s\n' "${check_runs}" | awk -F '\t' -v expected="${required}" \
    '$1 == expected || $1 == "CI / " expected || $1 == "Secret scan / " expected')"
  if [ -z "${matching}" ]; then
    echo "required CI check is missing for ${GITHUB_SHA}: ${required}" >&2
    exit 1
  fi
  if ! printf '%s\n' "${matching}" | awk -F '\t' \
    '$2 == "completed" && $3 == "success" { found = 1 } END { exit(found ? 0 : 1) }'; then
    echo "required CI check did not complete successfully: ${required}" >&2
    printf '%s\n' "${matching}" >&2
    exit 1
  fi
done
