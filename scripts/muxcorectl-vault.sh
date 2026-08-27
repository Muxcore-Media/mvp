#!/usr/bin/env bash
# Run muxcorectl against the vault MVP mesh (gRPC is local on vault only).
#
# Usage:
#   ./scripts/muxcorectl-vault.sh health status
#   ./scripts/muxcorectl-vault.sh modules list
#   ./scripts/muxcorectl-vault.sh --json settings list
#
# Env: MVP_DEPLOY_SSH, MVP_DEPLOY_MVP (same as deploy-module-to-vault.sh)
set -euo pipefail

DEFAULT_VAULT='ender@fd2c:a2fd:5d9e:ab72:9d99:930d:f160:3e95'
SSH_TARGET="${MVP_DEPLOY_SSH:-$DEFAULT_VAULT}"
REMOTE_MVP="${MVP_DEPLOY_MVP:-/mnt/fast-storage/appdata/muxcore/mvp}"

if [[ $# -eq 0 || "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  echo "usage: $0 <muxcorectl args…>" >&2
  echo "example: $0 health status" >&2
  echo "       $0 modules list" >&2
  echo "env: MVP_DEPLOY_SSH, MVP_DEPLOY_MVP (same as deploy-module-to-vault.sh)" >&2
  exit 2
fi

ssh -6 -o BatchMode=yes -o ConnectTimeout=30 "$SSH_TARGET" \
  env REMOTE_MVP="$REMOTE_MVP" bash -s -- "$@" <<'REMOTE'
set -euo pipefail
cd "$REMOTE_MVP"
export PATH="$REMOTE_MVP/bin:$PATH"
export MUXCORE_INSECURE_DISABLE_TLS=true
export MUXCORE_MESH_DIAL_LOCAL=true
export MUXCORE_GRPC_ADDR=127.0.0.1:9090
export MUXCORE_TOKEN="$(cat run/admin.token)"
exec muxcorectl "$@"
REMOTE
