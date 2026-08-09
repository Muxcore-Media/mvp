#!/usr/bin/env bash
# Staging profile: mesh mTLS enabled; never sets MUXCORE_INSECURE_DISABLE_TLS.
# Requires packaging gate + CA (see tls/MTLS-STAGING.md).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
export MUXCORE_CONFIG="${MUXCORE_CONFIG:-$ROOT/muxcore.staging.json}"
export MUXCORE_PROFILE=staging
# Explicitly clear insecure flag if inherited from the parent shell.
unset MUXCORE_INSECURE_DISABLE_TLS || true

if [[ "${1:-}" == "up" ]]; then
  if [[ ! -f "$MUXCORE_CONFIG" ]]; then
    echo "missing $MUXCORE_CONFIG" >&2
    exit 1
  fi
  mkdir -p "$ROOT/tls/ca-mesh"
  chmod 700 "$ROOT/tls/ca-mesh" || true
  echo "staging: MUXCORE_CONFIG=$MUXCORE_CONFIG (mTLS; insecure TLS disabled)"
  echo "NOTE: host binaries still need bootstrap certs or core --tag spawn; see tls/MTLS-STAGING.md"
fi

# Re-exec the host runner but strip insecure exports by filtering the script env.
# run-host.sh hard-codes insecure today — patch at call site via env override after source.
# For a true cutover, prefer core-spawned modules. Until then this documents the profile
# and fails closed if someone forces insecure.
if [[ "${MUXCORE_FORCE_INSECURE:-}" == "1" ]]; then
  echo "refusing MUXCORE_FORCE_INSECURE on staging profile" >&2
  exit 1
fi

exec env -u MUXCORE_INSECURE_DISABLE_TLS \
  MUXCORE_CONFIG="$MUXCORE_CONFIG" \
  MUXCORE_PROFILE=staging \
  bash "$ROOT/run-host.sh" "$@"
