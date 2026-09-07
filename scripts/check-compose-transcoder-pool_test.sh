#!/usr/bin/env bash
# CI: optional media-transcoder-pool service is profile-gated in compose files.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
fail() { echo "FAIL: $*" >&2; exit 1; }

check_file() {
  local file="$1"
  [[ -f "$file" ]] || fail "missing $file"
  grep -qE '^  media-transcoder-pool:' "$file" || fail "$file missing service media-transcoder-pool"
  awk '/^  media-transcoder-pool:/{p=1} p && /profiles:/{print; exit}' "$file" | grep -q '"transcoder-pool"' \
    || fail "$file media-transcoder-pool must use compose profile transcoder-pool"
  grep -A30 '^  media-transcoder-pool:' "$file" | grep -q ':9720' \
    || fail "$file media-transcoder-pool must publish container gRPC port 9720"
  grep -A30 '^  media-transcoder-pool:' "$file" | grep -q ':9721' \
    || fail "$file media-transcoder-pool must publish container health port 9721"
  grep -A30 '^  media-transcoder-pool:' "$file" | grep -q 'MUXCORE_MODULE_ID: media-transcoder-pool' \
    || fail "$file media-transcoder-pool must set MUXCORE_MODULE_ID"
  grep -A30 '^  media-transcoder-pool:' "$file" | grep -q 'POOL_DB_PATH: /data/transcoder-pool/pool.db' \
    || fail "$file media-transcoder-pool must set POOL_DB_PATH"
}

check_file "$ROOT/docker-compose.yml"
check_file "$ROOT/docker-compose.registry.yml"

echo "OK: compose media-transcoder-pool profile wired"
