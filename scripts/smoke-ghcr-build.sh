#!/usr/bin/env bash
# Verify GHCR publish path builds locally without pushing (no gh write:packages required).
#
# Usage:
#   ./scripts/smoke-ghcr-build.sh
#   ./scripts/smoke-ghcr-build.sh v0.6.15
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TAG="${1:-v0.6.15}"
BUILD_ONLY=1 "$ROOT/scripts/publish-muxcored-ghcr.sh" "$TAG"
