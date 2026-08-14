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
grep -Fq 'existing directory requires EFFCHAT_MODE=update' "$SCRIPT"
grep -Fq 'docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" pull' "$SCRIPT"
grep -Fq 'docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" up -d --wait' "$SCRIPT"
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
mkdir "$install_dir"
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

update_root="$TMP_DIR/update"
update_dir="$update_root/src"
mkdir -p "$update_dir/backend" "$update_root/data"
cp "$ROOT/docker-compose.registry.yml" "$update_dir/docker-compose.registry.yml"
cp -R "$ROOT/backend/migrations" "$update_dir/backend/migrations"
printf 'legacy migration marker\n' > "$update_dir/backend/migrations/old-marker.txt"
printf 'keep data\n' > "$update_root/data/sentinel.txt"
cat > "$update_root/.env.docker" <<'EOF'
COMPOSE_PROJECT_NAME=effchat_existing
DATA_DIR=../data
WEB_PORT=8188
POSTGRES_PASSWORD=keep-existing-password
JWT_SECRET=keep-existing-jwt
EFFCHAT_VERSION=v0.3.4-beta.2
EOF
chmod 600 "$update_root/.env.docker"
: > "$TMP_DIR/docker.log"

PATH="$TMP_DIR/bin:$PATH" \
FAKE_ARCHIVE="$TMP_DIR/release.tar.gz" \
FAKE_DOCKER_LOG="$TMP_DIR/docker.log" \
EFFCHAT_NONINTERACTIVE=1 \
EFFCHAT_MODE=update \
EFFCHAT_HOME="$update_dir" \
bash "$SCRIPT" >/dev/null

grep -Fxq 'COMPOSE_PROJECT_NAME=effchat_existing' "$update_root/.env.docker"
grep -Fxq 'DATA_DIR=../data' "$update_root/.env.docker"
grep -Fxq 'WEB_PORT=8188' "$update_root/.env.docker"
grep -Fxq 'POSTGRES_PASSWORD=keep-existing-password' "$update_root/.env.docker"
grep -Fxq 'JWT_SECRET=keep-existing-jwt' "$update_root/.env.docker"
grep -Fxq 'EFFCHAT_VERSION=v0.3.4-beta.3' "$update_root/.env.docker"
grep -Fxq 'keep data' "$update_root/data/sentinel.txt"
test -f "$update_dir/backend/migrations/production/001_schema.sql"
test ! -e "$update_dir/backend/migrations/old-marker.txt"
backup_dir="$(find "$update_dir/deployment-backups" -mindepth 1 -maxdepth 1 -type d -print -quit)"
test -n "$backup_dir"
test -f "$backup_dir/docker-compose.registry.yml"
test -f "$backup_dir/migrations/old-marker.txt"
grep -Fq 'pull' "$TMP_DIR/docker.log"
grep -Fq 'up -d --wait' "$TMP_DIR/docker.log"
docker compose --env-file "$update_root/.env.docker" -f "$update_dir/docker-compose.registry.yml" config >/dev/null

if stat -f '%Lp' "$update_root/.env.docker" >/dev/null 2>&1; then
    test "$(stat -f '%Lp' "$update_root/.env.docker")" = "600"
else
    test "$(stat -c '%a' "$update_root/.env.docker")" = "600"
fi

unknown_dir="$TMP_DIR/unknown"
mkdir -p "$unknown_dir/data"
printf 'services: {}\n' > "$unknown_dir/docker-compose.yml"
printf 'keep unknown data\n' > "$unknown_dir/data/sentinel.txt"
printf 'POSTGRES_PASSWORD=keep\nJWT_SECRET=keep\n' > "$unknown_dir/.env.docker"
if PATH="$TMP_DIR/bin:$PATH" \
    FAKE_ARCHIVE="$TMP_DIR/release.tar.gz" \
    FAKE_DOCKER_LOG="$TMP_DIR/docker.log" \
    EFFCHAT_NONINTERACTIVE=1 \
    EFFCHAT_MODE=update \
    EFFCHAT_HOME="$unknown_dir" \
    bash "$SCRIPT" >/dev/null 2>&1; then
    echo "installer accepted an unknown Compose layout" >&2
    exit 1
fi
grep -Fxq 'keep unknown data' "$unknown_dir/data/sentinel.txt"

grep -Fq 'DEFAULT_VERSION="v0.3.4-beta.3"' "$SCRIPT"
! grep -Fq 'POSTGRES_PASSWORD=change-this-postgres-password' "$SCRIPT"
! grep -Fq 'JWT_SECRET=your-secret-key-change-this-in-production' "$SCRIPT"
grep -Fq 'curl -fsSL https://raw.githubusercontent.com/huoguojun123/EffChat/main/scripts/install.sh | bash' "$ROOT/README.md"
grep -Fq 'curl -fsSL https://raw.githubusercontent.com/huoguojun123/EffChat/main/scripts/install.sh | bash' "$ROOT/README.en.md"
grep -Fq 'EFFCHAT_HOME=/srv/effchat bash' "$ROOT/docs/deployment.md"

echo "one-command install contract checks passed."
