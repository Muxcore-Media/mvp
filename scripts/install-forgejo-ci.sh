#!/usr/bin/env bash
# Install origin CI workflows for vault Forgejo runner (runs-on: native).
# GitHub .github/workflows are legacy mirrors only — see _mvp/tls/GITHUB-RUNNER.md.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
GOLDEN="$ROOT/_wave1/GOLDEN_FORGEJO_CI.yml"
CORE_CI="$ROOT/core/.forgejo/workflows/ci.yml"

# Polluted workspace dumps — never add CI here (MASTER-ROADMAP Appendix H).
SKIP=(
  cache-memory custom-scripts media-jellyfin muxcorectl media-ui
  _wave1 muxcore-docs media-android
)

skip_dir() {
  local name="$1"
  for s in "${SKIP[@]}"; do
    [[ "$name" == "$s" ]] && return 0
  done
  return 1
}

install_go_ci() {
  local dir="$1"
  local name
  name="$(basename "$dir")"
  if skip_dir "$name"; then
    echo "skip dump/non-go: $name"
    return 0
  fi
  if [[ ! -f "$dir/go.mod" ]]; then
    return 0
  fi
  if [[ "$name" == "core" ]]; then
    if [[ -f "$CORE_CI" ]]; then
      echo "keep core custom: $CORE_CI"
    else
      mkdir -p "$dir/.forgejo/workflows"
      cp "$GOLDEN" "$dir/.forgejo/workflows/ci.yml"
      echo "installed core from golden"
    fi
    return 0
  fi
  mkdir -p "$dir/.forgejo/workflows"
  cp "$GOLDEN" "$dir/.forgejo/workflows/ci.yml"
  echo "installed: $name"
}

install_media_ui_ci() {
  local dir="$ROOT/media-ui-app"
  [[ -d "$dir" ]] || return 0
  mkdir -p "$dir/.forgejo/workflows"
  cat >"$dir/.forgejo/workflows/ci.yml" <<'YAML'
name: CI
on:
  push:
    branches: [master, main]
  pull_request:
    branches: [master, main]

jobs:
  build:
    runs-on: native
    steps:
      - uses: https://data.forgejo.org/actions/checkout@v4
      - name: Install Node
        run: |
          set -euo pipefail
          ver=22.14.0
          mkdir -p "$HOME/.local"
          if [ ! -x "$HOME/.local/node/bin/node" ] || ! "$HOME/.local/node/bin/node" -v | grep -q "v${ver}"; then
            curl -fsSL "https://nodejs.org/dist/v${ver}/node-v${ver}-linux-x64.tar.xz" | tar -xJ -C "$HOME/.local"
            mv "$HOME/.local/node-v${ver}-linux-x64" "$HOME/.local/node"
          fi
          echo "$HOME/.local/node/bin" >> "$GITHUB_PATH"
      - run: npm ci
      - run: npm run typecheck
      - run: npm run build
      - name: Assert dist-app
        run: test -f dist-app/index.html
YAML
  echo "installed: media-ui-app (node)"
}

if [[ ! -f "$GOLDEN" ]]; then
  echo "missing golden template: $GOLDEN" >&2
  exit 1
fi

for dir in "$ROOT"/*/; do
  install_go_ci "$dir"
done
install_media_ui_ci

echo "done — commit .forgejo/workflows/ci.yml in each repo and push to git.zem.systems"
