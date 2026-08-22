#!/usr/bin/env bash
# Mirror local module clones to vault Forgejo (org push-to-create enabled).
# Run from desk on the MuxCore workspace parent; requires SSH to vault as ender.
set -euo pipefail

ROOT="${MUXCORE_ROOT:-/home/ender/Projects/MuxCore}"
VAULT_ZT='fd2c:a2fd:5d9e:ab72:9d99:930d:f160:3e95'
VAULT_USER="${MUXCORE_VAULT_USER:-ender}"
VAULT="${MUXCORE_VAULT_SSH:-${VAULT_USER}@${VAULT_ZT}}"
VAULT_SCP="${VAULT_USER}@[${VAULT_ZT}]"
FORGEJO_PUSH='ssh://forgejo@127.0.0.1:2222/muxcore'
SSH_OPTS=(-6 -o BatchMode=yes)

# local_dir:forgejo_name (default name = basename)
declare -A MAP=(
  [contracts-automation]=contracts-automation
  [contracts-media]=contracts-media
  [contracts-metadata]=contracts-metadata
  [contracts-playback]=contracts-playback
  [contracts-scanner]=contracts-scanner
  [emby]=emby
  [media-library-maintainer]=media-library-maintainer
  [muxcore-ios]=muxcore-ios
  [playback-guard]=playback-guard
  [playback-monitor]=playback-monitor
  [plex]=plex
  [media-tvos-app]=muxcore-tvos
  [core.wiki]=core-wiki
)

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

ok=0
fail=0

for local in "${!MAP[@]}"; do
  forgejo="${MAP[$local]}"
  repo_path="$ROOT/$local"
  if [[ ! -d "$repo_path/.git" ]]; then
    echo "SKIP $local (no .git)"
    continue
  fi
  bundle="$tmpdir/${forgejo}.bundle"
  echo "== $local -> muxcore/$forgejo =="
  git -C "$repo_path" bundle create "$bundle" --all
  scp "${SSH_OPTS[@]}" "$bundle" "${VAULT_SCP}:/tmp/muxcore-mirror.bundle"
  if ssh "${SSH_OPTS[@]}" "$VAULT" bash -s "$forgejo" <<'REMOTE'
set -euo pipefail
name="$1"
work="/tmp/muxcore-mirror-$name"
rm -rf "$work"
git clone /tmp/muxcore-mirror.bundle "$work"
cd "$work"
branch="$(git symbolic-ref --short HEAD 2>/dev/null || echo main)"
# Push branch + tags to org repo (auto-creates when missing).
git push "ssh://forgejo@127.0.0.1:2222/muxcore/${name}.git" "$branch:main" --tags
REMOTE
  then
    echo "OK $forgejo"
    ok=$((ok + 1))
  else
    echo "FAIL $forgejo" >&2
    fail=$((fail + 1))
  fi
done

echo "done: ok=$ok fail=$fail"
exit "$([[ $fail -eq 0 ]] && echo 0 || echo 1)"
