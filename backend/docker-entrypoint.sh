#!/bin/sh
set -eu

mkdir -p \
  /app/storage/attachments/originals \
  /app/storage/attachments/extracted \
  /app/storage/attachments/ocr-staging \
  /app/storage/avatars \
  /app/storage/fonts \
  /app/storage/skills

# Attachments are authenticated user content, not public static assets. Normalize
# only this managed subtree on startup so legacy 0755/0644 files become private
# without changing the intentionally shareable avatar, font, or skill domains.
chown -R app:app /app/storage
chmod -R go-rwx /app/storage/attachments

exec su-exec app "$@"
