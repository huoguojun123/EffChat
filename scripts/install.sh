#!/usr/bin/env bash
set -euo pipefail

# One-command installer and updater for the registry deployment. The deployed
# directory has one active .env and one compose.yml; application migrations are
# embedded in the EffChat image, while PostgreSQL remains a separate service.
umask 077

REPO="${EFFCHAT_REPOSITORY:-huoguojun123/EffChat}"
DEFAULT_VERSION="v0.4.1-beta.8"
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
    local message="$1" default="$2" answer=""
    if [ -n "$tty_fd" ]; then
        read -r -u "$tty_fd" -p "$message [$default]: " answer
        printf '\n' >&2
    fi
    printf '%s' "${answer:-$default}"
}

prompt_secret() {
    local message="$1" answer=""
    if [ -n "$tty_fd" ]; then
        read -r -s -u "$tty_fd" -p "$message: " answer
        printf '\n' >&2
    fi
    printf '%s' "$answer"
}

random_hex() {
    od -An -N32 -tx1 /dev/urandom | tr -d ' \n'
}

case "$INSTALL_DIR" in
    ""|/|.|..|*/..|*/.) die "EFFCHAT_HOME must be a dedicated installation directory" ;;
esac

env_value() {
    local key="$1" file="$2" value
    value="$(awk -F= -v key="$key" '$1 == key {sub(/^[^=]*=/, ""); print; exit}' "$file")"
    case "$value" in
        \'*\')
            value="${value:1:${#value}-2}"
            printf '%s' "$value" | sed "s/\\\\'/'/g"
            ;;
        \"*\")
            value="${value:1:${#value}-2}"
            printf '%s' "$value" | sed 's/\\"/"/g; s/\\\\/\\/g'
            ;;
        *) printf '%s' "$value" ;;
    esac
}

env_line() {
    local key="$1" value="$2" escaped quote_escape
    case "$value" in
        *$'\n'*|*$'\r'*) die "$key cannot contain a newline" ;;
    esac
    escaped="$value"
    quote_escape="\\'"
    escaped="${escaped//\'/$quote_escape}"
    printf "%s='%s'" "$key" "$escaped"
}

set_env_value() {
    local file="$1" key="$2" value="$3" replacement tmp found=0 line
    replacement="$(env_line "$key" "$value")"
    tmp="$(mktemp "$(dirname "$file")/.effchat-env.XXXXXX")"
    while IFS= read -r line || [ -n "$line" ]; do
        case "$line" in
            "$key="*)
                if [ "$found" -eq 0 ]; then
                    printf '%s\n' "$replacement" >> "$tmp"
                    found=1
                fi
                ;;
            *) printf '%s\n' "$line" >> "$tmp" ;;
        esac
    done < "$file"
    if [ "$found" -eq 0 ]; then
        printf '%s\n' "$replacement" >> "$tmp"
    fi
    chmod 600 "$tmp"
    mv "$tmp" "$file"
}

MODE="install"
OLD_COMPOSE=""
OLD_ENV=""
TARGET_ENV="$INSTALL_DIR/.env"
TARGET_COMPOSE="$INSTALL_DIR/compose.yml"

if [ -z "${EFFCHAT_HOME:-}" ]; then
    INSTALL_DIR="$(prompt "Install or update directory" "$INSTALL_DIR")"
    TARGET_ENV="$INSTALL_DIR/.env"
    TARGET_COMPOSE="$INSTALL_DIR/compose.yml"
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
                OLD_COMPOSE="$candidate"
                break
            fi
        done
        [ -n "$OLD_COMPOSE" ] || die "no supported Compose file found; refusing to update an unknown directory"
        grep -Eq 'effchat-(backend|web|py-extractor)|/effchat:' "$OLD_COMPOSE" \
            || die "Compose file is not a recognized EffChat deployment"

        for candidate in "$INSTALL_DIR/.env" "$INSTALL_DIR/.env.docker" "$INSTALL_DIR/../.env.docker"; do
            if [ -f "$candidate" ]; then
                OLD_ENV="$candidate"
                break
            fi
        done
        [ -n "$OLD_ENV" ] || die "no supported environment file found"
        [ ! -L "$OLD_ENV" ] || die "environment file must not be a symlink"
        WEB_PORT="$(env_value WEB_PORT "$OLD_ENV")"
        WEB_PORT="${WEB_PORT:-8088}"
    fi
else
    mkdir -p "$INSTALL_DIR"
fi

if [ "$MODE" = install ] && [ -z "${EFFCHAT_WEB_PORT:-}" ]; then
    WEB_PORT="$(prompt "Web port" "$WEB_PORT")"
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
[ -f "$source_dir/docker-compose.registry.yml" ] || die "release archive is missing docker-compose.registry.yml"

if [ "$MODE" = "update" ]; then
    mkdir -p "$INSTALL_DIR/deployment-backups"
    backup_dir="$(mktemp -d "$INSTALL_DIR/deployment-backups/$(date -u +%Y%m%dT%H%M%SZ).XXXXXX")"
    cp -p "$OLD_COMPOSE" "$backup_dir/$(basename "$OLD_COMPOSE")"
    cp -p "$OLD_ENV" "$backup_dir/$(basename "$OLD_ENV")"
    if [ "$OLD_ENV" != "$TARGET_ENV" ]; then
        cp -p "$OLD_ENV" "$TARGET_ENV"
    fi
    chmod 600 "$TARGET_ENV"

    if [ -d "$INSTALL_DIR/migrations" ] && [ ! -L "$INSTALL_DIR/migrations" ]; then
        mv "$INSTALL_DIR/migrations" "$backup_dir/migrations"
    fi
    if [ -d "$INSTALL_DIR/backend/migrations" ] && [ ! -L "$INSTALL_DIR/backend/migrations" ]; then
        mv "$INSTALL_DIR/backend/migrations" "$backup_dir/backend-migrations"
    fi
    if [ "$OLD_ENV" != "$TARGET_ENV" ] && [ "$(dirname "$OLD_ENV")" = "$INSTALL_DIR" ]; then
        mv "$OLD_ENV" "$backup_dir/active-$(basename "$OLD_ENV")"
    fi
    if [ "$OLD_COMPOSE" != "$TARGET_COMPOSE" ] && [ "$(dirname "$OLD_COMPOSE")" = "$INSTALL_DIR" ]; then
        mv "$OLD_COMPOSE" "$backup_dir/active-$(basename "$OLD_COMPOSE")"
    fi
else
    : > "$TARGET_ENV"
    chmod 600 "$TARGET_ENV"
fi

compose_tmp="$TARGET_COMPOSE.effchat-new"
cp "$source_dir/docker-compose.registry.yml" "$compose_tmp"
mv "$compose_tmp" "$TARGET_COMPOSE"

compose_project="$(env_value COMPOSE_PROJECT_NAME "$TARGET_ENV" || true)"
set_env_value "$TARGET_ENV" COMPOSE_PROJECT_NAME "${compose_project:-effchat}"
data_dir="${EFFCHAT_DATA_DIR:-$(env_value DATA_DIR "$TARGET_ENV" || true)}"
set_env_value "$TARGET_ENV" DATA_DIR "${data_dir:-./data}"
set_env_value "$TARGET_ENV" WEB_PORT "$WEB_PORT"
set_env_value "$TARGET_ENV" BACKEND_PORT "${EFFCHAT_BACKEND_PORT:-$(env_value BACKEND_PORT "$TARGET_ENV" || true)}"
if [ -z "$(env_value BACKEND_PORT "$TARGET_ENV")" ]; then
    set_env_value "$TARGET_ENV" BACKEND_PORT 18080
fi
set_env_value "$TARGET_ENV" EFFCHAT_VERSION "$VERSION"

database_mode="${EFFCHAT_DATABASE_MODE:-}"
if [ -z "$database_mode" ] && [ "$MODE" = "update" ]; then
    existing_url="$(env_value DATABASE_URL "$TARGET_ENV" || true)"
    existing_host="$(env_value DB_HOST "$TARGET_ENV" || true)"
    if [ -n "$existing_url" ] || { [ -n "$existing_host" ] && [ "$existing_host" != postgres ]; }; then
        database_mode=external
    else
        database_mode=bundled
    fi
fi
if [ -z "$database_mode" ]; then
    database_mode="$(prompt "Database mode (bundled/external)" "bundled")"
fi
case "$database_mode" in
    bundled|external) ;;
    *) die "database mode must be bundled or external" ;;
esac

if [ "$database_mode" = bundled ]; then
    postgres_user="${EFFCHAT_DB_USER:-$(env_value POSTGRES_USER "$TARGET_ENV" || true)}"
    postgres_user="${postgres_user:-effchat}"
    postgres_db="${EFFCHAT_DB_NAME:-$(env_value POSTGRES_DB "$TARGET_ENV" || true)}"
    postgres_db="${postgres_db:-effchat}"
    postgres_password="${EFFCHAT_DB_PASSWORD:-$(env_value POSTGRES_PASSWORD "$TARGET_ENV" || true)}"
    postgres_password="${postgres_password:-$(random_hex)}"

    set_env_value "$TARGET_ENV" COMPOSE_PROFILES bundled-db
    set_env_value "$TARGET_ENV" DATABASE_URL ""
    set_env_value "$TARGET_ENV" POSTGRES_USER "$postgres_user"
    set_env_value "$TARGET_ENV" POSTGRES_DB "$postgres_db"
    set_env_value "$TARGET_ENV" POSTGRES_PASSWORD "$postgres_password"
    set_env_value "$TARGET_ENV" DB_HOST postgres
    set_env_value "$TARGET_ENV" DB_PORT 5432
    set_env_value "$TARGET_ENV" DB_USER "$postgres_user"
    set_env_value "$TARGET_ENV" DB_NAME "$postgres_db"
    set_env_value "$TARGET_ENV" DB_PASSWORD "$postgres_password"
    set_env_value "$TARGET_ENV" DB_SSLMODE disable
else
    database_url="${DATABASE_URL:-$(env_value DATABASE_URL "$TARGET_ENV" || true)}"
    set_env_value "$TARGET_ENV" COMPOSE_PROFILES ""
    if [ -n "$database_url" ]; then
        set_env_value "$TARGET_ENV" DATABASE_URL "$database_url"
    else
        db_host="${EFFCHAT_DB_HOST:-$(env_value DB_HOST "$TARGET_ENV" || true)}"
        db_port="${EFFCHAT_DB_PORT:-$(env_value DB_PORT "$TARGET_ENV" || true)}"
        db_name="${EFFCHAT_DB_NAME:-$(env_value DB_NAME "$TARGET_ENV" || true)}"
        db_user="${EFFCHAT_DB_USER:-$(env_value DB_USER "$TARGET_ENV" || true)}"
        db_password="${EFFCHAT_DB_PASSWORD:-$(env_value DB_PASSWORD "$TARGET_ENV" || true)}"
        db_sslmode="${EFFCHAT_DB_SSLMODE:-$(env_value DB_SSLMODE "$TARGET_ENV" || true)}"

        if [ -n "$tty_fd" ]; then
            db_host="$(prompt "PostgreSQL host" "${db_host:-127.0.0.1}")"
            db_port="$(prompt "PostgreSQL port" "${db_port:-5432}")"
            db_name="$(prompt "PostgreSQL database" "${db_name:-effchat}")"
            db_user="$(prompt "PostgreSQL user" "${db_user:-effchat}")"
            if [ -z "$db_password" ]; then
                db_password="$(prompt_secret "PostgreSQL password")"
            fi
            db_sslmode="$(prompt "PostgreSQL SSL mode" "${db_sslmode:-require}")"
        fi
        [ -n "$db_host" ] || die "external PostgreSQL host is required"
        case "$db_port" in ''|*[!0-9]*) die "external PostgreSQL port must be numeric" ;; esac
        [ -n "$db_name" ] || die "external PostgreSQL database is required"
        [ -n "$db_user" ] || die "external PostgreSQL user is required"
        [ -n "$db_password" ] || die "external PostgreSQL password is required"
        [ -n "$db_sslmode" ] || die "external PostgreSQL SSL mode is required"

        set_env_value "$TARGET_ENV" DATABASE_URL ""
        set_env_value "$TARGET_ENV" DB_HOST "$db_host"
        set_env_value "$TARGET_ENV" DB_PORT "$db_port"
        set_env_value "$TARGET_ENV" DB_NAME "$db_name"
        set_env_value "$TARGET_ENV" DB_USER "$db_user"
        set_env_value "$TARGET_ENV" DB_PASSWORD "$db_password"
        set_env_value "$TARGET_ENV" DB_SSLMODE "$db_sslmode"
    fi
fi

jwt_secret="${EFFCHAT_JWT_SECRET:-$(env_value JWT_SECRET "$TARGET_ENV" || true)}"
jwt_secret="${jwt_secret:-$(random_hex)}"
set_env_value "$TARGET_ENV" JWT_SECRET "$jwt_secret"

if [ "$MODE" = update ]; then
    echo "Updating EffChat to ${VERSION}; existing settings and data are preserved."
else
    echo "Pulling EffChat ${VERSION}..."
fi
(
    cd "$INSTALL_DIR"
    docker compose pull
    docker compose up -d --wait
)

echo
echo "EffChat is ready: http://127.0.0.1:${WEB_PORT}"
echo "Installation directory: $INSTALL_DIR"
echo "Daily commands: cd $INSTALL_DIR && docker compose pull && docker compose up -d"
