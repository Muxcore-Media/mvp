#!/usr/bin/env bash
# Compare workspace git repos with Forgejo org listing, git SSH reachability, and GitHub tip SHA.
# Usage: ./scripts/audit-forgejo-mirrors.sh [workspace_root]
# Exit 1 when any tracked repo is missing on Forgejo or Forgejo lacks GitHub default-branch tip.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "$SCRIPT_DIR/mirror-lib.sh"

ROOT="${1:-$(cd "$SCRIPT_DIR/../.." && pwd)}"
CHECK_GITHUB_TIPS="${AUDIT_GITHUB_TIPS:-1}"

mapfile -t WORKSPACE < <(
  find "$ROOT" -maxdepth 2 -name .git -type d -printf '%h\n' \
    | xargs -I{} basename {} \
    | sed 's/core\.wiki/core-wiki/' \
    | grep -vE '^(_mvp|_wave1)$' \
    | sort -u
)

mapfile -t FORGEJO < <(list_forgejo_repos)

missing_api=()
git_ok_no_api=()
no_git=()
behind_github=()
github_ok=()

for name in "${WORKSPACE[@]}"; do
  forgejo_name="$name"
  [[ "$name" == "media-tvos-app" ]] && forgejo_name="muxcore-tvos"
  gh_name="$(github_repo_name "$forgejo_name")"

  if ! printf '%s\n' "${FORGEJO[@]}" | grep -qx "$forgejo_name"; then
    missing_api+=("$name")
    dir="$ROOT/$name"
    [[ "$name" == "core-wiki" ]] && dir="$ROOT/core.wiki"
    if git -C "$dir" ls-remote "${FORGEJO_GIT}/${forgejo_name}.git" HEAD &>/dev/null; then
      git_ok_no_api+=("$name")
    else
      no_git+=("$name")
    fi
  fi

  if [[ "$CHECK_GITHUB_TIPS" == "1" ]] && command -v gh >/dev/null 2>&1 && [[ -n "$GITHUB_TOKEN" ]]; then
    if should_skip_repo "$gh_name"; then
      continue
    fi
    gh_sha="$(default_branch_sha "$GITHUB_ORG" "$gh_name" 2>/dev/null || true)"
    [[ -n "$gh_sha" ]] || continue
    fj_sha="$(forgejo_head_sha "$forgejo_name")"
    if [[ -z "$fj_sha" ]]; then
      behind_github+=("${gh_name}: Forgejo muxcore/${forgejo_name} unreachable (GitHub ${gh_sha:0:12})")
    elif [[ "$gh_sha" == "$fj_sha" ]] || forgejo_has_sha "$forgejo_name" "$gh_sha"; then
      github_ok+=("${gh_name}: ${gh_sha:0:12}")
    else
      behind_github+=("${gh_name}: GitHub ${gh_sha:0:12} missing on Forgejo (HEAD ${fj_sha:0:12})")
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

if [[ "$CHECK_GITHUB_TIPS" == "1" ]]; then
  echo
  echo "GitHub default-branch tip synced on Forgejo (${#github_ok[@]}):"
  printf '  %s\n' "${github_ok[@]}"
  echo
  echo "GitHub tip MISSING on Forgejo (${#behind_github[@]}) — run mirror-github-to-forgejo.sh:"
  printf '  %s\n' "${behind_github[@]}"
fi

fail=0
[[ ${#missing_api[@]} -eq 0 && ${#no_git[@]} -eq 0 ]] || fail=1
[[ ${#behind_github[@]} -eq 0 ]] || fail=1
exit "$fail"
