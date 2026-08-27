#!/usr/bin/env bash
# Full vault smoke: local HTTP over SSH, public edge TLS, muxcorectl mesh health.
#
# Usage:
#   ./scripts/smoke-vault-all.sh
#   MVP_DEPLOY_SSH=ender@192.168.40.153 ./scripts/smoke-vault-all.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  echo "usage: $0" >&2
  echo "  runs: smoke-vault-health.sh, smoke-vault-public.sh, muxcorectl-vault.sh health status" >&2
  echo "  env: MVP_DEPLOY_SSH, MVP_DEPLOY_MVP (same as deploy-module-to-vault.sh)" >&2
  exit 0
fi

fail=0
run_step() {
  local label="$1"
  shift
  echo ""
  echo "=== $label ==="
  if "$@"; then
    return 0
  fi
  fail=1
  return 0
}

echo "smoke-vault-all"
run_step "vault HTTP (SSH)" "$SCRIPT_DIR/smoke-vault-health.sh"
run_step "public edge (TLS)" "$SCRIPT_DIR/smoke-vault-public.sh"
run_step "mesh (muxcorectl)" "$SCRIPT_DIR/muxcorectl-vault.sh" health status

if [[ "$fail" -ne 0 ]]; then
  echo ""
  echo "smoke-vault-all: FAIL (see above)" >&2
  exit 1
fi

echo ""
echo "smoke-vault-all: OK"
exit 0
