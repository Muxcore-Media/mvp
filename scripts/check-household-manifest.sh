#!/usr/bin/env bash
# Validate household-manifest.yaml against registry compose and publish script lists.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
MANIFEST="$ROOT/household-manifest.yaml"
REGISTRY="$ROOT/docker-compose.registry.yml"
PUBLISH="$ROOT/scripts/publish-module-images.sh"

die() { echo "household-manifest: $*" >&2; exit 1; }
[[ -f "$MANIFEST" ]] || die "missing $MANIFEST"

extract_yaml_list() {
  awk '/^'"$1"':/{p=1;next} /^[a-z_]+:/{if(p)exit} p && /^  - /{gsub(/^  - /,""); gsub(/#.*/,""); gsub(/ +$/,""); if($0!="") print $0}' "$MANIFEST"
}

mapfile -t required < <(extract_yaml_list required)
mapfile -t recommended < <(extract_yaml_list recommended)

missing_registry=()
for name in "${required[@]}" "${recommended[@]}"; do
  [[ "$name" == "core" ]] && continue
  svc="$name"
  [[ "$name" == "media-ui" ]] && svc="media-ui"
  if ! grep -q "${name}:" "$REGISTRY" 2>/dev/null; then
    missing_registry+=("$name")
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
  echo "WARN: not in docker-compose.registry.yml: ${missing_registry[*]}"
fi
if ((${#missing_publish[@]})); then
  echo "WARN: not in publish-module-images.sh DEFAULT_MODULES: ${missing_publish[*]}"
fi

echo "OK: household manifest lists ${#required[@]} required + ${#recommended[@]} recommended modules"
