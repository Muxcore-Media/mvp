#!/usr/bin/env bash
# Offline: every named-volume mount point in docker-compose.registry.yml that is
# not the image's existing /data home must be pre-created (owned by `app`) in the
# Dockerfile that builds that service. Otherwise docker/podman create the mount
# point root-owned and the non-root module cannot write to the volume.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
COMPOSE="${1:-$ROOT/docker-compose.registry.yml}"
MODULE_DF="$ROOT/dockerfiles/module.Dockerfile"
MEDIA_UI_DF="$ROOT/dockerfiles/media-ui.Dockerfile"
# Prebuilt-binary variants (ADR-0014, publish-module-images.sh PREBUILT_DIR) must
# pre-create exactly the same directories as the Dockerfiles they mirror.
MODULE_PREBUILT_DF="$ROOT/dockerfiles/module-prebuilt.Dockerfile"
MEDIA_UI_PREBUILT_DF="$ROOT/dockerfiles/media-ui-prebuilt.Dockerfile"

command -v yq >/dev/null || { echo "skip: yq not found"; exit 0; }
command -v jq >/dev/null || { echo "skip: jq not found"; exit 0; }

yq_flags=()
if yq --help 2>&1 | grep -q -- '--yaml-fix-merge-anchor-to-spec'; then
  yq_flags+=(--yaml-fix-merge-anchor-to-spec=true)
fi

# Directories a Dockerfile pre-creates: every /data/<x> token on its mkdir lines.
precreated() {
  grep -E 'mkdir -p|^[[:space:]]+/data/' "$1" | grep -oE '/data/[A-Za-z0-9._-]+' | sort -u
}

# Lines "service<TAB>target" for named-volume mounts (skips bind mounts ./ and /).
mounts="$(yq "${yq_flags[@]}" -o=json 'explode(.)' "$COMPOSE" | jq -r '
  .services | to_entries[] | .key as $svc
  | (.value.volumes // [])[] | tostring
  | select(startswith(".") or startswith("/") | not)
  | split(":") | "\($svc)\t\(.[1])"')"

module_dirs="$(precreated "$MODULE_DF")"
media_ui_dirs="$(precreated "$MEDIA_UI_DF")"

fail=0
checked=0
for pair in "$MODULE_DF:$MODULE_PREBUILT_DF" "$MEDIA_UI_DF:$MEDIA_UI_PREBUILT_DF"; do
  src="${pair%%:*}" dst="${pair#*:}"
  if [[ "$(precreated "$src")" != "$(precreated "$dst")" ]]; then
    echo "FAIL: $(basename "$dst") pre-creates different /data dirs than $(basename "$src")" >&2
    diff <(precreated "$src") <(precreated "$dst") >&2 || true
    fail=1
  fi
  grep -q -- '-u 1000' "$dst" || { echo "FAIL: $(basename "$dst") does not create the uid 1000 app user" >&2; fail=1; }
done
while IFS=$'\t' read -r svc target; do
  [[ -n "$svc" ]] || continue
  case "$target" in
    /data | /app/* | /source/*) continue ;;  # image home, core image, backup ro sources
  esac
  if [[ "$svc" == "media-ui" ]]; then
    dirs="$media_ui_dirs"
    df="media-ui.Dockerfile"
  else
    dirs="$module_dirs"
    df="module.Dockerfile"
  fi
  checked=$((checked + 1))
  if ! grep -qxF "$target" <<<"$dirs"; then
    echo "FAIL: $svc mounts a volume at $target but dockerfiles/$df does not pre-create it" >&2
    fail=1
  fi
done <<<"$mounts"

((checked > 0)) || { echo "FAIL: no non-/data mount points found in $COMPOSE (parse error?)" >&2; exit 1; }
[[ "$fail" -eq 0 ]] || exit 1
echo "OK: compose mount points pre-created in Dockerfiles ($checked mounts checked)"
