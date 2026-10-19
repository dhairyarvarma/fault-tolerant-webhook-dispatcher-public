#!/usr/bin/env bash
set -u

check() {
  local cmd="$1"
  if command -v "$cmd" >/dev/null 2>&1; then
    printf '[OK]   %s -> %s\n' "$cmd" "$(command -v "$cmd")"
  else
    printf '[MISS] %s\n' "$cmd"
  fi
}

check git
check go
check node
check npm
check docker
check gh
check k6
check claude
check codex

printf '\nDocker Compose:\n'
if docker compose version >/dev/null 2>&1; then
  docker compose version
else
  printf '[MISS] docker compose\n'
fi
