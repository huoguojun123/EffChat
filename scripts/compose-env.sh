#!/usr/bin/env bash

env_value() {
  local key="$1"
  # Compose owns dotenv quoting, comments, escapes, and precedence. Reading its
  # resolved interpolation environment keeps script decisions on the same
  # contract as the actual deployment without sourcing an untrusted env file.
  "${COMPOSE[@]}" config --environment | awk -v key="$key" '
    index($0, key "=") == 1 {
      value = substr($0, length(key) + 2)
      found = 1
    }
    END {
      if (found) print value
    }
  '
}
