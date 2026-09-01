#!/usr/bin/env bash
# Populate sha256 checksum fields on spool tag modules (required modules only by default).
#
# Usage:
#   ./scripts/populate-spool-checksums.sh spool/tags/minimal.json
#   ALL_MODULES=1 ./scripts/populate-spool-checksums.sh spool/tags/default.json
#
# Requires: go, git, jq. Builds each module at the pinned version and writes
# "checksum": "sha256:..." into the tag JSON (in place).
set -euo pipefail

TAG_FILE="${1:-}"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
ALL_MODULES="${ALL_MODULES:-0}"
GO="${GO:-go}"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

die() { echo "FAIL: $*" >&2; exit 1; }

[[ -n "$TAG_FILE" ]] || die "usage: $0 <spool-tag.json>"
[[ -f "$ROOT/$TAG_FILE" ]] || die "missing $ROOT/$TAG_FILE"
command -v jq >/dev/null 2>&1 || die "jq required"

module_build_target() {
  local dir="$1"
  if [[ -d "$dir/cmd/module" ]]; then
    printf '%s\n' "./cmd/module/"
    return 0
  fi
  if [[ -f "$dir/main.go" ]]; then
    printf '%s\n' "."
    return 0
  fi
  die "no build target in $dir"
}

build_checksum() {
  local repo="$1" version="$2"
  local build_dir="$TMP/build"
  rm -rf "$build_dir"

  local name="${repo##*/}"
  name="${name%.git}"
  local local_dir="$ROOT/$name"
  local target bin sum
  bin="$TMP/${name}-$$"

  git config --global url."https://git.zem.systems/muxcore/".insteadOf "https://github.com/Muxcore-Media/" >/dev/null 2>&1 || true

  try_build() {
    local dir="$1"
    target="$(module_build_target "$dir")"
    rm -f "$bin"
    mkdir -p "$TMP/gocache"
    (cd "$dir" && GOCACHE="$TMP/gocache" GOSUMDB=off GONOSUMDB='github.com/Muxcore-Media/*' GOPRIVATE='github.com/Muxcore-Media/*' \
      "$GO" build -mod=mod -o "$bin" "$target") >&2
    [[ -f "$bin" ]]
  }

  apply_workspace_replaces() {
    local dir="$1"
    local args=()
    for mod in core contracts-media contracts-notification contracts-playback \
      contracts-scanner contracts-automation contracts-metadata contracts-media-admin \
      contracts-downloader contracts-indexer; do
      [[ -d "$ROOT/$mod" ]] && args+=(-replace "github.com/Muxcore-Media/$mod=$ROOT/$mod")
    done
    [[ -d "$ROOT/core/pkg/contracts" ]] && args+=(-replace "github.com/Muxcore-Media/core/pkg/contracts=$ROOT/core/pkg/contracts")
    [[ -d "$ROOT/core/pkg/tenant" ]] && args+=(-replace "github.com/Muxcore-Media/core/pkg/tenant=$ROOT/core/pkg/tenant")
    [[ -d "$ROOT/core/sdk/go/client" ]] && args+=(-replace "github.com/Muxcore-Media/core/sdk/go/client=$ROOT/core/sdk/go/client")
    [[ -d "$ROOT/core/sdk/go/module" ]] && args+=(-replace "github.com/Muxcore-Media/core/sdk/go/module=$ROOT/core/sdk/go/module")
    ((${#args[@]})) || return 0
    (cd "$dir" && "$GO" mod edit "${args[@]}") >/dev/null 2>&1 || true
  }

  local origin="https://git.zem.systems/muxcore/${name}.git"
  local cloned=0
  local clone_label=""

  if [[ ( -d "$local_dir/.git" || -f "$local_dir/.git" ) && -f "$local_dir/go.mod" ]]; then
    echo "  local: $name@$version (workspace)" >&2
    apply_workspace_replaces "$local_dir"
    if try_build "$local_dir"; then
      :
    else
      echo "  workspace build failed; trying origin tag" >&2
      cloned=0
    fi
  fi

  if [[ ! -f "$bin" ]]; then
    if [[ "$version" =~ ^[0-9a-fA-F]{40}$ ]]; then
      if git -c credential.helper= clone --depth 1 "$origin" "$build_dir" >/dev/null 2>&1 \
        && git -C "$build_dir" fetch --depth 1 origin "$version" >/dev/null 2>&1 \
        && git -C "$build_dir" checkout "$version" >/dev/null 2>&1; then
        cloned=1
        clone_label="$version"
      fi
    elif git -c credential.helper= clone --depth 1 --branch "$version" "$origin" "$build_dir" >/dev/null 2>&1 \
      || git -c credential.helper= clone --depth 1 --branch "${version#v}" "$origin" "$build_dir" >/dev/null 2>&1; then
      cloned=1
      clone_label="$version"
    fi
    [[ "$cloned" == "1" ]] || die "build failed for $repo@$version (workspace and origin)"
    echo "  origin: $name@$clone_label" >&2
    rm -f "$build_dir/go.sum"
    try_build "$build_dir" || {
      apply_workspace_replaces "$build_dir"
      try_build "$build_dir" || die "build produced no binary for $repo@$version"
    }
  fi

  sha256sum "$bin" | awk '{print "sha256:" $1}'
  rm -f "$bin"
}

mapfile -t modules < <(jq -c '.modules[]' "$ROOT/$TAG_FILE")
updated=0
for row in "${modules[@]}"; do
  required="$(jq -r '.required // false' <<<"$row")"
  if [[ "$ALL_MODULES" != "1" && "$required" != "true" ]]; then
    continue
  fi
  repo="$(jq -r '.repo' <<<"$row")"
  version="$(jq -r '.version' <<<"$row")"
  existing="$(jq -r '.checksum // empty' <<<"$row")"
  if [[ -n "$existing" && "$existing" == sha256:* ]]; then
    echo "skip (has checksum): $repo@$version"
    continue
  fi
  echo "build: $repo@$version"
  sum="$(build_checksum "$repo" "$version")"
  [[ "$sum" == sha256:* ]] || die "invalid checksum for $repo@$version: $sum"
  jq --arg repo "$repo" --arg version "$version" --arg sum "$sum" '
    .modules |= map(if .repo == $repo and .version == $version then .checksum = $sum else . end)
  ' "$ROOT/$TAG_FILE" >"$TMP/tag.json"
  mv "$TMP/tag.json" "$ROOT/$TAG_FILE"
  updated=$((updated + 1))
done

echo "updated $updated module checksum(s) in $TAG_FILE"
