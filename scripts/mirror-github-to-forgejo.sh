#!/usr/bin/env bash
# Mirror GitHub Muxcore-Media/* → Forgejo muxcore/* (private origin for self-hosted CI).
# Requires: gh auth with repo scope, SSH to git.zem.systems:2222 as forgejo.
#
# Usage:
#   ./scripts/mirror-github-to-forgejo.sh              # all GitHub org repos
#   ./scripts/mirror-github-to-forgejo.sh core         # one repo (GitHub name)
#   ./scripts/mirror-github-to-forgejo.sh --check core # exit 1 if Forgejo lacks GitHub tip
#   ./scripts/mirror-github-to-forgejo.sh --wait core <sha>  # poll until sha on Forgejo
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "$SCRIPT_DIR/mirror-lib.sh"

CHECK_ONLY=0
WAIT_SHA=""
WAIT_TIMEOUT="${MIRROR_WAIT_TIMEOUT:-300}"
WAIT_INTERVAL="${MIRROR_WAIT_INTERVAL:-10}"
ONLY=""

usage() {
  cat <<'EOF'
Usage: mirror-github-to-forgejo.sh [--check] [--wait REPO SHA] [REPO]

  --check REPO     Exit 0 when Forgejo has GitHub default-branch tip; else 1.
  --wait REPO SHA  Mirror REPO then poll until SHA exists on Forgejo (or timeout).
  REPO             Mirror one GitHub repo name (e.g. core, auth-local).

Env: GITHUB_TOKEN/GH_TOKEN, FORGEJO_GIT, MIRROR_WAIT_TIMEOUT (default 300s).
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --check)
      CHECK_ONLY=1
      shift
      ONLY="${1:-}"
      [[ -n "$ONLY" ]] || die "--check requires REPO"
      shift
      ;;
    --wait)
      shift
      ONLY="${1:-}"
      WAIT_SHA="${2:-}"
      [[ -n "$ONLY" && -n "$WAIT_SHA" ]] || die "--wait requires REPO and SHA"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      ONLY="$1"
      shift
      ;;
  esac
done

check_one() {
  local gh_name="$1"
  local forgejo
  forgejo="$(forgejo_repo_name "$gh_name")"
  local gh_sha fj_sha
  gh_sha="$(default_branch_sha "$GITHUB_ORG" "$gh_name")" || die "cannot read GitHub tip for ${gh_name}"
  fj_sha="$(forgejo_head_sha "$forgejo")"
  if [[ -z "$fj_sha" ]]; then
    echo "BEHIND ${gh_name}: Forgejo muxcore/${forgejo} unreachable or empty (GitHub ${gh_sha:0:12})"
    return 1
  fi
  if [[ "$gh_sha" == "$fj_sha" ]]; then
    echo "OK ${gh_name}: ${gh_sha:0:12} (Forgejo muxcore/${forgejo})"
    return 0
  fi
  if forgejo_has_sha "$forgejo" "$gh_sha"; then
    echo "OK ${gh_name}: GitHub tip ${gh_sha:0:12} present on Forgejo (HEAD ${fj_sha:0:12})"
    return 0
  fi
  echo "BEHIND ${gh_name}: GitHub ${gh_sha:0:12} missing on Forgejo muxcore/${forgejo} (HEAD ${fj_sha:0:12})"
  return 1
}

mirror_one() {
  local gh_name="$1"
  local forgejo tmp work
  forgejo="$(forgejo_repo_name "$gh_name")"
  tmp="$(mktemp -d)"
  work="$tmp/${forgejo}.git"

  echo "==> ${GITHUB_ORG}/${gh_name} → ${FORGEJO_ORG}/${forgejo}"
  git clone --mirror \
    "https://x-access-token:${GITHUB_TOKEN}@github.com/${GITHUB_ORG}/${gh_name}.git" \
    "$work"
  git -C "$work" push --mirror "${FORGEJO_GIT}/${forgejo}.git"
  rm -rf "$tmp"
  echo "    OK"
}

wait_for_sha() {
  local gh_name="$1" want_sha="$2"
  local forgejo deadline now
  forgejo="$(forgejo_repo_name "$gh_name")"
  deadline=$((SECONDS + WAIT_TIMEOUT))
  echo "waiting for ${want_sha:0:12} on Forgejo muxcore/${forgejo} (timeout ${WAIT_TIMEOUT}s)"
  while (( SECONDS < deadline )); do
    if forgejo_has_sha "$forgejo" "$want_sha"; then
      echo "OK ${gh_name}: ${want_sha:0:12} on Forgejo"
      return 0
    fi
    mirror_one "$gh_name" || true
    sleep "$WAIT_INTERVAL"
  done
  die "timeout: ${want_sha:0:12} still missing on Forgejo muxcore/${forgejo} after ${WAIT_TIMEOUT}s — run mirror-github-to-forgejo.sh ${gh_name} on desk or check forgejo SSH"
}

main() {
  require_github_token

  if [[ "$CHECK_ONLY" == 1 ]]; then
    check_one "$ONLY"
    exit $?
  fi

  if [[ -n "$WAIT_SHA" ]]; then
    mirror_one "$ONLY"
    wait_for_sha "$ONLY" "$WAIT_SHA"
    exit 0
  fi

  local repos=() name ok=0 fail=0
  if [[ -n "$ONLY" ]]; then
    repos=("$ONLY")
  else
    mapfile -t repos < <(list_github_repos)
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
