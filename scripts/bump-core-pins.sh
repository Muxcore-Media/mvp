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

# Sync household OCI tag defaults (compose + publish fallbacks) with the core pin.
MANIFEST="$ROOT/household-manifest.yaml"
if [[ -f "$MANIFEST" && "$DRY" != "1" ]]; then
  if grep -q "^core_tag: ${VER}$" "$MANIFEST" 2>/dev/null || grep -q "^core_tag: \"${VER}\"$" "$MANIFEST" 2>/dev/null; then
    :
  else
    sed -i -E "s|^core_tag:.*|core_tag: ${VER}|" "$MANIFEST"
    echo "updated $MANIFEST core_tag -> ${VER}"
  fi
fi

sync_tag_files=(
  "$ROOT/docker-compose.registry.yml"
  "$ROOT/docker-compose.ghcr.yml"
  "$ROOT/README.md"
  "$ROOT/docs/PUBLIC-INSTALL.md"
  "$ROOT/scripts/publish-module-images.sh"
  "$ROOT/scripts/publish-muxcored-local.sh"
  "$ROOT/scripts/publish-muxcored-ghcr.sh"
)
for f in "${sync_tag_files[@]}"; do
  [[ -f "$f" ]] || continue
  if [[ "$DRY" == "1" ]]; then
    if grep -q 'v0\.[0-9.]\+' "$f"; then
      echo "would sync MUXCORE_IMAGE_TAG examples in $f -> ${VER}"
    fi
    continue
  fi
  if grep -q 'MUXCORE_IMAGE_TAG:-v' "$f" 2>/dev/null; then
    sed -i -E "s/MUXCORE_IMAGE_TAG:-v[0-9.]+/MUXCORE_IMAGE_TAG:-${VER}/g" "$f"
  fi
  sed -i -E "s/export MUXCORE_IMAGE_TAG=v[0-9.]+/export MUXCORE_IMAGE_TAG=${VER}/g" "$f"
  sed -i -E "s/publish-muxcored-local\.sh v[0-9.]+/publish-muxcored-local.sh ${VER}/g" "$f"
  sed -i -E "s/publish-module-images\.sh v[0-9.]+/publish-module-images.sh ${VER}/g" "$f"
  sed -i -E "s/publish-muxcored-ghcr\.sh v[0-9.]+/publish-muxcored-ghcr.sh ${VER}/g" "$f"
  sed -i -E "s/smoke-ghcr-build\.sh v[0-9.]+/smoke-ghcr-build.sh ${VER}/g" "$f"
  echo "synced image tag examples in $f -> ${VER}"
done
