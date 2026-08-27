#!/usr/bin/env bash
# HTTP health smoke for the vault MVP stack (run from desk/thin over SSH).
#
# Usage:
#   ./scripts/smoke-vault-health.sh
#   MVP_DEPLOY_SSH=ender@192.168.40.153 ./scripts/smoke-vault-health.sh
set -euo pipefail

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  echo "usage: $0" >&2
  echo "  env: MVP_DEPLOY_SSH (default vault ZT IPv6)" >&2
  exit 0
fi

DEFAULT_VAULT='ender@fd2c:a2fd:5d9e:ab72:9d99:930d:f160:3e95'
SSH_TARGET="${MVP_DEPLOY_SSH:-$DEFAULT_VAULT}"

fail=0
check() {
  local label="$1" url="$2" expect="${3:-200}"
  local code
  code=$(ssh -6 -o BatchMode=yes -o ConnectTimeout=30 "$SSH_TARGET" \
    "curl -s -o /dev/null -w '%{http_code}' --connect-timeout 3 '$url' 2>/dev/null || echo 000")
  if [[ "$code" == "$expect" ]] || [[ "$expect" == *"$code"* ]]; then
    printf 'ok   %s %s → %s\n' "$label" "$url" "$code"
  else
    printf 'FAIL %s %s → %s (want %s)\n' "$label" "$url" "$code" "$expect"
    fail=1
  fi
}

echo "smoke-vault-health: $SSH_TARGET"
check admin http://127.0.0.1:8082/health 200
check media http://127.0.0.1:5173/ '200 302 303'
check auth http://127.0.0.1:9401/login '200 302'
check core http://127.0.0.1:8080/health '200 503'
check api-rest http://127.0.0.1:18080/health '200 401 503'

exit "$fail"
