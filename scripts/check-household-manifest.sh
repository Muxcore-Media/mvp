#!/usr/bin/env bash
# Validate household-manifest.yaml against registry compose and publish script lists.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
MANIFEST="$ROOT/household-manifest.yaml"
REGISTRY="$ROOT/docker-compose.registry.yml"
PUBLISH="$ROOT/scripts/publish-module-images.sh"

die() { echo "household-manifest: $*" >&2; exit 1; }
[[ -f "$MANIFEST" ]] || die "missing $MANIFEST"
[[ -f "$REGISTRY" ]] || die "missing $REGISTRY"
[[ -f "$PUBLISH" ]] || die "missing $PUBLISH"

extract_yaml_list() {
  awk '/^'"$1"':/{p=1;next} /^[a-z_]+:/{if(p)exit} p && /^  - /{gsub(/^  - /,""); gsub(/#.*/,""); gsub(/ +$/,""); if($0!="") print $0}' "$MANIFEST"
}

registry_service_for() {
  local name="$1"
  case "$name" in
    jellyfin) echo "jellyfin-bridge" ;;
    media-ui) echo "media-ui" ;;
    *) echo "$name" ;;
  esac
}

mapfile -t required < <(extract_yaml_list required)
mapfile -t recommended < <(extract_yaml_list recommended)

missing_registry=()
for name in "${required[@]}" "${recommended[@]}"; do
  [[ "$name" == "core" ]] && continue
  svc="$(registry_service_for "$name")"
  if ! grep -qE "^  ${svc}:" "$REGISTRY"; then
    missing_registry+=("$name→${svc}")
  fi
done

missing_publish=()
for name in "${required[@]}" "${recommended[@]}"; do
  [[ "$name" == "core" ]] && continue
  if ! grep -q "$name" "$PUBLISH"; then
    missing_publish+=("$name")
  fi
done

if ((${#missing_registry[@]})); then
  die "not in docker-compose.registry.yml: ${missing_registry[*]}"
fi
if ((${#missing_publish[@]})); then
  die "not in publish-module-images.sh DEFAULT_MODULES: ${missing_publish[*]}"
fi

core_tag="$(awk -F': ' '/^core_tag:/{gsub(/"/,"",$2); gsub(/ +$/,"",$2); print $2; exit}' "$MANIFEST")"
[[ -n "$core_tag" ]] || die "missing core_tag in $MANIFEST"
if ! grep -q "MUXCORE_IMAGE_TAG:-${core_tag}" "$REGISTRY"; then
  die "docker-compose.registry.yml default MUXCORE_IMAGE_TAG must match manifest core_tag (${core_tag})"
fi

echo "OK: household manifest lists ${#required[@]} required + ${#recommended[@]} recommended modules (core_tag=${core_tag})"
