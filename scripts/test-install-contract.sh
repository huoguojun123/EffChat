#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
SCRIPT="$ROOT/scripts/install.sh"
test -x "$SCRIPT"

grep -Fq 'umask 077' "$SCRIPT"
grep -Fq 'archive/refs/tags/${VERSION}.tar.gz' "$SCRIPT"
grep -Fq 'docker-compose.registry.yml' "$SCRIPT"
grep -Fq 'MIGRATIONS_DIR=./migrations' "$SCRIPT"
grep -Fq 'refusing to overwrite configuration or data' "$SCRIPT"
grep -Fq 'docker compose --env-file "$INSTALL_DIR/.env.docker" -f "$INSTALL_DIR/compose.yml" pull' "$SCRIPT"
grep -Fq 'docker compose --env-file "$INSTALL_DIR/.env.docker" -f "$INSTALL_DIR/compose.yml" up -d --wait' "$SCRIPT"
! grep -Eq 'docker compose .*down -v|rm -rf .*data|rm -rf .*volume' "$SCRIPT"

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/effchat-install-contract.XXXXXX")"
trap 'rm -rf "$TMP_DIR"' EXIT

mkdir "$TMP_DIR/occupied"
printf 'keep\n' > "$TMP_DIR/occupied/.env.docker"
if EFFCHAT_HOME="$TMP_DIR/occupied" bash "$SCRIPT" >/dev/null 2>&1; then
    echo "installer accepted a non-empty target" >&2
    exit 1
fi

grep -Fq 'DEFAULT_VERSION="v0.3.4-beta.3"' "$SCRIPT"
! grep -Fq 'POSTGRES_PASSWORD=change-this-postgres-password' "$SCRIPT"
! grep -Fq 'JWT_SECRET=your-secret-key-change-this-in-production' "$SCRIPT"
grep -Fq 'curl -fsSL https://raw.githubusercontent.com/huoguojun123/EffChat/main/scripts/install.sh | bash' "$ROOT/README.md"
grep -Fq 'curl -fsSL https://raw.githubusercontent.com/huoguojun123/EffChat/main/scripts/install.sh | bash' "$ROOT/README.en.md"
grep -Fq 'EFFCHAT_HOME=/srv/effchat bash' "$ROOT/docs/deployment.md"

echo "one-command install contract checks passed."
