#!/usr/bin/env bash
# Compare workspace git repos with Forgejo org listing and git SSH reachability.
# Usage: ./_mvp/scripts/audit-forgejo-mirrors.sh [workspace_root]
set -euo pipefail

ROOT="${1:-$(cd "$(dirname "$0")/../.." && pwd)}"
FORGEJO_API="${FORGEJO_API:-https://git.zem.systems/api/v1}"
ORG="${FORGEJO_ORG:-muxcore}"
GIT_SSH="${GIT_SSH:-ssh://forgejo@git.zem.systems:2222}"

mapfile -t WORKSPACE < <(
  find "$ROOT" -maxdepth 2 -name .git -type d -printf '%h\n' \
    | xargs -I{} basename {} \
    | sed 's/core\.wiki/core-wiki/' \
    | grep -vE '^(_mvp|_wave1)$' \
    | sort -u
)

mapfile -t FORGEJO < <(
  page=1
  names=()
  while :; do
  batch="$(curl -fsSL "${FORGEJO_API}/orgs/${ORG}/repos?limit=50&page=${page}" | jq -r '.[].name' 2>/dev/null || true)"
    [[ -z "$batch" ]] && break
    while IFS= read -r n; do
      [[ -n "$n" ]] && names+=("$n")
    done <<<"$batch"
    ((page++))
    [[ "$page" -gt 10 ]] && break
  done
  printf '%s\n' "${names[@]}" | sort -u
)

missing_api=()
git_ok_no_api=()
no_git=()

for name in "${WORKSPACE[@]}"; do
  forgejo_name="$name"
  [[ "$name" == "media-tvos-app" ]] && forgejo_name="muxcore-tvos"
  if ! printf '%s\n' "${FORGEJO[@]}" | grep -qx "$forgejo_name"; then
    missing_api+=("$name")
    dir="$ROOT/$name"
    [[ "$name" == "core-wiki" ]] && dir="$ROOT/core.wiki"
    if git -C "$dir" ls-remote "${GIT_SSH}/${ORG}/${forgejo_name}.git" HEAD &>/dev/null; then
      git_ok_no_api+=("$name")
    else
      no_git+=("$name")
    fi
  fi
done

echo "Workspace repos: ${#WORKSPACE[@]}"
echo "Forgejo org API: ${#FORGEJO[@]}"
echo
echo "Missing from org API (${#missing_api[@]}):"
printf '  %s\n' "${missing_api[@]}"
echo
echo "Git SSH reachable but not in org API (${#git_ok_no_api[@]}):"
printf '  %s\n' "${git_ok_no_api[@]}"
echo
echo "Neither API nor git SSH (${#no_git[@]}):"
printf '  %s\n' "${no_git[@]}"
