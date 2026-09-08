#!/usr/bin/env bash
# CI: optional secrets-vault service is profile-gated in compose files.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
fail() { echo "FAIL: $*" >&2; exit 1; }

check_file() {
  local file="$1"
  [[ -f "$file" ]] || fail "missing $file"
  grep -qE '^  secrets-vault:' "$file" || fail "$file missing service secrets-vault"
  awk '/^  secrets-vault:/{p=1} p && /profiles:/{print; exit}' "$file" | grep -q '"secrets-vault"' \
    || fail "$file secrets-vault must use compose profile secrets-vault"
  grep -A40 '^  secrets-vault:' "$file" | grep -q ':9551' \
    || fail "$file secrets-vault must publish container gRPC port 9551"
  grep -A40 '^  secrets-vault:' "$file" | grep -q 'MUXCORE_MODULE_ID: secrets-vault' \
    || fail "$file secrets-vault must set MUXCORE_MODULE_ID"
  grep -A40 '^  secrets-vault:' "$file" | grep -q 'SECRETS_GRPC_ADDR: ":9551"' \
    || fail "$file secrets-vault must set SECRETS_GRPC_ADDR"
  grep -A40 '^  secrets-vault:' "$file" | grep -q 'SECRETS_BACKEND:' \
    || fail "$file secrets-vault must wire SECRETS_BACKEND"
}

check_file "$ROOT/docker-compose.yml"
check_file "$ROOT/docker-compose.registry.yml"

if ! grep -q 'name: secrets-vault' "$ROOT/household-manifest.yaml"; then
  fail "household-manifest.yaml must list secrets-vault under optional_env_gated"
fi
if ! grep -q 'MVP_ENABLE_SECRETS_VAULT' "$ROOT/household-manifest.yaml"; then
  fail "household-manifest.yaml must gate secrets-vault on MVP_ENABLE_SECRETS_VAULT"
fi

if command -v docker >/dev/null 2>&1; then
  default_svcs="$(cd "$ROOT" && docker compose -f docker-compose.yml config --services 2>/dev/null)" || default_svcs=""
  if [[ -n "$default_svcs" ]]; then
    if grep -qx 'secrets-vault' <<<"$default_svcs"; then
      fail "docker-compose.yml default Path A must not include secrets-vault"
    fi
    profile_svcs="$(cd "$ROOT" && docker compose -f docker-compose.yml --profile secrets-vault config --services 2>/dev/null)" || profile_svcs=""
    if [[ -n "$profile_svcs" ]] && ! grep -qx 'secrets-vault' <<<"$profile_svcs"; then
      fail "docker-compose.yml --profile secrets-vault must include secrets-vault"
    fi
  fi
fi

echo "OK: compose secrets-vault profile wired"
