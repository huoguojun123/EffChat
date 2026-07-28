#!/bin/sh
set -eu

mkdir -p \
  /app/storage/attachments/originals \
  /app/storage/attachments/extracted \
  /app/storage/attachments/ocr-staging \
  /app/storage/avatars \
  /app/storage/fonts \
  /app/storage/skills
chown -R app:app /app/storage

exec su-exec app "$@"
