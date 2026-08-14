#!/usr/bin/env bash
set -euo pipefail

# One-command installer and updater. It downloads the Compose template and
# migrations from one immutable release, then lets Docker pull the already-built
# images. Updates preserve the existing environment and data mounts.
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

case "$INSTALL_DIR" in
    ""|/|.|..|*/..|*/.) die "EFFCHAT_HOME must be a dedicated installation directory" ;;
esac

env_value() {
    local key="$1"
    local file="$2"
    awk -F= -v key="$key" '$1 == key {sub(/^[^=]*=/, ""); print; exit}' "$file" | tr -d "\"'"
}

file_mode() {
    if stat -f '%Lp' "$1" >/dev/null 2>&1; then
        stat -f '%Lp' "$1"
    else
        stat -c '%a' "$1"
    fi
}

MODE="install"
COMPOSE_FILE=""
ENV_FILE="${EFFCHAT_ENV_FILE:-}"
MIGRATIONS_DIR_TARGET=""

if [ -z "${EFFCHAT_HOME:-}" ]; then
    INSTALL_DIR="$(prompt "Install or update directory" "$INSTALL_DIR")"
fi

if [ -e "$INSTALL_DIR" ]; then
    [ -d "$INSTALL_DIR" ] || die "$INSTALL_DIR exists and is not a directory"
    if [ -n "$(find "$INSTALL_DIR" -mindepth 1 -maxdepth 1 -print -quit)" ]; then
        MODE="${EFFCHAT_MODE:-}"
        if [ -z "$MODE" ] && [ -n "$tty_fd" ]; then
            MODE="$(prompt "Existing EffChat deployment found; type update to continue" "update")"
        fi
        [ "$MODE" = "update" ] || die "existing directory requires EFFCHAT_MODE=update or an interactive update confirmation"

        for candidate in "$INSTALL_DIR/compose.yml" "$INSTALL_DIR/docker-compose.registry.yml" "$INSTALL_DIR/docker-compose.yml"; do
            if [ -f "$candidate" ]; then
                COMPOSE_FILE="$candidate"
                break
            fi
        done
        [ -n "$COMPOSE_FILE" ] || die "no supported Compose file found; refusing to update an unknown directory"
        grep -Fq 'effchat-backend' "$COMPOSE_FILE" || die "Compose file is not an EffChat registry deployment"
        grep -Fq 'MIGRATIONS_DIR' "$COMPOSE_FILE" || die "Compose file has no migration mount contract"

        if [ -z "$ENV_FILE" ]; then
            for candidate in "$INSTALL_DIR/.env.docker" "$INSTALL_DIR/../.env.docker"; do
                if [ -f "$candidate" ]; then
                    ENV_FILE="$candidate"
                    break
                fi
            done
        fi
        if [ -z "$ENV_FILE" ]; then
            ENV_FILE="$(prompt "Environment file" "$INSTALL_DIR/.env.docker")"
        fi
        [ -f "$ENV_FILE" ] || die "environment file not found: $ENV_FILE"
        [ ! -L "$ENV_FILE" ] || die "environment file must not be a symlink"

        WEB_PORT="$(env_value WEB_PORT "$ENV_FILE")"
        WEB_PORT="${WEB_PORT:-8088}"
    else
        MODE="install"
    fi
else
    mkdir -p "$INSTALL_DIR"
fi

if [ "$MODE" = "install" ]; then
    if [ -z "${EFFCHAT_WEB_PORT:-}" ]; then
        WEB_PORT="$(prompt "Web port" "$WEB_PORT")"
    fi
    COMPOSE_FILE="$INSTALL_DIR/compose.yml"
    ENV_FILE="$INSTALL_DIR/.env.docker"
else
    migration_value="$(env_value MIGRATIONS_DIR "$ENV_FILE")"
    migration_value="${migration_value:-./backend/migrations}"
    case "$migration_value" in
        *..*|*[[:space:]]*) die "MIGRATIONS_DIR must be a simple path inside the deployment layout" ;;
        /*) MIGRATIONS_DIR_TARGET="$migration_value" ;;
        *) MIGRATIONS_DIR_TARGET="$INSTALL_DIR/${migration_value#./}" ;;
    esac
fi

case "$WEB_PORT" in
    ''|*[!0-9]*) die "Web port must be a number between 1 and 65535" ;;
esac
[ "$WEB_PORT" -ge 1 ] && [ "$WEB_PORT" -le 65535 ] || die "Web port must be a number between 1 and 65535"

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

if [ "$MODE" = "install" ]; then
    MIGRATIONS_DIR_TARGET="$INSTALL_DIR/migrations"
else
    mkdir -p "$INSTALL_DIR/deployment-backups"
    backup_dir="$(mktemp -d "$INSTALL_DIR/deployment-backups/$(date -u +%Y%m%dT%H%M%SZ).XXXXXX")"
    cp -p "$COMPOSE_FILE" "$backup_dir/$(basename "$COMPOSE_FILE")"
fi

mkdir -p "$(dirname "$MIGRATIONS_DIR_TARGET")"
if [ -e "$MIGRATIONS_DIR_TARGET" ]; then
    [ -d "$MIGRATIONS_DIR_TARGET" ] || die "migration path is not a directory: $MIGRATIONS_DIR_TARGET"
    [ ! -L "$MIGRATIONS_DIR_TARGET" ] || die "migration path must not be a symlink"
    [ "$MODE" = "update" ] || die "new installation unexpectedly contains migrations"
    mv "$MIGRATIONS_DIR_TARGET" "$backup_dir/migrations"
fi
mkdir -p "$MIGRATIONS_DIR_TARGET"
cp -R "$source_dir/backend/migrations/." "$MIGRATIONS_DIR_TARGET/"

compose_tmp="$COMPOSE_FILE.effchat-new"
cp "$source_dir/docker-compose.registry.yml" "$compose_tmp"
mv "$compose_tmp" "$COMPOSE_FILE"

random_hex() {
    od -An -N32 -tx1 /dev/urandom | tr -d ' \n'
}

if [ "$MODE" = "install" ]; then
    cat > "$ENV_FILE" <<EOF
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
else
    env_tmp="$(mktemp "$(dirname "$ENV_FILE")/.effchat-env.XXXXXX")"
    awk -v version="$VERSION" '
        BEGIN { found = 0 }
        /^EFFCHAT_VERSION[[:space:]]*=/ {
            print "EFFCHAT_VERSION=" version
            found = 1
            next
        }
        { print }
        END {
            if (!found) print "EFFCHAT_VERSION=" version
        }
    ' "$ENV_FILE" > "$env_tmp"
    chmod "$(file_mode "$ENV_FILE")" "$env_tmp"
    mv "$env_tmp" "$ENV_FILE"
fi

if [ "$MODE" = "update" ]; then
    echo "Updating EffChat to ${VERSION}; existing environment and data are preserved."
else
    echo "Pulling EffChat ${VERSION} images..."
fi
docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" pull
echo "Starting EffChat..."
docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" up -d --wait

echo
echo "EffChat is ready: http://127.0.0.1:${WEB_PORT}"
echo "Installation directory: $INSTALL_DIR"
echo "Compose file: $COMPOSE_FILE"
echo "Environment file: $ENV_FILE (secrets and data settings preserved)"
