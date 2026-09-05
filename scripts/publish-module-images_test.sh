#!/usr/bin/env bash
# Offline checks for media-ui publish path (umbrella #29).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
fail() { echo "FAIL: $*" >&2; exit 1; }

grep -q 'ARG MVP_DIR=mvp' "$ROOT/dockerfiles/media-ui.Dockerfile" \
  || fail "media-ui.Dockerfile should default MVP_DIR to mvp"
grep -q 'COPY \${MVP_DIR} /ws/mvp' "$ROOT/dockerfiles/media-ui.Dockerfile" \
  || fail "media-ui.Dockerfile should COPY MVP_DIR into /ws/mvp"
grep -q 'COPY _mvp' "$ROOT/dockerfiles/media-ui.Dockerfile" \
  && fail "media-ui.Dockerfile still hard-codes COPY _mvp"

grep -q 'resolve_mvp_dir' "$ROOT/scripts/publish-module-images.sh" \
  || fail "publish-module-images.sh should resolve mvp vs _mvp"
grep -q 'preflight_media_ui_context' "$ROOT/scripts/publish-module-images.sh" \
  || fail "publish-module-images.sh should preflight media-ui siblings"
grep -q 'MVP_DIR=' "$ROOT/scripts/publish-module-images.sh" \
  || fail "publish-module-images.sh should pass MVP_DIR build-arg"

grep -q 'mvp/dockerfiles/media-ui.Dockerfile' "$ROOT/docker-compose.yml" \
  || fail "compose should reference mvp/dockerfiles/media-ui.Dockerfile"
grep -q '_mvp/dockerfiles' "$ROOT/docker-compose.yml" \
  && fail "compose should not reference _mvp/dockerfiles paths"

echo "ok publish-module-images tests"
