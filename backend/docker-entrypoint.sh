#!/bin/sh
set -eu

mkdir -p \
  /app/storage/attachments/originals \
  /app/storage/attachments/extracted \
  /app/storage/attachments/ocr-staging \
  /app/storage/avatars \
  /app/storage/fonts \
  /app/storage/skills

# Keep the existing owner normalization for every backend-managed storage domain.
# Attachments are authenticated user content, so only their mode is tightened on
# startup; avatars, fonts, and skills retain their distinct mode semantics.
chown -R app:app /app/storage
chmod -R go-rwx /app/storage/attachments

exec su-exec app "$@"
