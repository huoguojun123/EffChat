#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TARGET_DIR="${1:-"${ROOT_DIR}/../effchat-public-source"}"

if [[ "${TARGET_DIR}" == "${ROOT_DIR}" || "${TARGET_DIR}" == "${ROOT_DIR}/"* ]]; then
  echo "Refusing to export into the source tree: ${TARGET_DIR}" >&2
  exit 1
fi

mkdir -p "${TARGET_DIR}"

rsync -a --delete --delete-excluded \
  --exclude='.git/' \
  --exclude='.claude/' \
  --exclude='.serena/' \
  --exclude='.superpowers/' \
  --exclude='AGENTS.md' \
  --exclude='CLAUDE.md' \
  --exclude='.DS_Store' \
  --exclude='node_modules/' \
  --exclude='frontend/node_modules/' \
  --exclude='ui-baseline/' \
  --exclude='frontend/dist/' \
  --exclude='frontend/test-results/' \
  --exclude='frontend/playwright-report/' \
  --exclude='frontend/blob-report/' \
  --exclude='backend/bin/' \
  --exclude='backend/uploads/' \
  --exclude='backend/data/' \
  --exclude='data/' \
  --exclude='uploads/' \
  --exclude='logs/' \
  --exclude='*.log' \
  --exclude='.env' \
  --exclude='.env.docker' \
  --exclude='.env.docker.local' \
  --exclude='.env.local' \
  --exclude='.env.*.local' \
  --exclude='*.env' \
  --exclude='docs/05-排障/' \
  --exclude='docs/06-项目全量审查/' \
  --exclude='docs/archive/' \
  --exclude='docs/superpowers/' \
  --exclude='docs/03-实施计划/开源初版功能取舍选择表.md' \
  "${ROOT_DIR}/" "${TARGET_DIR}/"

rm -f \
  "${TARGET_DIR}/backend/API.md" \
  "${TARGET_DIR}/backend/PROGRESS.md" \
  "${TARGET_DIR}/backend/test_api.sh" \
  "${TARGET_DIR}/backend/test_stream.sh"

if [[ -d "${TARGET_DIR}/docs" ]]; then
  find "${TARGET_DIR}/docs" -mindepth 1 -maxdepth 1 ! -name '03-实施计划' -exec rm -rf {} +
  if [[ -d "${TARGET_DIR}/docs/03-实施计划" ]]; then
    find "${TARGET_DIR}/docs/03-实施计划" -type f \
      ! -name 'Docker-Compose-部署.md' \
      ! -name '管理员配置指南.md' \
      ! -name '开源发布检查清单.md' \
      -delete
    find "${TARGET_DIR}/docs/03-实施计划" -type d -empty -delete
  fi
fi

echo "Public source exported to: ${TARGET_DIR}"
echo "Next: run the release leak-scan checklist before publishing."
