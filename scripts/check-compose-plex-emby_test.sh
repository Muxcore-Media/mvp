#!/usr/bin/env bash
# CI: optional plex/emby playback bridges are profile-gated in compose files.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
fail() { echo "FAIL: $*" >&2; exit 1; }

check_dev_plex() {
  local file="$1"
  grep -qE '^  plex:' "$file" || fail "$file missing service plex"
  awk '/^  plex:/{p=1} p && /profiles:/{print; exit}' "$file" | grep -q '"plex"' \
    || fail "$file plex must use compose profile plex"
  grep -A35 '^  plex:' "$file" | grep -q ':9476' \
    || fail "$file plex must publish container gRPC port 9476"
  grep -A35 '^  plex:' "$file" | grep -q ':8476' \
    || fail "$file plex must publish container HTTP port 8476"
  grep -A35 '^  plex:' "$file" | grep -q 'MUXCORE_MODULE_ID: plex' \
    || fail "$file plex must set MUXCORE_MODULE_ID"
  grep -A35 '^  plex:' "$file" | grep -q 'PLEX_URL:' \
    || fail "$file plex must set PLEX_URL"
  grep -A35 '^  plex:' "$file" | grep -q 'PLEX_TOKEN:' \
    || fail "$file plex must set PLEX_TOKEN"
  grep -A35 '^  plex:' "$file" | grep -q 'PLEX_HTTP_SECRET:' \
    || fail "$file plex must set PLEX_HTTP_SECRET"
  grep -A35 '^  plex:' "$file" | grep -q 'PLEX_DATA_DIR: /data/plex' \
    || fail "$file plex must set PLEX_DATA_DIR"
  grep -A35 '^  plex:' "$file" | grep -q 'plex-data:/data' \
    || fail "$file plex must mount plex-data volume"
}

check_dev_emby() {
  local file="$1"
  grep -qE '^  emby:' "$file" || fail "$file missing service emby"
  awk '/^  emby:/{p=1} p && /profiles:/{print; exit}' "$file" | grep -q '"emby"' \
    || fail "$file emby must use compose profile emby"
  grep -A35 '^  emby:' "$file" | grep -q ':9477' \
    || fail "$file emby must publish container gRPC port 9477"
  grep -A35 '^  emby:' "$file" | grep -q ':8477' \
    || fail "$file emby must publish container HTTP port 8477"
  grep -A35 '^  emby:' "$file" | grep -q 'MUXCORE_MODULE_ID: emby' \
    || fail "$file emby must set MUXCORE_MODULE_ID"
  grep -A35 '^  emby:' "$file" | grep -q 'EMBY_URL:' \
    || fail "$file emby must set EMBY_URL"
  grep -A35 '^  emby:' "$file" | grep -q 'EMBY_TOKEN:' \
    || fail "$file emby must set EMBY_TOKEN"
  grep -A35 '^  emby:' "$file" | grep -q 'EMBY_SSE_SECRET:' \
    || fail "$file emby must set EMBY_SSE_SECRET"
  grep -A35 '^  emby:' "$file" | grep -q 'EMBY_DATA_DIR: /data/emby' \
    || fail "$file emby must set EMBY_DATA_DIR"
  grep -A35 '^  emby:' "$file" | grep -q 'emby-data:/data' \
    || fail "$file emby must mount emby-data volume"
}

check_registry_plex() {
  local file="$1"
  grep -qE '^  plex-bridge:' "$file" || fail "$file missing service plex-bridge"
  awk '/^  plex-bridge:/{p=1} p && /profiles:/{print; exit}' "$file" | grep -q '"plex"' \
    || fail "$file plex-bridge must use compose profile plex"
  grep -A30 '^  plex-bridge:' "$file" | grep -q 'MUXCORE_MODULE_ID: plex' \
    || fail "$file plex-bridge must set MUXCORE_MODULE_ID"
  grep -A30 '^  plex-bridge:' "$file" | grep -q 'PLEX_URL:' \
    || fail "$file plex-bridge must set PLEX_URL"
  grep -A30 '^  plex-bridge:' "$file" | grep -q 'PLEX_TOKEN:' \
    || fail "$file plex-bridge must set PLEX_TOKEN"
  grep -A30 '^  plex-bridge:' "$file" | grep -q 'PLEX_HTTP_SECRET:' \
    || fail "$file plex-bridge must set PLEX_HTTP_SECRET"
  grep -A35 '^  plex-bridge:' "$file" | grep -q 'PLEX_DATA_DIR: /data/plex' \
    || fail "$file plex-bridge must set PLEX_DATA_DIR"
  grep -A35 '^  plex-bridge:' "$file" | grep -q 'plex-data:/data' \
    || fail "$file plex-bridge must mount plex-data volume"
  grep -qE '^  plex-data:' "$file" \
    || fail "$file must declare plex-data named volume"
}

check_registry_emby() {
  local file="$1"
  grep -qE '^  emby-bridge:' "$file" || fail "$file missing service emby-bridge"
  awk '/^  emby-bridge:/{p=1} p && /profiles:/{print; exit}' "$file" | grep -q '"emby"' \
    || fail "$file emby-bridge must use compose profile emby"
  grep -A30 '^  emby-bridge:' "$file" | grep -q 'MUXCORE_MODULE_ID: emby' \
    || fail "$file emby-bridge must set MUXCORE_MODULE_ID"
  grep -A30 '^  emby-bridge:' "$file" | grep -q 'EMBY_URL:' \
    || fail "$file emby-bridge must set EMBY_URL"
  grep -A30 '^  emby-bridge:' "$file" | grep -q 'EMBY_TOKEN:' \
    || fail "$file emby-bridge must set EMBY_TOKEN"
  grep -A30 '^  emby-bridge:' "$file" | grep -q 'EMBY_SSE_SECRET:' \
    || fail "$file emby-bridge must set EMBY_SSE_SECRET"
  grep -A35 '^  emby-bridge:' "$file" | grep -q 'EMBY_DATA_DIR: /data/emby' \
    || fail "$file emby-bridge must set EMBY_DATA_DIR"
  grep -A35 '^  emby-bridge:' "$file" | grep -q 'emby-data:/data' \
    || fail "$file emby-bridge must mount emby-data volume"
  grep -qE '^  emby-data:' "$file" \
    || fail "$file must declare emby-data named volume"
}

check_profile_gating() {
  local file="$1"
  local label="$2"
  command -v docker >/dev/null 2>&1 || return 0
  docker compose version >/dev/null 2>&1 || return 0

  local default_svcs profile_plex_svcs profile_emby_svcs
  default_svcs="$(cd "$ROOT" && docker compose -f "$file" config --services 2>/dev/null)" || return 0
  profile_plex_svcs="$(cd "$ROOT" && docker compose -f "$file" --profile plex config --services 2>/dev/null)" || return 0
  profile_emby_svcs="$(cd "$ROOT" && docker compose -f "$file" --profile emby config --services 2>/dev/null)" || return 0

  if grep -qx 'plex' <<<"$default_svcs" || grep -qx 'plex-bridge' <<<"$default_svcs"; then
    fail "$label default compose must not include plex bridge"
  fi
  if grep -qx 'emby' <<<"$default_svcs" || grep -qx 'emby-bridge' <<<"$default_svcs"; then
    fail "$label default compose must not include emby bridge"
  fi
  if ! grep -qx 'plex' <<<"$profile_plex_svcs" && ! grep -qx 'plex-bridge' <<<"$profile_plex_svcs"; then
    fail "$label --profile plex must include plex bridge"
  fi
  if ! grep -qx 'emby' <<<"$profile_emby_svcs" && ! grep -qx 'emby-bridge' <<<"$profile_emby_svcs"; then
    fail "$label --profile emby must include emby bridge"
  fi
}

check_dev_plex "$ROOT/docker-compose.yml"
check_dev_emby "$ROOT/docker-compose.yml"
check_registry_plex "$ROOT/docker-compose.registry.yml"
check_registry_emby "$ROOT/docker-compose.registry.yml"
check_profile_gating "$ROOT/docker-compose.yml" "docker-compose.yml"
check_profile_gating "$ROOT/docker-compose.registry.yml" "docker-compose.registry.yml"

echo "OK: compose plex/emby profiles wired"
