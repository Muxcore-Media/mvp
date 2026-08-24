#!/usr/bin/env bash
# Commit and push Forgejo CI workflows + README badge updates to vault Forgejo.
# Run from desk on the MuxCore workspace; requires SSH to vault as ender.
set -euo pipefail

ROOT="${MUXCORE_ROOT:-/home/ender/Projects/MuxCore}"
VAULT_ZT='fd2c:a2fd:5d9e:ab72:9d99:930d:f160:3e95'
VAULT_USER="${MUXCORE_VAULT_USER:-ender}"
VAULT="${MUXCORE_VAULT_SSH:-${VAULT_USER}@${VAULT_ZT}}"
VAULT_SCP="${VAULT_USER}@[${VAULT_ZT}]"
FORGEJO_PUSH='ssh://forgejo@127.0.0.1:2222/muxcore'
SSH_OPTS=(-6 -o BatchMode=yes)

# local_dir:forgejo_name overrides (default: basename without leading _)
declare -A FORGEJO_NAME=(
  [_mvp]=mvp
  [_wave1]=wave1
  [media-tvos-app]=muxcore-tvos
  [core.wiki]=core-wiki
)

SKIP=(
  cache-memory custom-scripts media-jellyfin muxcorectl media-ui
  _wave1 muxcore-docs
)

skip_dir() {
  local name="$1"
  for s in "${SKIP[@]}"; do
    [[ "$name" == "$s" ]] && return 0
  done
  return 1
}

forgejo_repo_name() {
  local name="$1"
  if [[ -n "${FORGEJO_NAME[$name]+x}" ]]; then
    printf '%s\n' "${FORGEJO_NAME[$name]}"
    return 0
  fi
  printf '%s\n' "${name#_}"
}

update_readme_badge() {
  local dir="$1"
  local forgejo="$2"
  local readme="$dir/README.md"
  [[ -f "$readme" ]] || return 0
  if grep -q 'git.zem.systems/muxcore/' "$readme" 2>/dev/null; then
    return 0
  fi
  if grep -q 'github.com/Muxcore-Media/.*/actions/workflows/ci.yml/badge.svg' "$readme"; then
    sed -i "s|https://github.com/Muxcore-Media/[^/]*/actions/workflows/ci.yml/badge.svg|https://git.zem.systems/muxcore/${forgejo}/actions/workflows/ci.yml/badge.svg|g" "$readme"
    sed -i "s|https://github.com/Muxcore-Media/[^/]*/actions/workflows/ci.yml)|https://git.zem.systems/muxcore/${forgejo}/actions)|g" "$readme"
    echo "  badge: $forgejo"
  fi
}

commit_local() {
  local dir="$1"
  if [[ -f "$dir/.forgejo/workflows/ci.yml" ]] && ! git -C "$dir" diff --quiet -- .forgejo 2>/dev/null; then
    git -C "$dir" add .forgejo/workflows/ci.yml
  fi
  if ! git -C "$dir" diff --quiet README.md 2>/dev/null; then
    git -C "$dir" add README.md
  fi
  if git -C "$dir" diff --cached --quiet; then
    return 1
  fi
  git -C "$dir" commit -m "ci: Forgejo origin workflow and badge"
  return 0
}

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

ok=0
fail=0
skip=0

for dir in "$ROOT"/*; do
  [[ -d "$dir/.git" ]] || continue
  name="$(basename "$dir")"
  if skip_dir "$name"; then
    continue
  fi
  forgejo="$(forgejo_repo_name "$name")"
  if [[ ! -f "$dir/.forgejo/workflows/ci.yml" ]] && [[ ! -f "$dir/go.mod" ]] && [[ "$name" != "media-ui-app" ]]; then
    continue
  fi
  update_readme_badge "$dir" "$forgejo"
  if ! commit_local "$dir"; then
    # Still push when ahead of remote even without new commit.
    if ! git -C "$dir" rev-parse @{u} >/dev/null 2>&1; then
      skip=$((skip + 1))
      continue
    fi
    ahead="$(git -C "$dir" rev-list --count @{u}..HEAD 2>/dev/null || echo 0)"
    behind="$(git -C "$dir" rev-list --count HEAD..@{u} 2>/dev/null || echo 0)"
    if [[ "$ahead" == "0" ]]; then
      skip=$((skip + 1))
      continue
    fi
  fi
  echo "== push $name -> muxcore/$forgejo =="
  bundle="$tmpdir/${forgejo}.bundle"
  git -C "$dir" bundle create "$bundle" --all
  scp "${SSH_OPTS[@]}" "$bundle" "${VAULT_SCP}:/tmp/muxcore-push.bundle"
  if ssh "${SSH_OPTS[@]}" "$VAULT" bash -s "$forgejo" <<'REMOTE'
set -euo pipefail
name="$1"
work="/tmp/muxcore-push-$name"
rm -rf "$work"
git clone /tmp/muxcore-push.bundle "$work"
cd "$work"
branch="$(git symbolic-ref --short HEAD 2>/dev/null || echo main)"
remote="ssh://forgejo@127.0.0.1:2222/muxcore/${name}.git"
git push "$remote" "$branch:main" --tags --force
REMOTE
  then
    echo "OK $forgejo"
    ok=$((ok + 1))
  else
    echo "FAIL $forgejo" >&2
    fail=$((fail + 1))
  fi
done

echo "done: ok=$ok fail=$fail skip=$skip"
exit "$([[ $fail -eq 0 ]] && echo 0 || echo 1)"
