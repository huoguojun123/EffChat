#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
SCRIPT="$ROOT/scripts/install.sh"
test -x "$SCRIPT"

grep -Fq 'umask 077' "$SCRIPT"
grep -Fq 'read -r -s -u "$tty_fd"' "$SCRIPT"
grep -Fq 'archive/refs/tags/${VERSION}.tar.gz' "$SCRIPT"
grep -Fq 'TARGET_ENV="$INSTALL_DIR/.env"' "$SCRIPT"
grep -Fq 'TARGET_COMPOSE="$INSTALL_DIR/compose.yml"' "$SCRIPT"
grep -Fq 'docker compose pull' "$SCRIPT"
grep -Fq 'docker compose up -d --wait' "$SCRIPT"
! grep -Eq 'docker compose .*down -v|rm -rf .*data|rm -rf .*volume' "$SCRIPT"

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/effchat-install-contract.XXXXXX")"
trap 'rm -rf "$TMP_DIR"' EXIT

fixture="$TMP_DIR/fixture/EffChat-v0.4.1-beta.2"
mkdir -p "$fixture"
cp "$ROOT/docker-compose.registry.yml" "$fixture/docker-compose.registry.yml"
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
printf '%s\t%s\n' "$PWD" "$*" >> "$FAKE_DOCKER_LOG"
EOF
chmod +x "$TMP_DIR/bin/curl" "$TMP_DIR/bin/docker"

run_installer() {
    PATH="$TMP_DIR/bin:$PATH" \
    FAKE_ARCHIVE="$TMP_DIR/release.tar.gz" \
    FAKE_DOCKER_LOG="$TMP_DIR/docker.log" \
    EFFCHAT_NONINTERACTIVE=1 \
    bash "$SCRIPT" >/dev/null
}

install_dir="$TMP_DIR/install"
mkdir "$install_dir"
EFFCHAT_HOME="$install_dir" \
EFFCHAT_WEB_PORT=9090 \
EFFCHAT_DATABASE_MODE=bundled \
EFFCHAT_DB_PASSWORD="fixture-'bundled-\\password-#-\$" \
run_installer

test -f "$install_dir/compose.yml"
test -f "$install_dir/.env"
test ! -e "$install_dir/.env.docker"
test ! -e "$install_dir/migrations"
grep -Fxq "DATA_DIR='./data'" "$install_dir/.env"
grep -Fxq "WEB_PORT='9090'" "$install_dir/.env"
grep -Fxq "COMPOSE_PROFILES='bundled-db'" "$install_dir/.env"
grep -Fxq "DB_HOST='postgres'" "$install_dir/.env"
resolved_password="$(docker compose --env-file "$install_dir/.env" -f "$install_dir/compose.yml" config --environment \
    | awk -F= '$1 == "POSTGRES_PASSWORD" {sub(/^[^=]*=/, ""); print; exit}')"
test "$resolved_password" = "fixture-'bundled-\\password-#-\$"
grep -Eq "^JWT_SECRET='[0-9a-f]{64}'$" "$install_dir/.env"
grep -Fq $'\tcompose pull' "$TMP_DIR/docker.log"
grep -Fq $'\tcompose up -d --wait' "$TMP_DIR/docker.log"

if stat -f '%Lp' "$install_dir/.env" >/dev/null 2>&1; then
    test "$(stat -f '%Lp' "$install_dir/.env")" = "600"
else
    test "$(stat -c '%a' "$install_dir/.env")" = "600"
fi

EFFCHAT_HOME="$install_dir" \
EFFCHAT_MODE=update \
run_installer
resolved_password="$(docker compose --env-file "$install_dir/.env" -f "$install_dir/compose.yml" config --environment \
    | awk -F= '$1 == "POSTGRES_PASSWORD" {sub(/^[^=]*=/, ""); print; exit}')"
test "$resolved_password" = "fixture-'bundled-\\password-#-\$"

external_dir="$TMP_DIR/external"
mkdir "$external_dir"
: > "$TMP_DIR/docker.log"
EFFCHAT_HOME="$external_dir" \
EFFCHAT_DATABASE_MODE=external \
EFFCHAT_DB_HOST=db.example.invalid \
EFFCHAT_DB_PORT=6543 \
EFFCHAT_DB_NAME=effchat_external \
EFFCHAT_DB_USER=effchat_external_user \
EFFCHAT_DB_PASSWORD=fixture-external-password \
EFFCHAT_DB_SSLMODE=require \
run_installer

grep -Fxq "COMPOSE_PROFILES=''" "$external_dir/.env"
grep -Fxq "DB_HOST='db.example.invalid'" "$external_dir/.env"
grep -Fxq "DB_PORT='6543'" "$external_dir/.env"
grep -Fxq "DB_SSLMODE='require'" "$external_dir/.env"
test ! -d "$external_dir/data/postgres"

url_dir="$TMP_DIR/external-url"
mkdir "$url_dir"
DATABASE_URL='postgres://fixture:pa%23ss@db.example.invalid:5432/effchat?sslmode=require' \
EFFCHAT_HOME="$url_dir" \
EFFCHAT_DATABASE_MODE=external \
run_installer
grep -Fxq "DATABASE_URL='postgres://fixture:pa%23ss@db.example.invalid:5432/effchat?sslmode=require'" "$url_dir/.env"
grep -Fxq "COMPOSE_PROFILES=''" "$url_dir/.env"

update_dir="$TMP_DIR/update"
mkdir -p "$update_dir/migrations/production" "$update_dir/data/storage"
cat > "$update_dir/compose.yml" <<'EOF'
services:
  backend:
    image: gjhuo/effchat-backend:v0.4.0-beta.1
  migrate:
    volumes:
      - ./migrations:/migrations:ro
EOF
cat > "$update_dir/.env.docker" <<'EOF'
COMPOSE_PROJECT_NAME=effchat_existing
DATA_DIR=./data
WEB_PORT=8188
POSTGRES_USER=effchat
POSTGRES_DB=effchat
POSTGRES_PASSWORD=keep-existing-password
JWT_SECRET=keep-existing-jwt
EFFCHAT_VERSION=v0.4.0-beta.1
CUSTOM_SETTING=keep-me
EOF
printf 'legacy migration marker\n' > "$update_dir/migrations/production/old-marker.sql"
printf 'keep data\n' > "$update_dir/data/storage/sentinel.txt"
: > "$TMP_DIR/docker.log"

EFFCHAT_HOME="$update_dir" \
EFFCHAT_MODE=update \
run_installer

test -f "$update_dir/.env"
test ! -e "$update_dir/.env.docker"
test ! -e "$update_dir/migrations"
grep -Fxq 'CUSTOM_SETTING=keep-me' "$update_dir/.env"
grep -Fxq "WEB_PORT='8188'" "$update_dir/.env"
grep -Fxq "POSTGRES_PASSWORD='keep-existing-password'" "$update_dir/.env"
grep -Fxq "DB_PASSWORD='keep-existing-password'" "$update_dir/.env"
grep -Fxq "JWT_SECRET='keep-existing-jwt'" "$update_dir/.env"
grep -Fxq "EFFCHAT_VERSION='v0.4.1-beta.2'" "$update_dir/.env"
grep -Fxq 'keep data' "$update_dir/data/storage/sentinel.txt"
backup_dir="$(find "$update_dir/deployment-backups" -mindepth 1 -maxdepth 1 -type d -print -quit)"
test -n "$backup_dir"
test -f "$backup_dir/compose.yml"
test -f "$backup_dir/.env.docker"
test -f "$backup_dir/migrations/production/old-marker.sql"
docker compose --env-file "$update_dir/.env" -f "$update_dir/compose.yml" config >/dev/null

unknown_dir="$TMP_DIR/unknown"
mkdir -p "$unknown_dir/data"
printf 'services: {}\n' > "$unknown_dir/compose.yml"
printf 'keep unknown data\n' > "$unknown_dir/data/sentinel.txt"
printf 'JWT_SECRET=keep\n' > "$unknown_dir/.env"
if EFFCHAT_HOME="$unknown_dir" EFFCHAT_MODE=update run_installer 2>/dev/null; then
    echo "installer accepted an unknown Compose layout" >&2
    exit 1
fi
grep -Fxq 'keep unknown data' "$unknown_dir/data/sentinel.txt"

grep -Fq 'DEFAULT_VERSION="v0.4.1-beta.2"' "$SCRIPT"
grep -Fq 'curl -fsSL https://raw.githubusercontent.com/huoguojun123/EffChat/main/scripts/install.sh | bash' "$ROOT/README.md"
grep -Fq 'curl -fsSL https://raw.githubusercontent.com/huoguojun123/EffChat/main/scripts/install.sh | bash' "$ROOT/README.en.md"

echo "one-command install contract checks passed."
