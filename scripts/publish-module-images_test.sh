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

# ADR-0014 prebuilt mode: host-built binaries packaged without an in-image Go build.
grep -q 'PREBUILT_DIR' "$ROOT/scripts/publish-module-images.sh" \
  || fail "publish-module-images.sh should support PREBUILT_DIR"
grep -q 'build-module-binaries.sh' "$ROOT/scripts/publish-module-images.sh" \
  || fail "publish-module-images.sh should build missing binaries with build-module-binaries.sh"
for df in module-prebuilt.Dockerfile media-ui-prebuilt.Dockerfile; do
  [[ -f "$ROOT/dockerfiles/$df" ]] || fail "missing dockerfiles/$df"
  grep -q '^FROM golang' "$ROOT/dockerfiles/$df" && fail "$df must not compile (prebuilt binaries only)"
done
grep -q -- '-trimpath -buildvcs=false -ldflags=-buildid=' "$ROOT/scripts/build-module-binaries.sh" \
  || fail "build-module-binaries.sh should use the ADR-0012 build flags"

# ADR-0033: the userdata-local image must contain the packaged userdata-health
# probe the compose healthcheck invokes (both build paths).
grep -q 'go build -o /assets/userdata-health ./cmd/userdata-health' "$ROOT/dockerfiles/module.Dockerfile" \
  || fail "module.Dockerfile must package cmd/userdata-health as /app/userdata-health"
grep -q 'COPY --from=builder /assets/ ./' "$ROOT/dockerfiles/module.Dockerfile" \
  || fail "module.Dockerfile must copy /assets (probe) into /app"
# shellcheck disable=SC2016 # literal $ in the pattern
grep -qF 'build_go "$dir" ./cmd/userdata-health/ "$OUT/$name/userdata-health"' "$ROOT/scripts/build-module-binaries.sh" \
  || fail "build-module-binaries.sh must build the userdata-health probe for prebuilt images"
grep -q '^COPY \. \./' "$ROOT/dockerfiles/module-prebuilt.Dockerfile" \
  || fail "module-prebuilt.Dockerfile must copy the whole prebuilt module dir (probe included)"

echo "ok publish-module-images tests"
