#!/usr/bin/env bash
# Quick SSH reachability check before vault deploy/smoke (fail fast when ZT is down).
#
# Usage:
#   ./scripts/preflight-vault-ssh.sh
#   MVP_DEPLOY_SSH=ender@192.168.40.153 ./scripts/preflight-vault-ssh.sh
set -euo pipefail

DEFAULT_VAULT='ender@fd2c:a2fd:5d9e:ab72:9d99:930d:f160:3e95'
SSH_TARGET="${MVP_DEPLOY_SSH:-$DEFAULT_VAULT}"

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  echo "usage: $0" >&2
  echo "  env: MVP_DEPLOY_SSH (default $DEFAULT_VAULT)" >&2
  exit 0
fi

if ssh -6 -o BatchMode=yes -o ConnectTimeout=10 "$SSH_TARGET" 'echo ok' >/dev/null 2>&1; then
  echo "preflight-vault-ssh: OK $SSH_TARGET"
  exit 0
fi

echo "preflight-vault-ssh: cannot reach $SSH_TARGET (ZeroTier up? ssh key? try MVP_DEPLOY_SSH=ender@192.168.40.153)" >&2
exit 1
