#!/usr/bin/env bash
# Build and deploy a MuxCore module to the vault MVP soak stack.
#
# Wraps the recipes in workspace AGENTS.md (build → scp → stop-one → restart).
#
# Usage:
#   ./scripts/deploy-module-to-vault.sh admin-ui
#   ./scripts/deploy-module-to-vault.sh media-ui-app
#   ./scripts/deploy-module-to-vault.sh media-scanner   # origin-pinned (install-origin-module.sh)
#   ./scripts/deploy-module-to-vault.sh muxcorectl      # CLI only (no restart)
#   ./scripts/deploy-module-to-vault.sh admin-ui --build-only
#   MVP_DEPLOY_SSH=ender@192.168.40.153 ./scripts/deploy-module-to-vault.sh core
#
# Env:
#   MVP_DEPLOY_SSH   ssh target (default vault ZT IPv6 as ender)
#   MVP_DEPLOY_MVP   remote MVP root (default /mnt/fast-storage/appdata/muxcore/mvp)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
WS="$(cd "$ROOT/.." && pwd)"

DEFAULT_VAULT='ender@fd2c:a2fd:5d9e:ab72:9d99:930d:f160:3e95'
SSH_TARGET="${MVP_DEPLOY_SSH:-$DEFAULT_VAULT}"
REMOTE_MVP="${MVP_DEPLOY_MVP:-/mnt/fast-storage/appdata/muxcore/mvp}"
REMOTE_MEDIA_UI_DIST="${MVP_DEPLOY_MEDIA_UI_DIST:-/mnt/fast-storage/appdata/muxcore/media-ui-app/dist-app}"

BUILD_ONLY=0
NO_RESTART=0
NO_VERIFY=0
VERIFY=0
VERIFY_PUBLIC=0
VERIFY_ALL=0
SKIP_PREFLIGHT=0
LIST_ONLY=0
MODULE=""

ORIGIN_PINNED=(media-scanner media-automation downloader-native-torrent)

usage() {
  echo "usage: $0 <module> [--build-only] [--no-restart] [--no-verify] [--verify] [--verify-public] [--verify-all]" >&2
  echo "       $0 --list" >&2
  echo "  module: service name (admin-ui, media-ui, core, muxcorectl, media-ui-app, …)" >&2
  echo "  post-deploy module health runs by default; use --no-verify to skip" >&2
  echo "  --verify-all: health + public edge + muxcorectl mesh (implies --verify and --verify-public)" >&2
  echo "  env: MVP_SKIP_PREFLIGHT=1 to skip SSH preflight" >&2
  exit 2
}

list_modules() {
  echo "MuxCore vault deploy targets"
  echo "  ssh:  $SSH_TARGET"
  echo "  mvp:  $REMOTE_MVP"
  echo ""
  echo "Special artifacts (not 1:1 module dir names):"
  printf '  %-22s %s\n' "media-ui-app" "npm build + rsync dist-app; restarts media-ui"
  printf '  %-22s %s\n' "muxcorectl" "muxcorectl-cli; no service restart"
  printf '  %-22s %s\n' "core / muxcored" "binary muxcored; service core"
  printf '  %-22s %s\n' "media-ui / mediauiprox" "binary mediauiprox; service media-ui"
  echo ""
  echo "Origin-pinned (use install-origin-module.sh — push to Forgejo first):"
  for o in "${ORIGIN_PINNED[@]}"; do
    echo "  $o"
  done
  echo ""
  echo "run-host.sh services:"
  grep -oE 'maybe_start [a-z0-9-]+' "$ROOT/run-host.sh" | awk '{print $2}' | sort -u | while read -r svc; do
  [[ "$svc" == core ]] && continue
    printf '  %s\n' "$svc"
  done
  echo ""
  echo "Smoke / verify (from umbrella workspace):"
  echo "  scripts/smoke-vault-all.sh"
  echo "  scripts/preflight-vault-ssh.sh"
  echo "  scripts/muxcorectl-vault.sh health status"
  echo "  deploy flags: --no-verify --verify --verify-public --verify-all"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --build-only) BUILD_ONLY=1; shift ;;
    --no-restart) NO_RESTART=1; shift ;;
    --no-verify) NO_VERIFY=1; shift ;;
    --verify) VERIFY=1; shift ;;
    --verify-public) VERIFY_PUBLIC=1; shift ;;
    --verify-all) VERIFY_ALL=1; shift ;;
    --skip-preflight) SKIP_PREFLIGHT=1; shift ;;
    --list) LIST_ONLY=1; shift ;;
    -h|--help) usage ;;
    *)
      if [[ -z "$MODULE" ]]; then
        MODULE="$1"
      else
        echo "unexpected argument: $1" >&2
        usage
      fi
      shift
      ;;
  esac
done

if [[ "$LIST_ONLY" -eq 1 ]]; then
  list_modules
  exit 0
fi

[[ -n "$MODULE" ]] || usage

# Common aliases (binary name ≠ run-host service name).
case "$MODULE" in
  muxcored) MODULE=core ;;
  mediauiprox) MODULE=media-ui ;;
esac

is_origin_pinned() {
  local m="$1"
  for o in "${ORIGIN_PINNED[@]}"; do
    [[ "$o" == "$m" ]] && return 0
  done
  return 1
}

run_go() {
  local dir="$1"
  local cmd="$2"
  if command -v go >/dev/null 2>&1; then
    (cd "$dir" && eval "$cmd")
  else
    nix-shell -p go --run "cd \"$dir\" && $cmd"
  fi
}

run_node() {
  local dir="$1"
  local cmd="$2"
  if command -v npm >/dev/null 2>&1; then
    (cd "$dir" && eval "$cmd")
  else
    nix-shell -p nodejs --run "cd \"$dir\" && $cmd"
  fi
}

# Remote bin filename on vault (may differ from service name).
remote_bin_name() {
  case "$1" in
    core) echo muxcored ;;
    media-ui) echo mediauiprox ;;
    muxcorectl) echo muxcorectl ;;
    *) echo "$1" ;;
  esac
}

# Service name for run-host.sh restart/stop-one.
service_name() {
  case "$1" in
    muxcorectl) echo "" ;;
    muxcored) echo core ;;
    mediauiprox) echo media-ui ;;
    *) echo "$1" ;;
  esac
}

build_module() {
  local m="$MODULE"
  local out="/tmp/${m}-linux-amd64"
  local bin_name
  bin_name="$(remote_bin_name "$m")"
  out="/tmp/${bin_name}-linux-amd64"

  if is_origin_pinned "$m"; then
    echo "==> origin-pinned $m (install-origin-module.sh)"
    MVP_DEPLOY_SSH="$SSH_TARGET" "$ROOT/scripts/install-origin-module.sh" "$m" --dest "$ROOT/bin"
    cp "$ROOT/bin/$m" "$out"
    return 0
  fi

  case "$m" in
    core)
      echo "==> building muxcored"
      run_go "$WS/core" \
        "GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags='-s -w' -o '$out' ./cmd/muxcored"
      ;;
    admin-ui)
      echo "==> building admin-ui (css + binary)"
      if command -v make >/dev/null 2>&1; then
        (cd "$WS/admin-ui" && make css)
      else
        run_go "$WS/admin-ui" "make css" 2>/dev/null || run_node "$WS/admin-ui" "npm ci && npx @tailwindcss/cli -i input.css -o assets/dist/styles.css --minify" || true
      fi
      ver="${ADMIN_UI_VERSION:-0.1.10}"
      run_go "$WS/admin-ui" \
        "GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags='-s -w -X main.version=${ver}' -o '$out' ."
      ;;
    media-ui)
      echo "==> building mediauiprox"
      run_go "$ROOT" \
        "GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags='-s -w' -o '$out' ./cmd/mediauiprox"
      ;;
    media-ui-app)
      echo "==> building media-ui-app (npm)"
      run_node "$WS/media-ui-app" "npm ci && npm run build"
      echo "built SPA in $WS/media-ui-app/dist-app (rsync on deploy)"
      return 0
      ;;
    muxcorectl)
      echo "==> building muxcorectl"
      run_go "$WS/muxcorectl-cli" \
        "GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags='-s -w' -o '$out' ./cmd/muxcorectl"
      ;;
    *)
      if [[ -d "$WS/$m/cmd/module" ]]; then
        echo "==> building $m (cmd/module)"
        run_go "$WS/$m" \
          "GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags='-s -w' -o '$out' ./cmd/module"
      elif [[ -f "$WS/$m/main.go" ]]; then
        echo "==> building $m (package root)"
        run_go "$WS/$m" \
          "GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags='-s -w' -o '$out' ."
      else
        echo "deploy-module: cannot infer build for $m (no cmd/module or main.go)" >&2
        exit 1
      fi
      ;;
  esac

  echo "==> built $out"
}

scp_to_vault() {
  local local_path="$1"
  local remote_name="$2"
  local remote_tmp="/tmp/${remote_name}.new"
  echo "==> scp to $SSH_TARGET:$remote_tmp"
  scp -6 -o BatchMode=yes -o ConnectTimeout=30 "$local_path" "${SSH_TARGET}:${remote_tmp}"
}

remote_install_binary() {
  local remote_name="$1"
  local svc="$2"
  local remote_tmp="/tmp/${remote_name}.new"
  local remote_bin_dir="$REMOTE_MVP/bin"
  local atomic="$REMOTE_MVP/scripts/deploy-atomic.sh"

  echo "==> bootstrap deploy-atomic.sh on vault"
  scp -6 -o BatchMode=yes -o ConnectTimeout=30 \
    "$SCRIPT_DIR/deploy-atomic.sh" "${SSH_TARGET}:${atomic}"

  local install_args=(
    install
    --bin-dir "$remote_bin_dir"
    --name "$remote_name"
    --new "$remote_tmp"
    --mvp-root "$REMOTE_MVP"
  )
  if [[ -n "$svc" && "$NO_RESTART" -eq 0 ]]; then
    install_args+=(--service "$svc")
  fi

  echo "==> atomic install $remote_name on vault"
  ssh -6 -o BatchMode=yes -o ConnectTimeout=30 "$SSH_TARGET" \
    "bash '$atomic' ${install_args[*]}"
}

remote_verify_module() {
  local module="$1"
  local atomic="$REMOTE_MVP/scripts/deploy-atomic.sh"
  ssh -6 -o BatchMode=yes -o ConnectTimeout=30 "$SSH_TARGET" \
    "bash '$atomic' verify --module '$module' --mvp-root '$REMOTE_MVP'"
}

remote_rollback_binary() {
  local remote_name="$1"
  local svc="$2"
  local remote_bin_dir="$REMOTE_MVP/bin"
  local atomic="$REMOTE_MVP/scripts/deploy-atomic.sh"
  local rollback_args=(
    rollback
    --bin-dir "$remote_bin_dir"
    --name "$remote_name"
    --mvp-root "$REMOTE_MVP"
  )
  if [[ -n "$svc" && "$NO_RESTART" -eq 0 ]]; then
    rollback_args+=(--service "$svc")
  fi
  echo "==> rolling back $remote_name from .prev"
  ssh -6 -o BatchMode=yes -o ConnectTimeout=30 "$SSH_TARGET" \
    "bash '$atomic' ${rollback_args[*]}"
}

post_deploy_verify() {
  local module="$1"
  local bin_name="$2"
  local svc="$3"
  local verify_module=1

  if [[ "$NO_VERIFY" -eq 1 ]]; then
    verify_module=0
  fi
  if [[ "$NO_RESTART" -eq 1 && -z "$svc" ]]; then
    verify_module=0
  fi

  if [[ "$verify_module" -eq 1 ]]; then
    if ! remote_verify_module "$module"; then
      remote_rollback_binary "$bin_name" "$svc" || true
      echo "deploy-module: health check failed; rolled back $module" >&2
      return 1
    fi
  fi

  [[ "$VERIFY" -eq 1 ]] && verify_health
  [[ "$VERIFY_PUBLIC" -eq 1 ]] && verify_public
  [[ "$VERIFY_ALL" -eq 1 ]] && verify_mesh
}

rsync_media_ui_app() {
  echo "==> rsync dist-app to $SSH_TARGET:$REMOTE_MEDIA_UI_DIST"
  rsync -avz --delete \
    -e 'ssh -6 -o BatchMode=yes -o ConnectTimeout=30' \
    "$WS/media-ui-app/dist-app/" \
    "${SSH_TARGET}:${REMOTE_MEDIA_UI_DIST}/"
  if [[ "$NO_RESTART" -eq 0 ]]; then
    ssh -6 -o BatchMode=yes -o ConnectTimeout=30 "$SSH_TARGET" \
      "cd '$REMOTE_MVP' && ./run-host.sh restart media-ui"
  fi
}

verify_health() {
  MVP_DEPLOY_SSH="$SSH_TARGET" "$SCRIPT_DIR/smoke-vault-health.sh"
}

verify_public() {
  "$SCRIPT_DIR/smoke-vault-public.sh"
}

verify_mesh() {
  MVP_DEPLOY_SSH="$SSH_TARGET" MVP_DEPLOY_MVP="$REMOTE_MVP" \
    "$SCRIPT_DIR/muxcorectl-vault.sh" health status
}

run_verify() {
  [[ "$VERIFY" -eq 1 ]] && verify_health
  [[ "$VERIFY_PUBLIC" -eq 1 ]] && verify_public
  [[ "$VERIFY_ALL" -eq 1 ]] && verify_mesh
}

preflight_ssh() {
  [[ "$SKIP_PREFLIGHT" -eq 1 || "${MVP_SKIP_PREFLIGHT:-0}" == "1" ]] && return 0
  MVP_DEPLOY_SSH="$SSH_TARGET" "$SCRIPT_DIR/preflight-vault-ssh.sh"
}

main() {
  if [[ "$VERIFY_ALL" -eq 1 ]]; then
    VERIFY=1
    VERIFY_PUBLIC=1
  fi

  echo "deploy-module: $MODULE → $SSH_TARGET ($REMOTE_MVP)"
  preflight_ssh

  case "$MODULE" in
    core|muxcored|admin-ui|media-ui|mediauiprox|media-ui-app|muxcorectl) ;;
    *)
      if ! is_origin_pinned "$MODULE" && [[ ! -d "$WS/$MODULE" ]]; then
        echo "deploy-module: unknown module '$MODULE' (no sibling dir $WS/$MODULE)" >&2
        echo "  try: $0 --list" >&2
        exit 1
      fi
      ;;
  esac

  if [[ "$MODULE" == "media-ui-app" ]]; then
    if [[ "$BUILD_ONLY" -eq 1 ]] || [[ ! -d "$WS/media-ui-app/dist-app" ]]; then
      build_module
    fi
    [[ "$BUILD_ONLY" -eq 1 ]] && exit 0
    rsync_media_ui_app
    if [[ "$NO_VERIFY" -eq 0 ]]; then
      remote_verify_module media-ui || {
        echo "deploy-module: media-ui health failed after dist-app rsync" >&2
        exit 1
      }
    fi
    [[ "$VERIFY" -eq 1 || "$VERIFY_PUBLIC" -eq 1 || "$VERIFY_ALL" -eq 1 ]] && run_verify
    echo "==> done (media-ui-app SPA)"
    return 0
  fi

  build_module
  [[ "$BUILD_ONLY" -eq 1 ]] && exit 0

  local bin_name svc local_out
  bin_name="$(remote_bin_name "$MODULE")"
  svc="$(service_name "$bin_name")"
  local_out="/tmp/${bin_name}-linux-amd64"

  if [[ ! -f "$local_out" ]]; then
    echo "deploy-module: missing build artifact $local_out" >&2
    exit 1
  fi

  scp_to_vault "$local_out" "$bin_name"

  if [[ "$NO_RESTART" -eq 1 ]]; then
    remote_install_binary "$bin_name" ""
  else
    remote_install_binary "$bin_name" "$svc"
  fi

  post_deploy_verify "$MODULE" "$bin_name" "$svc"
  echo "==> done ($MODULE → $bin_name${svc:+, restarted $svc})"
}

main
