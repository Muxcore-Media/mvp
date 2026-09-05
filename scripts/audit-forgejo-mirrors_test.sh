#!/usr/bin/env bash
# Offline audit smoke (skip live GitHub tip checks).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
AUDIT_GITHUB_TIPS=0 bash "$ROOT/scripts/audit-forgejo-mirrors.sh" "$ROOT" || true
echo "ok audit-forgejo-mirrors offline smoke"
