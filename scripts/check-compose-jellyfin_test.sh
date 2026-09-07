#!/usr/bin/env bash
# CI: optional jellyfin bridge is profile-gated in compose files.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
fail() { echo "FAIL: $*" >&2; exit 1; }

check_dev() {
  local file="$1"
  [[ -f "$file" ]] || fail "missing $file"
  grep -qE '^  jellyfin:' "$file" || fail "$file missing service jellyfin"
  awk '/^  jellyfin:/{p=1} p && /profiles:/{print; exit}' "$file" | grep -q '"jellyfin"' \
    || fail "$file jellyfin must use compose profile jellyfin"
  grep -A35 '^  jellyfin:' "$file" | grep -q ':9475' \
    || fail "$file jellyfin must publish container gRPC port 9475"
  grep -A35 '^  jellyfin:' "$file" | grep -q ':8475' \
    || fail "$file jellyfin must publish container HTTP port 8475"
  grep -A35 '^  jellyfin:' "$file" | grep -q 'MUXCORE_MODULE_ID: jellyfin' \
    || fail "$file jellyfin must set MUXCORE_MODULE_ID"
  grep -A35 '^  jellyfin:' "$file" | grep -q 'JELLYFIN_BASE_URL:' \
    || fail "$file jellyfin must set JELLYFIN_BASE_URL"
  grep -A35 '^  jellyfin:' "$file" | grep -q 'JELLYFIN_API_KEY:' \
    || fail "$file jellyfin must set JELLYFIN_API_KEY"
  grep -A35 '^  jellyfin:' "$file" | grep -q 'JELLYFIN_WEBHOOK_SECRET:' \
    || fail "$file jellyfin must set JELLYFIN_WEBHOOK_SECRET"
}

check_registry() {
  local file="$1"
  [[ -f "$file" ]] || fail "missing $file"
  grep -qE '^  jellyfin-bridge:' "$file" || fail "$file missing service jellyfin-bridge"
  awk '/^  jellyfin-bridge:/{p=1} p && /profiles:/{print; exit}' "$file" | grep -q '"jellyfin"' \
    || fail "$file jellyfin-bridge must use compose profile jellyfin"
  grep -A30 '^  jellyfin-bridge:' "$file" | grep -q 'MUXCORE_MODULE_ID: jellyfin' \
    || fail "$file jellyfin-bridge must set MUXCORE_MODULE_ID"
  grep -A30 '^  jellyfin-bridge:' "$file" | grep -q 'JELLYFIN_BASE_URL:' \
    || fail "$file jellyfin-bridge must set JELLYFIN_BASE_URL"
  grep -A30 '^  jellyfin-bridge:' "$file" | grep -q 'JELLYFIN_API_KEY:' \
    || fail "$file jellyfin-bridge must set JELLYFIN_API_KEY"
  grep -A30 '^  jellyfin-bridge:' "$file" | grep -q 'JELLYFIN_WEBHOOK_SECRET:' \
    || fail "$file jellyfin-bridge must set JELLYFIN_WEBHOOK_SECRET"
}

check_dev "$ROOT/docker-compose.yml"
check_registry "$ROOT/docker-compose.registry.yml"

echo "OK: compose jellyfin profile wired"
