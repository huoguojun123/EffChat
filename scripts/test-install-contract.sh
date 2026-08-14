#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
SCRIPT="$ROOT/scripts/install.sh"
test -x "$SCRIPT"

grep -Fq 'umask 077' "$SCRIPT"
grep -Fq 'read -r -u "$tty_fd"' "$SCRIPT"
grep -Fq 'archive/refs/tags/${VERSION}.tar.gz' "$SCRIPT"
grep -Fq 'WEB_PORT=$WEB_PORT' "$SCRIPT"
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

fixture="$TMP_DIR/fixture/EffChat-v0.3.4-beta.3"
mkdir -p "$fixture/backend"
cp "$ROOT/docker-compose.registry.yml" "$fixture/docker-compose.registry.yml"
cp -R "$ROOT/backend/migrations" "$fixture/backend/migrations"
tar -czf "$TMP_DIR/release.tar.gz" -C "$TMP_DIR/fixture" "$(basename "$fixture")"

mkdir -p "$TMP_DIR/bin"
cat > "$TMP_DIR/bin/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
output=""
while [ "$#" -gt 0 ]; do
    if [ "$1" = "--output" ]; then
        output="$2"
        shift 2
        continue
    fi
    shift
done
[ -n "$output" ]
cp "$FAKE_ARCHIVE" "$output"
EOF
cat > "$TMP_DIR/bin/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$FAKE_DOCKER_LOG"
EOF
chmod +x "$TMP_DIR/bin/curl" "$TMP_DIR/bin/docker"

install_dir="$TMP_DIR/install"
PATH="$TMP_DIR/bin:$PATH" \
FAKE_ARCHIVE="$TMP_DIR/release.tar.gz" \
FAKE_DOCKER_LOG="$TMP_DIR/docker.log" \
EFFCHAT_NONINTERACTIVE=1 \
EFFCHAT_HOME="$install_dir" \
EFFCHAT_WEB_PORT=9090 \
bash "$SCRIPT" >/dev/null

test -f "$install_dir/compose.yml"
test -f "$install_dir/migrations/build_migration_script.sh"
test -f "$install_dir/migrations/production/001_schema.sql"
grep -Fxq 'DATA_DIR=./data' "$install_dir/.env.docker"
grep -Fxq 'MIGRATIONS_DIR=./migrations' "$install_dir/.env.docker"
grep -Fxq 'WEB_PORT=9090' "$install_dir/.env.docker"
grep -Eq '^POSTGRES_PASSWORD=[0-9a-f]{64}$' "$install_dir/.env.docker"
grep -Eq '^JWT_SECRET=[0-9a-f]{64}$' "$install_dir/.env.docker"
grep -Fq 'compose --env-file ' "$TMP_DIR/docker.log"
grep -Fq ' pull' "$TMP_DIR/docker.log"
grep -Fq ' up -d --wait' "$TMP_DIR/docker.log"

if stat -f '%Lp' "$install_dir/.env.docker" >/dev/null 2>&1; then
    test "$(stat -f '%Lp' "$install_dir/.env.docker")" = "600"
else
    test "$(stat -c '%a' "$install_dir/.env.docker")" = "600"
fi

grep -Fq 'DEFAULT_VERSION="v0.3.4-beta.3"' "$SCRIPT"
! grep -Fq 'POSTGRES_PASSWORD=change-this-postgres-password' "$SCRIPT"
! grep -Fq 'JWT_SECRET=your-secret-key-change-this-in-production' "$SCRIPT"
grep -Fq 'curl -fsSL https://raw.githubusercontent.com/huoguojun123/EffChat/main/scripts/install.sh | bash' "$ROOT/README.md"
grep -Fq 'curl -fsSL https://raw.githubusercontent.com/huoguojun123/EffChat/main/scripts/install.sh | bash' "$ROOT/README.en.md"
grep -Fq 'EFFCHAT_HOME=/srv/effchat bash' "$ROOT/docs/deployment.md"

echo "one-command install contract checks passed."
