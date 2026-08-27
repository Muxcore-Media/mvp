#!/usr/bin/env bash
# Public edge smoke for MuxCore vault origins (TLS via dawn/dusk).
#
# Runs curl from this machine — not over SSH to vault.
#
# Usage:
#   ./scripts/smoke-vault-public.sh
#   MUXCORE_PUBLIC_MUX=https://mux.zem.systems ./scripts/smoke-vault-public.sh
set -euo pipefail

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  echo "usage: $0" >&2
  echo "  env: MUXCORE_PUBLIC_ADMIN, MUXCORE_PUBLIC_MUX, MUXCORE_PUBLIC_AUTH" >&2
  exit 0
fi

PUBLIC_ADMIN="${MUXCORE_PUBLIC_ADMIN:-https://admin.zem.systems/health}"
PUBLIC_MUX="${MUXCORE_PUBLIC_MUX:-https://mux.zem.systems/}"
PUBLIC_AUTH="${MUXCORE_PUBLIC_AUTH:-https://auth.zem.systems/login}"

fail=0
check() {
  local label="$1" url="$2" expect="${3:-200}"
  local code
  code=$(curl -sS -o /dev/null -w '%{http_code}' --connect-timeout 8 --max-time 20 "$url" 2>/dev/null || echo 000)
  if [[ "$code" == "$expect" ]] || [[ "$expect" == *"$code"* ]]; then
    printf 'ok   %s %s → %s\n' "$label" "$url" "$code"
  else
    printf 'FAIL %s %s → %s (want %s)\n' "$label" "$url" "$code" "$expect"
    fail=1
  fi
}

echo "smoke-vault-public (edge TLS)"
check admin "$PUBLIC_ADMIN" 200
check mux "$PUBLIC_MUX" '200 302 303'
check auth "$PUBLIC_AUTH" '200 302'

exit "$fail"
