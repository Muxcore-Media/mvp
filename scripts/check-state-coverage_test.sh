#!/usr/bin/env bash
# Offline tests for check-state-coverage.sh (ADR-0013): fixtures under
# testdata/state-coverage plus the real manifest/registry compose.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECK="$SCRIPT_DIR/check-state-coverage.sh"
FX="$SCRIPT_DIR/testdata/state-coverage"
failed=0

fail() { echo "FAIL: $*" >&2; failed=1; }

# expect <name> <want-rc> <stderr-pattern|-> <manifest> <compose>
expect() {
  local name="$1" want="$2" pattern="$3" manifest="$4" compose="$5" out rc=0
  out="$(bash "$CHECK" "$manifest" "$compose" 2>&1)" || rc=$?
  if [[ "$rc" != "$want" ]]; then
    fail "$name: rc=$rc want $want"$'\n'"$out"
    return
  fi
  if [[ "$pattern" != "-" ]] && ! grep -qE "$pattern" <<<"$out"; then
    fail "$name: output does not match /$pattern/"$'\n'"$out"
    return
  fi
  echo "ok: $name"
}

expect "good fixture" 0 "^OK: state coverage — 5 state entries, 1 stateless, 0 warning" \
  "$FX/good-manifest.yaml" "$FX/good-compose.yml"
expect "required module uncovered" 1 "ERROR: notification-default: required/recommended module is in neither" \
  "$FX/bad-uncovered-manifest.yaml" "$FX/good-compose.yml"
expect "env drift" 1 "ERROR: auth-local: auth-local AUTH_DB_PATH=/app/auth.db" \
  "$FX/good-manifest.yaml" "$FX/bad-env-drift-compose.yml"
expect "backup mount missing" 1 "ERROR: media-rename: backup-local must mount media-rename-data:/source/media-rename:ro" \
  "$FX/good-manifest.yaml" "$FX/bad-backup-mount-compose.yml"
expect "default source list missing entry" 1 "ERROR: auth-local: /source/auth-local missing from backup-local default BACKUP_SOURCE_DIRS" \
  "$FX/good-manifest.yaml" "$FX/bad-backup-sources-compose.yml"
expect "key material archived" 1 "ERROR: backup-local archives excluded volume encryption-aesgcm-data" \
  "$FX/good-manifest.yaml" "$FX/bad-key-archived-compose.yml"
expect "unaccounted volume" 1 "ERROR: service auth-local mounts volume auth-extra" \
  "$FX/good-manifest.yaml" "$FX/bad-unaccounted-volume-compose.yml"
expect "optional drift only warns" 0 "WARN: playback-guard: playback-guard PLAYBACK_GUARD_DB_PATH=/tmp/guard.db" \
  "$FX/good-manifest.yaml" "$FX/warn-optional-drift-compose.yml"
expect "repo manifest + registry compose" 0 "^OK: state coverage" \
  "$SCRIPT_DIR/../household-manifest.yaml" "$SCRIPT_DIR/../docker-compose.registry.yml"

if ((failed)); then
  exit 1
fi
echo "check-state-coverage_test: all passed"
