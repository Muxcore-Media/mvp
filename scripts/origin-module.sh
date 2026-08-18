#!/usr/bin/env bash
# Pin module installs to origin/main (or origin/master).
# Refuse unpublished muxcore.json / Info() version strings.
set -euo pipefail

json_version() {
  # First "version" value in the file (module version is listed before contract versions).
  grep -o '"version"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed -n 's/.*"\([^"]*\)"$/\1/p'
}

go_info_version() {
  grep -E 'Version:[[:space:]]+"' | head -1 | sed -n 's/.*Version:[[:space:]]*"\([^"]*\)".*/\1/p'
}

origin_ref_for() {
  local repo="$1"
  if git -C "$repo" rev-parse --verify -q origin/main >/dev/null; then
    echo origin/main
    return 0
  fi
  if git -C "$repo" rev-parse --verify -q origin/master >/dev/null; then
    echo origin/master
    return 0
  fi
  echo "origin-module: no origin/main or origin/master in $repo" >&2
  return 1
}

origin_muxcore_version() {
  local repo="$1" ref="$2"
  git -C "$repo" show "${ref}:muxcore.json" | json_version
}

origin_go_version() {
  local repo="$1" ref="$2"
  git -C "$repo" show "${ref}:internal/module.go" | go_info_version
}

# Assert $repo can be installed: HEAD is origin, muxcore.json and Info()
# versions match origin, and the requested version (if any) is that origin version.
assert_module_on_origin() {
  local repo="$1"
  local want="${2:-}"
  if [[ ! -d "$repo/.git" && ! -f "$repo/.git" ]]; then
    echo "origin-module: not a git repo: $repo" >&2
    return 1
  fi
  if [[ ! -f "$repo/muxcore.json" ]]; then
    echo "origin-module: missing muxcore.json in $repo" >&2
    return 1
  fi
  local ref sha origin_ver go_ver local_ver head
  ref="$(origin_ref_for "$repo")"
  sha="$(git -C "$repo" rev-parse "$ref")"
  origin_ver="$(origin_muxcore_version "$repo" "$ref")"
  go_ver="$(origin_go_version "$repo" "$ref")"
  head="$(git -C "$repo" rev-parse HEAD)"
  local_ver="$(json_version <"$repo/muxcore.json")"

  if [[ -z "$origin_ver" ]]; then
    echo "origin-module: empty version on $ref" >&2
    return 1
  fi
  if [[ -n "$go_ver" && "$go_ver" != "$origin_ver" ]]; then
    echo "origin-module: $ref muxcore.json version=$origin_ver but Info() Version=$go_ver" >&2
    return 1
  fi
  if [[ "$head" != "$sha" ]]; then
    echo "origin-module: HEAD $(git -C "$repo" rev-parse --short HEAD) is not $ref $(git -C "$repo" rev-parse --short "$ref"); push before install" >&2
    return 1
  fi
  if ! git -C "$repo" diff --quiet -- muxcore.json; then
    echo "origin-module: muxcore.json is dirty; unpublished version would drift from $ref" >&2
    return 1
  fi
  if [[ "$local_ver" != "$origin_ver" ]]; then
    echo "origin-module: refuse unpublished version $local_ver ( $ref is $origin_ver )" >&2
    return 1
  fi
  if [[ -n "$want" && "$want" != "$origin_ver" ]]; then
    echo "origin-module: refuse unpublished version $want ( $ref is $origin_ver )" >&2
    return 1
  fi
  printf '%s\t%s\t%s\n' "$origin_ver" "$sha" "$ref"
}

format_origin_record() {
  local name="$1" version="$2" sha="$3" ref="$4"
  local ts
  ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  printf 'module=%s version=%s sha=%s origin_ref=%s built_at=%s\n' \
    "$name" "$version" "$sha" "$ref" "$ts"
}
