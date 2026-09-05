#!/usr/bin/env bash
# Mirror Forgejo muxcore/* → GitHub Muxcore-Media/* (private).
# Requires: gh auth with repo scope, SSH to git.zem.systems:2222 as forgejo.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "$SCRIPT_DIR/mirror-lib.sh"

mirror_one() {
  local forgejo="$1"
  local gh_name tmp work
  gh_name="$(github_repo_name "$forgejo")"
  tmp="$(mktemp -d)"
  work="$tmp/${forgejo}.git"

  echo "==> ${forgejo} → ${GITHUB_ORG}/${gh_name}"
  git clone --mirror "${FORGEJO_GIT}/${forgejo}.git" "$work"
  git -C "$work" push --mirror \
    "https://x-access-token:${GITHUB_TOKEN}@github.com/${GITHUB_ORG}/${gh_name}.git"
  rm -rf "$tmp"
  echo "    OK"
}

main() {
  local only="${1:-}"
  require_github_token

  local repos=() name ok=0 fail=0
  if [[ -n "$only" ]]; then
    repos=("$only")
  else
    mapfile -t repos < <(list_forgejo_repos)
  fi

  for name in "${repos[@]}"; do
    [[ -n "$name" ]] || continue
    if should_skip_repo "$name"; then
      echo "SKIP $name"
      continue
    fi
    if mirror_one "$name"; then
      ok=$((ok + 1))
    else
      echo "FAIL $name" >&2
      fail=$((fail + 1))
    fi
  done
  echo "done: ok=$ok fail=$fail"
  [[ "$fail" -eq 0 ]]
}

main "$@"
