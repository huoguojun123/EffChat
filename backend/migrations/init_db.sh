#!/usr/bin/env bash

# ============================================
# 数据库初始化/升级脚本
# 用法: ./init_db.sh [reset]
#
# 发布前迁移体系只有两类入口：
# - init.sql：全新库 schema 快照。
# - production/*.sql：唯一增量迁移链，本脚本按文件名顺序执行并写入 schema_migrations。
# ============================================

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BACKEND_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
PROJECT_DIR="$(cd "$BACKEND_DIR/.." && pwd)"

trim() {
    local value="$1"
    value="${value#"${value%%[![:space:]]*}"}"
    value="${value%"${value##*[![:space:]]}"}"
    printf '%s' "$value"
}

load_env_file() {
    local file="$1"
    [ -f "$file" ] || return 0

    while IFS='=' read -r key value || [ -n "$key" ]; do
        key="${key%%$'\r'}"
        value="${value%%$'\r'}"

        [[ -z "$key" || "$key" =~ ^[[:space:]]*# ]] && continue
        key="$(trim "$key")"
        [[ "$key" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || continue

        case "$key" in
            DB_HOST|DB_PORT|DB_USER|DB_PASSWORD|DB_NAME|DB_SSLMODE)
                if [ -z "${!key+x}" ]; then
                    value="$(trim "$value")"
                    value="${value%\"}"
                    value="${value#\"}"
                    value="${value%\'}"
                    value="${value#\'}"
                    export "$key=$value"
                fi
                ;;
        esac
    done < "$file"
}

load_env_file "$BACKEND_DIR/.env"
load_env_file "$PROJECT_DIR/.env"

DB_HOST="${DB_HOST:-localhost}"
DB_PORT="${DB_PORT:-5432}"
DB_USER="${DB_USER:-postgres}"
DB_PASSWORD="${DB_PASSWORD:-123456}"
DB_NAME="${DB_NAME:-fchat}"
DB_SSLMODE="${DB_SSLMODE:-disable}"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

print_step() {
    echo -e "${YELLOW}[$1]${NC} $2"
}

fail() {
    echo -e "${RED}✗${NC} $1"
    [ -n "${2:-}" ] && printf '%b\n' "$2"
    exit 1
}

run_psql() {
    case "$PSQL_MODE" in
        local)
            PGPASSWORD="$DB_PASSWORD" PGSSLMODE="$DB_SSLMODE" psql \
                -v ON_ERROR_STOP=1 \
                -h "$DB_HOST" \
                -p "$DB_PORT" \
                -U "$DB_USER" \
                "$@"
            ;;
        docker)
            docker exec -i \
                -e "PGPASSWORD=$DB_PASSWORD" \
                -e "PGSSLMODE=$DB_SSLMODE" \
                "$POSTGRES_CONTAINER" \
                psql -v ON_ERROR_STOP=1 -U "$DB_USER" "$@"
            ;;
    esac
}

quote_ident() {
    local value="${1//\"/\"\"}"
    printf '"%s"' "$value"
}

sql_literal() {
    local value="$1"
    local quote="'"
    local escaped="''"
    value="${value//$quote/$escaped}"
    printf "'%s'" "$value"
}

find_postgres_containers() {
    docker ps --format '{{.Names}} {{.Image}} {{.Ports}}' 2>/dev/null \
        | awk 'tolower($0) ~ /postgres|pgvector|5432/ {print $1}'
}

select_psql_runner() {
    if [ -n "$POSTGRES_CONTAINER" ]; then
        if [[ ! "$POSTGRES_CONTAINER" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]*$ ]]; then
            fail "POSTGRES_CONTAINER 不是有效的容器名称"
        fi
        command -v docker >/dev/null 2>&1 || fail "已指定 POSTGRES_CONTAINER，但未找到 Docker 客户端"
        PSQL_MODE="docker"
        return
    fi

    if command -v psql >/dev/null 2>&1; then
        PSQL_MODE="local"
        return
    fi

    if command -v docker >/dev/null 2>&1; then
        local candidates candidate_count
        candidates="$(find_postgres_containers)"
        candidate_count="$(printf '%s\n' "$candidates" | awk 'NF { count++ } END { print count + 0 }')"
        if [ "$candidate_count" -gt 1 ]; then
            fail "检测到多个 PostgreSQL 容器，无法安全自动选择" "候选容器:\n$candidates\n\n请显式设置 POSTGRES_CONTAINER 后重试。"
        fi
        POSTGRES_CONTAINER="$candidates"
        if [ -n "$POSTGRES_CONTAINER" ]; then
            PSQL_MODE="docker"
            return
        fi
    fi

    fail "未找到可用的 PostgreSQL 客户端" "本机没有 psql，也没有检测到正在运行的 PostgreSQL/pgvector Docker 容器。"
}

database_exists() {
    local db_literal result
    db_literal="$(sql_literal "$DB_NAME")"
    result="$(run_psql -d postgres -tAc "SELECT 1 FROM pg_database WHERE datname = $db_literal;" 2>/dev/null || true)"
    [ "$result" = "1" ]
}

apply_migrations() {
    local migration_script output migration version checksum legacy_checksum sql_file
    migration_script="$(mktemp)"

    {
        cat <<'SQL'
SELECT pg_advisory_lock(823764219);
CREATE TABLE IF NOT EXISTS schema_migrations (
  version TEXT PRIMARY KEY,
  applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  checksum TEXT NOT NULL DEFAULT ''
);
ALTER TABLE schema_migrations ADD COLUMN IF NOT EXISTS checksum TEXT NOT NULL DEFAULT '';
SQL

        for migration in "${migrations[@]}"; do
            version="$(basename "$migration")"
            if [ "$version" = "001_schema.sql" ]; then
                checksum="legacy-baseline-v1"
                checksum_reconcile="TRUE"
            else
                checksum="$(shasum -a 256 "$migration" | awk '{print $1}')"
                checksum_reconcile="checksum = ''"
            fi
            sql_file="$migration"
            if [ "$version" = "001_schema.sql" ]; then
                sql_file="$SCRIPT_DIR/init.sql"
            fi

            cat <<SQL
UPDATE schema_migrations
SET checksum = '$checksum'
WHERE version = '$version' AND ($checksum_reconcile);
SELECT NOT EXISTS (
  SELECT 1 FROM schema_migrations
  WHERE version = '$version' AND checksum <> '$checksum'
) AS migration_checksum_matches \gset
\if :migration_checksum_matches
\else
\echo migration checksum mismatch: $version
SELECT 1 / 0;
\endif
SELECT NOT EXISTS (SELECT 1 FROM schema_migrations WHERE version = '$version') AS apply_migration \gset
\if :apply_migration
\echo apply migration $version
SQL
            cat "$sql_file"
            cat <<SQL
INSERT INTO schema_migrations (version, checksum) VALUES ('$version', '$checksum');
\else
\echo skip migration $version
\endif
SQL
        done
        cat <<'SQL'
SELECT pg_advisory_unlock(823764219);
SQL
    } > "$migration_script"

    output="$(run_psql -d "$DB_NAME" < "$migration_script" 2>&1)"
    status=$?
    rm -f "$migration_script"
    [ "$status" -eq 0 ] || fail "迁移执行失败" "$output"
    printf '%s\n' "$output"
}

echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}EffChat 数据库初始化/升级${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""

PSQL_MODE=""
POSTGRES_CONTAINER="${POSTGRES_CONTAINER:-}"
select_psql_runner
if [ "$PSQL_MODE" = "docker" ]; then
    echo -e "${GREEN}✓${NC} 使用 Docker 容器执行 psql: $POSTGRES_CONTAINER"
else
    echo -e "${GREEN}✓${NC} 使用本机 psql"
fi
echo ""

print_step "1/4" "检查 PostgreSQL 连接..."
if output="$(run_psql -d postgres -tAc "SELECT version();" 2>&1)"; then
    echo -e "${GREEN}✓${NC} PostgreSQL 连接成功"
else
    fail "PostgreSQL 连接失败" "请检查 PostgreSQL 是否运行在 ${DB_HOST}:${DB_PORT}，以及 backend/.env 中的 DB_USER / DB_PASSWORD / DB_SSLMODE 是否正确。\n\n原始错误:\n$output"
fi
echo ""

if [ "${1:-}" = "reset" ]; then
    if [ "${CONFIRM_RESET:-}" != "DELETE_FCHAT_DB" ]; then
        fail "reset 会删除整个数据库 $DB_NAME" "如确认用于本地开发重建，请执行：CONFIRM_RESET=DELETE_FCHAT_DB ./init_db.sh reset"
    fi
    print_step "2/5" "删除已有数据库..."
    db_ident="$(quote_ident "$DB_NAME")"
    if output="$(run_psql -d postgres -c "DROP DATABASE IF EXISTS $db_ident WITH (FORCE);" 2>&1)"; then
        echo -e "${GREEN}✓${NC} 数据库已删除"
    else
        fail "数据库删除失败" "$output"
    fi
    echo ""
fi

db_ident="$(quote_ident "$DB_NAME")"
print_step "2/4" "创建数据库..."
if database_exists; then
    echo -e "${YELLOW}!${NC} 数据库 $DB_NAME 已存在（跳过创建）"
else
    if output="$(run_psql -d postgres -c "CREATE DATABASE $db_ident;" 2>&1)"; then
        echo -e "${GREEN}✓${NC} 数据库 $DB_NAME 创建成功"
    else
        fail "数据库 $DB_NAME 创建失败" "$output"
    fi
fi
echo ""

print_step "3/4" "执行 production 增量迁移链..."
shopt -s nullglob
migrations=("$SCRIPT_DIR"/production/*.sql)
shopt -u nullglob
[ ${#migrations[@]} -gt 0 ] || fail "未找到 production 迁移文件" "$SCRIPT_DIR/production/*.sql"
apply_migrations
echo ""

print_step "4/4" "完成"
echo -e "${GREEN}✓${NC} 已按 production 链完成初始化/升级；本脚本不再提供测试数据入口。"
echo ""

echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}数据库初始化/升级完成${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""
echo "数据库信息:"
echo "  主机: ${DB_HOST}:${DB_PORT}"
echo "  数据库: $DB_NAME"
echo "  用户: $DB_USER"
if [ "$PSQL_MODE" = "docker" ]; then
    echo "  容器: $POSTGRES_CONTAINER"
fi
echo ""
echo "快速查询:"
if [ "$PSQL_MODE" = "docker" ]; then
    echo "  docker exec -it $POSTGRES_CONTAINER psql -U $DB_USER -d $DB_NAME"
else
    echo "  PGPASSWORD=*** psql -h $DB_HOST -p $DB_PORT -U $DB_USER -d $DB_NAME"
fi
echo ""
echo "已记录迁移:"
run_psql -d "$DB_NAME" -c "SELECT version, checksum, applied_at FROM schema_migrations ORDER BY version;" 2>/dev/null || true
echo ""
