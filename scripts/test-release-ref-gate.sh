#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT

git -C "${tmp_dir}" init -q -b main
git -C "${tmp_dir}" config user.email test@example.invalid
git -C "${tmp_dir}" config user.name Test
printf 'main\n' >"${tmp_dir}/fixture"
git -C "${tmp_dir}" add fixture
git -C "${tmp_dir}" commit -q -m main
main_sha="$(git -C "${tmp_dir}" rev-parse HEAD)"
git -C "${tmp_dir}" update-ref refs/remotes/origin/main "${main_sha}"

checks_file="${tmp_dir}/checks.tsv"
printf '%s\tcompleted\tsuccess\n' \
  "Backend" \
  "Frontend" \
  "Python extractor" \
  "PostgreSQL integration" \
  "Isolated Playwright" \
  "Compose and containers" \
  "Gitleaks" >"${checks_file}"

run_gate() {
  (
    cd "${tmp_dir}"
    GITHUB_REF_NAME=v0.0.1 \
      GITHUB_REPOSITORY=example/effchat \
      GITHUB_SHA="$1" \
      RELEASE_CHECK_RUNS_FILE="$2" \
      "${repo_root}/scripts/verify-release-ref.sh"
  )
}

printf 'feature\n' >"${tmp_dir}/fixture"
git -C "${tmp_dir}" commit -qam feature
feature_sha="$(git -C "${tmp_dir}" rev-parse HEAD)"
if run_gate "${feature_sha}" "${checks_file}"; then
  echo "non-main tag unexpectedly passed" >&2
  exit 1
fi

awk -F '\t' '$1 != "Isolated Playwright"' \
  "${checks_file}" >"${tmp_dir}/missing-check.tsv"
if run_gate "${main_sha}" "${tmp_dir}/missing-check.tsv"; then
  echo "missing isolated Playwright check unexpectedly passed" >&2
  exit 1
fi

awk -F '\t' 'BEGIN { OFS = FS } $1 == "Frontend" { $3 = "failure" } { print }' \
  "${checks_file}" >"${tmp_dir}/failed-check.tsv"
if run_gate "${main_sha}" "${tmp_dir}/failed-check.tsv"; then
  echo "failed required check unexpectedly passed" >&2
  exit 1
fi

run_gate "${main_sha}" "${checks_file}"
echo "release ref gate tests passed"
