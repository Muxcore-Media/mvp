#!/usr/bin/env bash
# TDD §5 / T-M2-02: every image referenced by any service (including optional
# compose profiles) in docker-compose.registry.yml must be in the publish set,
# i.e. DEFAULT_MODULES in publish-module-images.sh.
#
# Image name -> module repo is identity, except `muxcored` (module `core`),
# which is published by publish-muxcored-local.sh rather than the module script.
#
# Usage: check-publish-set.sh [compose.yml] [publish-script.sh]
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
COMPOSE="${1:-$ROOT/docker-compose.registry.yml}"
PUBLISH="${2:-$ROOT/scripts/publish-module-images.sh}"
SPECIAL_OK="${PUBLISH_SET_SPECIAL:-muxcored}"

die() { echo "publish-set: $*" >&2; exit 1; }
[[ -f "$COMPOSE" ]] || die "missing $COMPOSE"
[[ -f "$PUBLISH" ]] || die "missing $PUBLISH"

# Images of services only (4-space `image:` under a 2-space service key).
mapfile -t images < <(awk '
  /^services:/ {s=1; next}
  /^[^ #]/ {s=0}
  s && /^    image:/ {
    v=$0; sub(/^    image:[ \t]*/, "", v)
    sub(/:\$\{MUXCORE_IMAGE_TAG[^}]*\}.*$/, "", v)
    sub(/^.*\}\//, "", v)
    print v
  }' "$COMPOSE" | sort -u)
((${#images[@]})) || die "no service images found in $COMPOSE"

mapfile -t published < <(awk '
  /^DEFAULT_MODULES=\(/ {p=1; next}
  p && /^\)/ {exit}
  p {gsub(/#.*/, ""); for (i=1;i<=NF;i++) print $i}' "$PUBLISH" | sort -u)
((${#published[@]})) || die "DEFAULT_MODULES not found in $PUBLISH"

missing=()
for img in "${images[@]}"; do
  case " $SPECIAL_OK " in *" $img "*) continue ;; esac
  printf '%s\n' "${published[@]}" | grep -qxF "$img" || missing+=("$img")
done

if ((${#missing[@]})); then
  die "compose images missing from publish set (DEFAULT_MODULES): ${missing[*]}"
fi
echo "ok publish-set: ${#images[@]} compose images covered by publish set"
