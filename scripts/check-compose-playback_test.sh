#!/usr/bin/env bash
# CI: optional playback-guard / playback-monitor services are profile-gated in compose files.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
fail() { echo "FAIL: $*" >&2; exit 1; }

check_guard() {
  local file="$1"
  [[ -f "$file" ]] || fail "missing $file"
  grep -qE '^  playback-guard:' "$file" || fail "$file missing service playback-guard"
  awk '/^  playback-guard:/{p=1} p && /profiles:/{print; exit}' "$file" | grep -q '"playback-guard"' \
    || fail "$file playback-guard must use compose profile playback-guard"
  grep -A30 '^  playback-guard:' "$file" | grep -q ':9561' \
    || fail "$file playback-guard must publish container gRPC port 9561"
  grep -A30 '^  playback-guard:' "$file" | grep -q 'MUXCORE_MODULE_ID: playback-guard' \
    || fail "$file playback-guard must set MUXCORE_MODULE_ID"
  grep -A30 '^  playback-guard:' "$file" | grep -q 'PLAYBACK_GUARD_DB_PATH: /data/playback-guard/guard.db' \
    || fail "$file playback-guard must set PLAYBACK_GUARD_DB_PATH"
}

check_monitor() {
  local file="$1"
  [[ -f "$file" ]] || fail "missing $file"
  grep -qE '^  playback-monitor:' "$file" || fail "$file missing service playback-monitor"
  awk '/^  playback-monitor:/{p=1} p && /profiles:/{print; exit}' "$file" | grep -q '"playback-monitor"' \
    || fail "$file playback-monitor must use compose profile playback-monitor"
  grep -A35 '^  playback-monitor:' "$file" | grep -q ':9560' \
    || fail "$file playback-monitor must publish container gRPC port 9560"
  grep -A35 '^  playback-monitor:' "$file" | grep -q ':8560' \
    || fail "$file playback-monitor must publish container HTTP port 8560"
  grep -A35 '^  playback-monitor:' "$file" | grep -q 'MUXCORE_MODULE_ID: playback-monitor' \
    || fail "$file playback-monitor must set MUXCORE_MODULE_ID"
  grep -A35 '^  playback-monitor:' "$file" | grep -q 'PLAYBACK_MONITOR_DB_PATH: /data/playback-monitor/monitor.db' \
    || fail "$file playback-monitor must set PLAYBACK_MONITOR_DB_PATH"
}

check_guard "$ROOT/docker-compose.yml"
check_monitor "$ROOT/docker-compose.yml"
check_guard "$ROOT/docker-compose.registry.yml"
check_monitor "$ROOT/docker-compose.registry.yml"

echo "OK: compose playback-guard / playback-monitor profiles wired"
