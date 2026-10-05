#!/usr/bin/env bash
# Offline tests for check-publish-set.sh using fixture compose + publish lists.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CHECK="$ROOT/scripts/check-publish-set.sh"
fail() { echo "FAIL: $*" >&2; exit 1; }
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT

cat >"$tmp/compose.yml" <<'Y'
x-image: &image
  image: ${MUXCORE_REGISTRY:-r/muxcore}/${MUXCORE_SERVICE:-muxcored}:${MUXCORE_IMAGE_TAG:-v1}
services:
  core:
    <<: *image
    image: ${MUXCORE_REGISTRY:-r/muxcore}/muxcored:${MUXCORE_IMAGE_TAG:-v1}
  api-rest:
    image: ${MUXCORE_REGISTRY:-r/muxcore}/api-rest:${MUXCORE_IMAGE_TAG:-v1}
  jellyfin-bridge:
    image: ${MUXCORE_REGISTRY:-r/muxcore}/jellyfin:${MUXCORE_IMAGE_TAG:-v1}
    profiles: ["jellyfin"]
networks:
  muxcore: {}
Y
cat >"$tmp/pub-ok.sh" <<'Y'
DEFAULT_MODULES=(
  api-rest
  jellyfin
)
Y
cat >"$tmp/pub-bad.sh" <<'Y'
DEFAULT_MODULES=(
  api-rest
)
Y

bash "$CHECK" "$tmp/compose.yml" "$tmp/pub-ok.sh" >/dev/null || fail "complete set should pass"
if out="$(bash "$CHECK" "$tmp/compose.yml" "$tmp/pub-bad.sh" 2>&1)"; then
  fail "missing optional-profile image should fail"
fi
grep -q 'jellyfin' <<<"$out" || fail "failure should name jellyfin: $out"
grep -q 'muxcored' <<<"$out" && fail "muxcored is special-cased, must not be reported"

bash "$CHECK" >/dev/null || fail "real compose/publish set must be consistent"
echo "ok check-publish-set tests"
