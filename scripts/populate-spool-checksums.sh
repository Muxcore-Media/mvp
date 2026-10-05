#!/usr/bin/env bash
# Populate sha256 checksum fields on spool tag modules (required modules only by default).
#
# Usage:
#   ./scripts/populate-spool-checksums.sh spool/tags/minimal.json
#   ./scripts/populate-spool-checksums.sh spool/tags/*.json
#   ALL_MODULES=1 ./scripts/populate-spool-checksums.sh spool/tags/default.json
#
# Requires: go, git, jq. Implements ADR-0012: for each selected module, clones
# the pinned tag (git clone --depth 1 --branch <version>; 40-hex SHAs are
# fetched and checked out) into a fresh temp dir, never the workspace checkout,
# and runs the canonical build
#   CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOFLAGS=-mod=readonly GOTOOLCHAIN=go<V> \
#     go build -trimpath -buildvcs=false -ldflags=-buildid= -o <bin> ./cmd/module/
# where <V> comes from the clone's go.mod `go` line. The clone is never edited
# (no replace directives, no go.mod/go.sum changes). Fails if muxcore.json
# declares `contracts` that go.mod does not already require at >= that version.
# Writes "checksum": "sha256:..." into each tag JSON in place (overwriting any
# existing value). Each repo@version is built once per invocation, so several
# tag files can be passed together. Relative tag paths are resolved against the
# current directory, then against the umbrella root.
#
# Tag repos are private: run `gh auth setup-git` once (uses your gh login or
# GH_TOKEN) so git and go can fetch over HTTPS. Never embed tokens in URLs or
# this script.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
ALL_MODULES="${ALL_MODULES:-0}"
GO="${GO:-go}"
# Fail fast instead of prompting for credentials on private repos.
export GIT_TERMINAL_PROMPT="${GIT_TERMINAL_PROMPT:-0}"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
CACHE="$TMP/cache"
mkdir -p "$CACHE"

die() { echo "FAIL: $*" >&2; exit 1; }

(($# >= 1)) || die "usage: $0 <spool-tag.json>..."
command -v jq >/dev/null 2>&1 || die "jq required"
command -v git >/dev/null 2>&1 || die "git required"
command -v "$GO" >/dev/null 2>&1 || die "go required"

# ADR-0012 §3: every muxcore.json contracts entry must already be required by
# go.mod (the module containing the declared path) at >= the declared version.
check_contracts() {
  local dir="$1" label="$2" row crepo cver have lowest
  [[ -f "$dir/muxcore.json" ]] || return 0
  while IFS= read -r row; do
    [[ -n "$row" ]] || continue
    crepo="$(jq -r '.repo' <<<"$row")"
    cver="$(jq -r '.version' <<<"$row")"
    # The declared repo may be a package path inside a required module
    # (e.g. core/proto/gen/...): match the longest required module prefix.
    have="$(cd "$dir" && "$GO" mod edit -json | jq -r --arg m "$crepo" '.Require // [] | map(select(. as $r | $m == $r.Path or ($m | startswith($r.Path + "/")))) | sort_by(.Path | length) | last | .Version // empty')"
    [[ -n "$have" ]] || die "$label: go.mod does not require $crepo (muxcore.json declares $cver)"
    lowest="$(printf '%s\n%s\n' "$cver" "$have" | sort -V | head -n1)"
    [[ "$lowest" == "$cver" ]] || die "$label: go.mod requires $crepo $have < muxcore.json contracts $cver"
  done < <(jq -c '.contracts // [] | .[]' "$dir/muxcore.json")
}

build_checksum() {
  local repo="$1" version="$2" label="$1@$2"
  local key cfile
  key="$(printf '%s' "$label" | sha256sum | cut -d' ' -f1)"
  cfile="$CACHE/$key"
  if [[ -f "$cfile" ]]; then
    cat "$cfile"
    return 0
  fi

  local dir="$TMP/clone-$key" bin="$TMP/bin-$key"
  if [[ "$version" =~ ^[0-9a-fA-F]{40}$ ]]; then
    if ! { git clone --quiet --depth 1 "$repo" "$dir" \
      && git -C "$dir" fetch --quiet --depth 1 origin "$version" \
      && git -C "$dir" checkout --quiet "$version"; } >&2; then
      die "cannot clone $label at that commit"
    fi
  else
    git clone --quiet --depth 1 --branch "$version" "$repo" "$dir" >&2 \
      || die "cannot clone $label (is $version a tag or branch?)"
  fi

  [[ -f "$dir/go.mod" ]] || die "$label: no go.mod"
  [[ -d "$dir/cmd/module" ]] || die "$label: no ./cmd/module (the canonical build only builds ./cmd/module/)"
  check_contracts "$dir" "$label"

  local gov
  gov="$(cd "$dir" && "$GO" mod edit -json | jq -r '.Go // empty')"
  [[ -n "$gov" ]] || die "$label: go.mod has no go line"
  [[ "$gov" =~ ^[0-9]+\.[0-9]+\.[0-9]+ ]] || gov="$gov.0"

  echo "  build: $label (go$gov)" >&2
  (cd "$dir" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOFLAGS=-mod=readonly GOTOOLCHAIN="go$gov" \
    GOCACHE="$TMP/gocache" \
    "$GO" build -trimpath -buildvcs=false -ldflags=-buildid= -o "$bin" ./cmd/module/) >&2 \
    || die "go build failed for $label"
  [[ -f "$bin" ]] || die "build produced no binary for $label"

  sha256sum "$bin" | awk '{print "sha256:" $1}' >"$cfile"
  rm -rf "$dir" "$bin"
  cat "$cfile"
}

total=0
for arg in "$@"; do
  tag_file="$arg"
  if [[ ! -f "$tag_file" ]]; then
    [[ -f "$ROOT/$arg" ]] || die "missing tag file $arg"
    tag_file="$ROOT/$arg"
  fi
  mapfile -t modules < <(jq -c '.modules[]' "$tag_file")
  updated=0
  for row in "${modules[@]}"; do
    required="$(jq -r '.required // false' <<<"$row")"
    if [[ "$ALL_MODULES" != "1" && "$required" != "true" ]]; then
      continue
    fi
    repo="$(jq -r '.repo' <<<"$row")"
    version="$(jq -r '.version' <<<"$row")"
    echo "$arg: $repo@$version"
    sum="$(build_checksum "$repo" "$version")"
    [[ "$sum" == sha256:* ]] || die "invalid checksum for $repo@$version: $sum"
    jq --arg repo "$repo" --arg version "$version" --arg sum "$sum" '
      .modules |= map(if .repo == $repo and .version == $version then .checksum = $sum else . end)
    ' "$tag_file" >"$TMP/tag.json"
    cat "$TMP/tag.json" >"$tag_file"
    updated=$((updated + 1))
  done
  echo "updated $updated module checksum(s) in $arg"
  total=$((total + updated))
done
echo "total: $total"
