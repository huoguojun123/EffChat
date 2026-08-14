#!/usr/bin/env bash
set -euo pipefail

# One-command installer for a fresh personal deployment. It downloads the
# compose template and migrations from one immutable release, then lets Docker
# pull the already-built images. Existing configuration and data are never
# replaced.
umask 077

REPO="${EFFCHAT_REPOSITORY:-huoguojun123/EffChat}"
DEFAULT_VERSION="v0.3.4-beta.3"
VERSION="${EFFCHAT_VERSION:-$DEFAULT_VERSION}"
INSTALL_DIR="${EFFCHAT_HOME:-$PWD/effchat}"
WEB_PORT="${EFFCHAT_WEB_PORT:-8088}"
ARCHIVE_URL="https://github.com/${REPO}/archive/refs/tags/${VERSION}.tar.gz"

die() {
    echo "EffChat installation failed: $*" >&2
    exit 1
}

command -v curl >/dev/null 2>&1 || die "curl is required"
command -v tar >/dev/null 2>&1 || die "tar is required"
command -v docker >/dev/null 2>&1 || die "Docker is required"
docker compose version >/dev/null 2>&1 || die "Docker Compose v2 is required"

tty_fd=""
if [ "${EFFCHAT_NONINTERACTIVE:-0}" != "1" ] && [ -r /dev/tty ]; then
    exec 3</dev/tty
    tty_fd=3
fi

prompt() {
    local message="$1"
    local default="$2"
    local answer=""
    if [ -n "$tty_fd" ]; then
        read -r -u "$tty_fd" -p "$message [$default]: " answer
        printf '\n' >&2
    fi
    printf '%s' "${answer:-$default}"
}

if [ -z "${EFFCHAT_HOME:-}" ]; then
    INSTALL_DIR="$(prompt "Install directory" "$INSTALL_DIR")"
fi
if [ -z "${EFFCHAT_WEB_PORT:-}" ]; then
    WEB_PORT="$(prompt "Web port" "$WEB_PORT")"
fi
case "$WEB_PORT" in
    ''|*[!0-9]*) die "Web port must be a number between 1 and 65535" ;;
esac
[ "$WEB_PORT" -ge 1 ] && [ "$WEB_PORT" -le 65535 ] || die "Web port must be a number between 1 and 65535"

case "$INSTALL_DIR" in
    ""|/|.|..|*/..|*/.) die "EFFCHAT_HOME must be a dedicated installation directory" ;;
esac

if [ -e "$INSTALL_DIR" ]; then
    [ -d "$INSTALL_DIR" ] || die "$INSTALL_DIR exists and is not a directory"
    [ -z "$(find "$INSTALL_DIR" -mindepth 1 -maxdepth 1 -print -quit)" ] || \
        die "$INSTALL_DIR is not empty; refusing to overwrite configuration or data"
else
    mkdir -p "$INSTALL_DIR"
fi

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/effchat-install.XXXXXX")"
cleanup() { rm -rf "$tmp_dir"; }
trap cleanup EXIT

archive="$tmp_dir/effchat.tar.gz"
curl --fail --location --proto '=https' --tlsv1.2 --silent --show-error \
    "$ARCHIVE_URL" --output "$archive"
tar -xzf "$archive" -C "$tmp_dir"
source_dir="$(find "$tmp_dir" -mindepth 1 -maxdepth 1 -type d -name 'EffChat-*' -print -quit)"
[ -n "$source_dir" ] || die "release archive did not contain an EffChat source directory"

for required in docker-compose.registry.yml backend/migrations/build_migration_script.sh backend/migrations/init.sql backend/migrations/production; do
    [ -e "$source_dir/$required" ] || die "release archive is missing $required"
done

cp "$source_dir/docker-compose.registry.yml" "$INSTALL_DIR/compose.yml"
mkdir -p "$INSTALL_DIR/migrations"
cp -R "$source_dir/backend/migrations/." "$INSTALL_DIR/migrations/"

random_hex() {
    od -An -N32 -tx1 /dev/urandom | tr -d ' \n'
}

cat > "$INSTALL_DIR/.env.docker" <<EOF
COMPOSE_PROJECT_NAME=effchat
DOCKER_NETWORK=effchat_net
DATA_DIR=./data
WEB_PORT=$WEB_PORT
BACKEND_PORT=18080
MIGRATIONS_DIR=./migrations
POSTGRES_USER=effchat
POSTGRES_DB=effchat
POSTGRES_PASSWORD=$(random_hex)
JWT_SECRET=$(random_hex)
EFFCHAT_VERSION=$VERSION
EOF

echo "Pulling EffChat ${VERSION} images..."
docker compose --env-file "$INSTALL_DIR/.env.docker" -f "$INSTALL_DIR/compose.yml" pull
echo "Starting EffChat..."
docker compose --env-file "$INSTALL_DIR/.env.docker" -f "$INSTALL_DIR/compose.yml" up -d --wait

echo
echo "EffChat is ready: http://127.0.0.1:8088"
echo "Installation directory: $INSTALL_DIR"
