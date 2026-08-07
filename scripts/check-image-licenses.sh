#!/bin/sh
set -eu

if [ "$#" -ne 3 ]; then
  echo "usage: $0 <backend-image> <frontend-image> <python-image>" >&2
  exit 2
fi

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
TMP_DIR=$(mktemp -d)
CONTAINERS=""

cleanup() {
  for container in $CONTAINERS; do
    docker rm -f "$container" >/dev/null 2>&1 || true
  done
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT INT TERM

check_image() {
  component=$1
  image=$2
  destination="$TMP_DIR/$component"
  container=$(docker create "$image")
  CONTAINERS="$CONTAINERS $container"
  mkdir -p "$destination"
  docker cp "$container:/usr/share/licenses/effchat/third-party/$component/." "$destination"
  python3 "$SCRIPT_DIR/licenses/collect-third-party-licenses.py" verify \
    --component "$component" \
    --archive "$destination"
}

check_image backend "$1"
check_image frontend "$2"
check_image python "$3"
