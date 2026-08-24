#!/usr/bin/env bash
# Bump github.com/Muxcore-Media/core and sdk/go/* pins across module go.mod files.
#
# Usage:
#   ./scripts/bump-core-pins.sh v0.5.8
#   DRY_RUN=1 ./scripts/bump-core-pins.sh v0.5.8
#
# Updates direct requires for:
#   github.com/Muxcore-Media/core
#   github.com/Muxcore-Media/core/pkg/contracts
#   github.com/Muxcore-Media/core/sdk/go/client
#   github.com/Muxcore-Media/core/sdk/go/module
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WS="$(cd "$ROOT/.." && pwd)"
VER="${1:-}"
DRY="${DRY_RUN:-0}"
[[ -n "$VER" ]] || { echo "usage: $0 <core-version e.g. v0.5.8>" >&2; exit 2; }

pkgs=(
  "github.com/Muxcore-Media/core[[:space:]]"
  "github.com/Muxcore-Media/core/pkg/contracts[[:space:]]"
  "github.com/Muxcore-Media/core/sdk/go/client[[:space:]]"
  "github.com/Muxcore-Media/core/sdk/go/module[[:space:]]"
)

updated=0
while IFS= read -r gomod; do
  dir="$(dirname "$gomod")"
  [[ "$dir" == "$WS/core" ]] && continue
  changed=0
  for pat in "${pkgs[@]}"; do
    if grep -qE "$pat" "$gomod"; then
      if [[ "$DRY" == "1" ]]; then
        echo "would bump $gomod ($pat -> $VER)"
      else
        sed -i -E "s|(${pat})v[0-9.]+|\1${VER}|g" "$gomod"
      fi
      changed=1
    fi
  done
  if [[ "$changed" == "1" ]]; then
    ((updated++)) || true
    [[ "$DRY" == "1" ]] || echo "bumped $gomod"
  fi
done < <(find "$WS" -name go.mod -not -path '*/vendor/*' -not -path '*/.git/*')

echo "OK: ${updated} go.mod files (target ${VER})"
