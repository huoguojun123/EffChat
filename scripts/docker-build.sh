#!/usr/bin/env bash
set -euo pipefail

SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
if [ "$(basename "$SRC_DIR")" = "src" ]; then
	DEPLOY_ROOT="$(cd "$SRC_DIR/.." && pwd -P)"
else
  DEPLOY_ROOT="$SRC_DIR"
fi
ENV_FILE="${ENV_FILE:-$DEPLOY_ROOT/.env.docker}"
EXAMPLE_ENV="$DEPLOY_ROOT/.env.docker.example"
COMPOSE=(docker compose --env-file "$ENV_FILE" -f "$SRC_DIR/docker-compose.yml")
# shellcheck source=compose-env.sh
source "$SRC_DIR/scripts/compose-env.sh"

# One owner drives both source hashing and Git dirty detection. These paths
# cover application source, Dockerfiles, build helpers, Compose behavior, the
# root context contract, and every project/license file copied into images.
BUILD_REF_PATHS=(
  backend
  frontend
  py-extractor
  scripts
  docker-compose.yml
  .dockerignore
  LICENSE
  NOTICE
  THIRD_PARTY_NOTICES.md
)

usage() {
  cat <<'USAGE'
Usage:
  scripts/docker-build.sh build      Build backend and web images
  scripts/docker-build.sh up         Build and start the full stack
  scripts/docker-build.sh config     Validate and render docker-compose.yml
  scripts/docker-build.sh build-ref  Print the build identifier for this source tree
  scripts/docker-build.sh logs       Follow service logs
  scripts/docker-build.sh down       Stop services without deleting DATA_DIR
  scripts/docker-build.sh reset-db   Destroy local data directories, then start a fresh database

Environment:
  ENV_FILE=/path/to/.env.docker      Override env file path

First run:
  cp .env.docker.example .env.docker
  edit .env.docker
USAGE
}

require_env_file() {
  if [ ! -f "$ENV_FILE" ]; then
    echo "Missing $ENV_FILE"
    echo "Create it with: cp $EXAMPLE_ENV $ENV_FILE"
    exit 1
  fi
}

validate_runtime_secrets() {
  require_env_file

  local jwt_secret postgres_password
  jwt_secret="$(env_value JWT_SECRET)"
  postgres_password="$(env_value POSTGRES_PASSWORD)"

  if [ -z "$jwt_secret" ] || [ "$jwt_secret" = "your-secret-key-change-this-in-production" ]; then
    echo "JWT_SECRET in $ENV_FILE must be replaced with a strong random value."
    exit 1
  fi

  if [ -z "$postgres_password" ] || [ "$postgres_password" = "change-this-postgres-password" ]; then
    echo "POSTGRES_PASSWORD in $ENV_FILE must be replaced before deployment."
    exit 1
  fi
}

data_dir() {
  local value
  value="$(env_value DATA_DIR)"
  if [ -z "$value" ]; then
    if [ "$(basename "$SRC_DIR")" = "src" ]; then
      value="../data"
    else
      value="./data"
    fi
  fi

  case "$value" in
    /*) printf '%s\n' "$value" ;;
    *) printf '%s/%s\n' "$SRC_DIR" "$value" ;;
  esac
}

prepare_data_dirs() {
  local dir
  dir="$(data_dir)"
  mkdir -p \
    "$dir/postgres" \
    "$dir/storage/attachments/originals" \
    "$dir/storage/attachments/extracted" \
    "$dir/storage/attachments/ocr-staging" \
    "$dir/storage/avatars" \
    "$dir/storage/fonts" \
    "$dir/storage/skills"
}

export_data_dir() {
  export DATA_DIR
  DATA_DIR="$(data_dir)"
}

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  elif command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 "$1" | awk '{print $NF}'
  else
    echo "A SHA-256 command is required to derive BUILD_REF." >&2
    return 1
  fi
}

sha256_stream() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 | awk '{print $1}'
  elif command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 | awk '{print $NF}'
  else
    echo "A SHA-256 command is required to derive BUILD_REF." >&2
    return 1
  fi
}

source_tree_hash() {
  (
    cd "$SRC_DIR"
    {
      find "${BUILD_REF_PATHS[@]}" \
        \( \( \
          -name node_modules -o -name dist -o -name dist-ssr -o -name bin -o \
          -name uploads -o -name data -o -name logs -o -name tmp -o -name coverage -o \
          -name test-results -o -name playwright-report -o -name blob-report -o \
          -name .venv -o -name __pycache__ \
        \) -prune \) -o \
        \( \( -type f -o -type l \) \
          ! -name '.env' ! -name '.env.*' ! -name '*.env' ! -name '*.env.*' \
          ! -name '.envrc' ! -name '.envrc.*' ! -name '*.local' ! -name '*.log' \
          ! -name '*.test' ! -name '*.out' ! -name '.DS_Store' -print \
        \)
    } | LC_ALL=C sort | while IFS= read -r file; do
      if [ -L "$file" ]; then
        printf 'link:%s  %s\n' "$(readlink "$file")" "$file"
      elif [ -f "$file" ]; then
        local mode='-'
        [ -x "$file" ] && mode='x'
        printf '%s:%s  %s\n' "$mode" "$(sha256_file "$file")" "$file"
      fi
    done
  ) | sha256_stream
}

export_build_ref() {
  export BUILD_REF
  if [ -n "${BUILD_REF:-}" ] && [ "$BUILD_REF" != "unknown" ]; then
    return
  fi

  local git_ref git_root git_root_raw tree_hash
  git_root_raw="$(git -C "$SRC_DIR" rev-parse --show-toplevel 2>/dev/null || true)"
  git_root=""
  if [ -n "$git_root_raw" ] && [ -d "$git_root_raw" ]; then
    git_root="$(cd "$git_root_raw" && pwd -P)"
  fi
  if [ "$git_root" = "$SRC_DIR" ] && git_ref="$(git -C "$SRC_DIR" rev-parse --short HEAD 2>/dev/null)"; then
    if [ -n "$(git -C "$SRC_DIR" status --porcelain --untracked-files=normal -- "${BUILD_REF_PATHS[@]}" 2>/dev/null)" ]; then
      tree_hash="$(source_tree_hash)"
      BUILD_REF="${git_ref}-dirty-${tree_hash:0:12}"
    else
      BUILD_REF="$git_ref"
    fi
    return
  fi

  tree_hash="$(source_tree_hash)"
  BUILD_REF="source-${tree_hash:0:12}"
}

reset_data_dirs() {
  local dir
  dir="$(data_dir)"
  if [ -z "$dir" ] || [ "$dir" = "/" ]; then
    echo "Refusing to reset unsafe DATA_DIR: $dir"
    exit 1
  fi

  rm -rf "$dir/postgres" "$dir/storage" "$dir/uploads"
  prepare_data_dirs
}

cmd="${1:-build}"

case "$cmd" in
  build)
    require_env_file
    export_data_dir
    export_build_ref
    "${COMPOSE[@]}" build
    ;;
  up)
    validate_runtime_secrets
    export_data_dir
    export_build_ref
    # Build before touching services, schema, or storage so a failed build
    # leaves the currently running deployment available for recovery.
    "${COMPOSE[@]}" build
    prepare_data_dirs
    "${COMPOSE[@]}" up -d --wait postgres
    "${COMPOSE[@]}" run --rm --no-deps migrate
    "$SRC_DIR/scripts/storage-layout.sh" apply
    "${COMPOSE[@]}" up -d --no-build --wait
    ;;
  config)
    require_env_file
    export_data_dir
    export_build_ref
    "${COMPOSE[@]}" config
    ;;
  build-ref)
    export_build_ref
    printf '%s\n' "$BUILD_REF"
    ;;
  logs)
    require_env_file
    export_data_dir
    "${COMPOSE[@]}" logs -f
    ;;
  down)
    require_env_file
    export_data_dir
    "${COMPOSE[@]}" down
    ;;
  reset-db)
    validate_runtime_secrets
    export_data_dir
    export_build_ref
    if [ "${CONFIRM_RESET:-}" != "DELETE_EFFCHAT_DATA" ]; then
      echo "reset-db deletes DATA_DIR/postgres, DATA_DIR/storage, and legacy DATA_DIR/uploads."
      echo "Re-run with CONFIRM_RESET=DELETE_EFFCHAT_DATA scripts/docker-build.sh reset-db"
      exit 1
    fi
    # Do not destroy the requested reset target until replacement images are
    # available and the final switch can run without another build.
    "${COMPOSE[@]}" build
    "${COMPOSE[@]}" down
    reset_data_dirs
    "${COMPOSE[@]}" up -d --no-build --wait
    ;;
  -h|--help|help)
    usage
    ;;
  *)
    usage
    exit 1
    ;;
esac
