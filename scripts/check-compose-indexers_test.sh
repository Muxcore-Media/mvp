#!/usr/bin/env bash
# CI: optional acquisition indexer services are profile-gated in compose files.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
fail() { echo "FAIL: $*" >&2; exit 1; }

INDEXERS=(
  "indexer-piratebay:indexer-piratebay:9485"
  "indexer-torznab:indexer-torznab:9486"
)

check_file() {
  local file="$1"
  [[ -f "$file" ]] || fail "missing $file"
  for entry in "${INDEXERS[@]}"; do
    IFS=: read -r svc profile port <<<"$entry"
    grep -qE "^  ${svc}:" "$file" || fail "$file missing service ${svc}"
    awk "/^  ${svc}:/{p=1} p && /profiles:/{print; exit}" "$file" | grep -q "\"${profile}\"" \
      || fail "$file ${svc} must use compose profile ${profile}"
    grep -A25 "^  ${svc}:" "$file" | grep -q ":${port}" \
      || fail "$file ${svc} must publish container gRPC port ${port}"
  done
}

check_file "$ROOT/docker-compose.yml"
check_file "$ROOT/docker-compose.registry.yml"

echo "OK: compose indexer profiles wired (${#INDEXERS[@]} services)"
