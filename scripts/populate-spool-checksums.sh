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
  local worktree_added=0

  if [[ -d "$local_dir/.git" && -f "$local_dir/go.mod" ]]; then
    echo "  local: $name@$version" >&2
    if git -C "$local_dir" worktree add --detach "$build_dir" "$version" >/dev/null 2>&1 \
      || git -C "$local_dir" worktree add --detach "$build_dir" "tags/$version" >/dev/null 2>&1; then
      worktree_added=1
    else
      cp -a "$local_dir" "$build_dir"
    fi
  elif [[ "$version" =~ ^[0-9a-fA-F]{40}$ ]]; then
    git clone --depth 1 "$repo" "$build_dir" >/dev/null 2>&1
    git -C "$build_dir" fetch --depth 1 origin "$version" >/dev/null 2>&1
    git -C "$build_dir" checkout "$version" >/dev/null 2>&1
  else
    git clone --depth 1 --branch "$version" "$repo" "$build_dir" >/dev/null 2>&1
  fi

  local target bin
  target="$(module_build_target "$build_dir")"
  bin="$build_dir/muxcore-module"
  (cd "$build_dir" && "$GO" build -o "$bin" "$target") >&2
  [[ -f "$bin" ]] || die "build produced no binary for $repo@$version"

  sha256sum "$bin" | awk '{print "sha256:" $1}'

  if [[ "$worktree_added" == "1" ]]; then
    git -C "$local_dir" worktree remove --force "$build_dir" >/dev/null 2>&1 || rm -rf "$build_dir"
  fi
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
