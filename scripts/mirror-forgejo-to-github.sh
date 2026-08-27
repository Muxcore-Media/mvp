#!/usr/bin/env bash
# Mirror Forgejo muxcore/* → GitHub Muxcore-Media/* (private).
# Requires: gh auth with repo scope, SSH to git.zem.systems:2222 as forgejo.
set -euo pipefail

FORGEJO_ORG="${FORGEJO_ORG:-muxcore}"
GITHUB_ORG="${GITHUB_ORG:-Muxcore-Media}"
FORGEJO_API="${FORGEJO_API:-https://git.zem.systems/api/v1}"
FORGEJO_GIT="${FORGEJO_GIT:-ssh://forgejo@git.zem.systems:2222/${FORGEJO_ORG}}"
GITHUB_TOKEN="${GITHUB_TOKEN:-${GH_TOKEN:-$(gh auth token 2>/dev/null || true)}}"

# Forgejo repo name → GitHub repo name (default: same).
declare -A GITHUB_NAME=(
  [core-wiki]=core.wiki
)

# Skip internal / non-module mirrors.
SKIP_REPOS=(
  claude-working-directory
)

die() { echo "error: $*" >&2; exit 1; }
[[ -n "$GITHUB_TOKEN" ]] || die "set GITHUB_TOKEN or run: gh auth login"

list_forgejo_repos() {
  local page=1 batch
  while :; do
  batch="$(curl -fsSL "${FORGEJO_API}/orgs/${FORGEJO_ORG}/repos?limit=50&page=${page}" | jq -r '.[].name' 2>/dev/null || true)"
    [[ -z "$batch" ]] && break
    printf '%s\n' "$batch"
    page=$((page + 1))
    [[ "$page" -gt 20 ]] && break
  done | sort -u
}

should_skip() {
  local name="$1" s
  for s in "${SKIP_REPOS[@]}"; do
    [[ "$name" == "$s" ]] && return 0
  done
  return 1
}

github_repo_name() {
  local forgejo="$1"
  if [[ -n "${GITHUB_NAME[$forgejo]+x}" ]]; then
    printf '%s\n' "${GITHUB_NAME[$forgejo]}"
  else
    printf '%s\n' "$forgejo"
  fi
}

ensure_github_repo() {
  local gh_name="$1"
  if gh repo view "${GITHUB_ORG}/${gh_name}" >/dev/null 2>&1; then
    return 0
  fi
  echo "    creating ${GITHUB_ORG}/${gh_name} (private)"
  gh repo create "${GITHUB_ORG}/${gh_name}" --private --confirm >/dev/null
}

mirror_one() {
  local forgejo="$1"
  local gh_name
  gh_name="$(github_repo_name "$forgejo")"
  local tmp work
  tmp="$(mktemp -d)"
  work="$tmp/${forgejo}.git"

  echo "==> ${forgejo} → ${GITHUB_ORG}/${gh_name}"
  ensure_github_repo "$gh_name"

  git clone --mirror "${FORGEJO_GIT}/${forgejo}.git" "$work"
  git -C "$work" push --mirror \
    "https://x-access-token:${GITHUB_TOKEN}@github.com/${GITHUB_ORG}/${gh_name}.git"
  rm -rf "$tmp"
  echo "    OK"
}

main() {
  local only="${1:-}"
  local repos=() name
  if [[ -n "$only" ]]; then
    repos=("$only")
  else
    mapfile -t repos < <(list_forgejo_repos)
  fi

  local ok=0 fail=0
  for name in "${repos[@]}"; do
    [[ -n "$name" ]] || continue
    if should_skip "$name"; then
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
