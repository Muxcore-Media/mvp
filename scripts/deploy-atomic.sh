#!/usr/bin/env bash
# Atomic binary deploy helpers: flock lock, .prev backup, health verify, rollback.
#
# Usage (CLI):
#   deploy-atomic.sh install --bin-dir DIR --name NAME --new PATH [--service SVC] [--mvp-root ROOT]
#   deploy-atomic.sh rollback --bin-dir DIR --name NAME [--service SVC] [--mvp-root ROOT]
#   deploy-atomic.sh verify --module MODULE [--mvp-root ROOT] [--run-dir DIR]
#
# Source for functions: source "$(dirname "$0")/deploy-atomic.sh"
set -euo pipefail

DEPLOY_ATOMIC_LOCK_NAME=".deploy.lock"
DEPLOY_ATOMIC_HEALTH_RETRIES="${DEPLOY_ATOMIC_HEALTH_RETRIES:-12}"
DEPLOY_ATOMIC_HEALTH_INTERVAL="${DEPLOY_ATOMIC_HEALTH_INTERVAL:-2}"

deploy_lock_path() {
  local mvp_root="${1:-}"
  if [[ -n "$mvp_root" ]]; then
    echo "$mvp_root/run/$DEPLOY_ATOMIC_LOCK_NAME"
    return 0
  fi
  echo "/tmp/$DEPLOY_ATOMIC_LOCK_NAME"
}

# Map service / module id to a health probe. Empty = process-only check.
module_health_probe() {
  case "$1" in
    core|muxcored) echo "http://127.0.0.1:8080/health" ;;
    admin-ui) echo "http://127.0.0.1:8082/health" ;;
    media-ui|mediauiprox) echo "http://127.0.0.1:5173/" ;;
    auth-local) echo "http://127.0.0.1:9401/login" ;;
    api-rest) echo "http://127.0.0.1:18080/health" ;;
    muxcorectl) echo "binary" ;;
    media-scanner|media-automation|downloader-native-torrent) echo "log:$1" ;;
    *) echo "" ;;
  esac
}

# Normalize module aliases to service names used by run-host.sh.
module_service_name() {
  case "$1" in
    muxcored) echo core ;;
    mediauiprox) echo media-ui ;;
    muxcorectl) echo "" ;;
    *) echo "$1" ;;
  esac
}

_with_deploy_lock() {
  local lock_file="$1"
  shift
  mkdir -p "$(dirname "$lock_file")"
  (
    flock -n 9 || {
      echo "deploy-atomic: another deploy in progress (lock $lock_file)" >&2
      exit 1
    }
    "$@"
  ) 9>"$lock_file"
}

_curl_health_code() {
  local url="$1"
  curl -s -o /dev/null -w '%{http_code}' --connect-timeout 3 "$url" 2>/dev/null || echo 000
}

_http_health_ok() {
  local url="$1"
  local code expect
  code="$(_curl_health_code "$url")"
  case "$url" in
    */health) expect="200 503" ;;
    *:5173/*) expect="200 302 303" ;;
    *:9401/*) expect="200 302" ;;
    *) expect="200" ;;
  esac
  [[ " $expect " == *" $code "* ]]
}

_process_running() {
  local mvp_root="$1" name="$2"
  local pidfile="$mvp_root/run/$name.pid"
  [[ -f "$pidfile" ]] && kill -0 "$(cat "$pidfile")" 2>/dev/null
}

_log_module_registered() {
  local run_dir="$1" name="$2"
  local log="$run_dir/$name.log"
  [[ -f "$log" ]] && grep -q "module registered with core id=${name}" "$log"
}

verify_module_health() {
  local module="$1"
  local mvp_root="${2:-}"
  local run_dir="${3:-}"
  local svc probe attempt code

  svc="$(module_service_name "$module")"
  [[ -n "$svc" ]] || svc="$module"
  probe="$(module_health_probe "$module")"
  [[ -n "$probe" ]] || probe="$(module_health_probe "$svc")"

  if [[ -z "$mvp_root" ]]; then
    mvp_root="."
  fi
  if [[ -z "$run_dir" ]]; then
    run_dir="$mvp_root/run"
  fi

  for attempt in $(seq 1 "$DEPLOY_ATOMIC_HEALTH_RETRIES"); do
    case "$probe" in
      binary)
        if [[ -x "$mvp_root/bin/muxcorectl" || -x "$mvp_root/bin/$module" ]]; then
          echo "deploy-atomic: health ok $module (binary present)"
          return 0
        fi
        ;;
      log:*)
        local log_name="${probe#log:}"
        if _log_module_registered "$run_dir" "$log_name"; then
          echo "deploy-atomic: health ok $module (registered in log)"
          return 0
        fi
        ;;
      http://*)
        code="$(_curl_health_code "$probe")"
        if _http_health_ok "$probe"; then
          echo "deploy-atomic: health ok $module $probe → $code"
          return 0
        fi
        ;;
      "")
        if _process_running "$mvp_root" "$svc"; then
          echo "deploy-atomic: health ok $module (pid running)"
          return 0
        fi
        ;;
    esac
    [[ "$attempt" -eq "$DEPLOY_ATOMIC_HEALTH_RETRIES" ]] && break
    sleep "$DEPLOY_ATOMIC_HEALTH_INTERVAL"
  done

  echo "deploy-atomic: health FAILED for $module (probe=${probe:-process})" >&2
  return 1
}

_run_host() {
  local mvp_root="$1"
  shift
  (cd "$mvp_root" && ./run-host.sh "$@")
}

atomic_bin_install() {
  local bin_dir="$1" name="$2" new_path="$3"
  local service="${4:-}" mvp_root="${5:-}"

  local target="$bin_dir/$name"
  local prev="$bin_dir/${name}.prev"
  local lock

  [[ -f "$new_path" ]] || {
    echo "deploy-atomic: missing new binary $new_path" >&2
    return 1
  }

  lock="$(deploy_lock_path "$mvp_root")"
  _with_deploy_lock "$lock" _atomic_bin_install_locked "$bin_dir" "$name" "$new_path" "$service" "$mvp_root"
}

_atomic_bin_install_locked() {
  local bin_dir="$1" name="$2" new_path="$3" service="$4" mvp_root="$5"
  local target="$bin_dir/$name"
  local prev="$bin_dir/${name}.prev"

  if [[ -f "$target" ]]; then
    cp -f "$target" "$prev"
    echo "deploy-atomic: backed up $name → ${name}.prev"
  fi
  if [[ -n "$service" && -n "$mvp_root" ]]; then
    _run_host "$mvp_root" stop-one "$service" >/dev/null
  fi
  mv -f "$new_path" "$target"
  chmod +x "$target"
  echo "deploy-atomic: installed $name"
  if [[ -n "$service" && -n "$mvp_root" ]]; then
    _run_host "$mvp_root" restart "$service" >/dev/null
    echo "deploy-atomic: restarted $service"
  fi
}

atomic_bin_rollback() {
  local bin_dir="$1" name="$2"
  local service="${3:-}" mvp_root="${4:-}"

  local target="$bin_dir/$name"
  local prev="$bin_dir/${name}.prev"
  local lock

  [[ -f "$prev" ]] || {
    echo "deploy-atomic: no .prev to rollback for $name" >&2
    return 1
  }

  lock="$(deploy_lock_path "$mvp_root")"
  _with_deploy_lock "$lock" _atomic_bin_rollback_locked "$bin_dir" "$name" "$service" "$mvp_root"
}

_atomic_bin_rollback_locked() {
  local bin_dir="$1" name="$2" service="$3" mvp_root="$4"
  local target="$bin_dir/$name"
  local prev="$bin_dir/${name}.prev"

  if [[ -n "$service" && -n "$mvp_root" ]]; then
    _run_host "$mvp_root" stop-one "$service" >/dev/null || true
  fi
  cp -f "$prev" "$target"
  chmod +x "$target"
  echo "deploy-atomic: restored $name from .prev"
  if [[ -n "$service" && -n "$mvp_root" ]]; then
    _run_host "$mvp_root" restart "$service" >/dev/null
    echo "deploy-atomic: restarted $service after rollback"
  fi
}

_cli_usage() {
  echo "usage: deploy-atomic.sh install --bin-dir DIR --name NAME --new PATH [--service SVC] [--mvp-root ROOT]" >&2
  echo "       deploy-atomic.sh rollback --bin-dir DIR --name NAME [--service SVC] [--mvp-root ROOT]" >&2
  echo "       deploy-atomic.sh verify --module MODULE [--mvp-root ROOT] [--run-dir DIR]" >&2
  exit 2
}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  cmd="${1:-}"
  shift || true
  case "$cmd" in
    install|rollback|verify) ;;
    -h|--help|"") _cli_usage ;;
    *) echo "deploy-atomic: unknown command $cmd" >&2; _cli_usage ;;
  esac

  BIN_DIR="" NAME="" NEW_PATH="" SERVICE="" MVP_ROOT="" MODULE="" RUN_DIR=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --bin-dir) BIN_DIR="${2:-}"; shift 2 ;;
      --name) NAME="${2:-}"; shift 2 ;;
      --new) NEW_PATH="${2:-}"; shift 2 ;;
      --service) SERVICE="${2:-}"; shift 2 ;;
      --mvp-root) MVP_ROOT="${2:-}"; shift 2 ;;
      --module) MODULE="${2:-}"; shift 2 ;;
      --run-dir) RUN_DIR="${2:-}"; shift 2 ;;
      *) echo "deploy-atomic: unknown arg $1" >&2; _cli_usage ;;
    esac
  done

  case "$cmd" in
    install)
      [[ -n "$BIN_DIR" && -n "$NAME" && -n "$NEW_PATH" ]] || _cli_usage
      atomic_bin_install "$BIN_DIR" "$NAME" "$NEW_PATH" "$SERVICE" "$MVP_ROOT"
      ;;
    rollback)
      [[ -n "$BIN_DIR" && -n "$NAME" ]] || _cli_usage
      atomic_bin_rollback "$BIN_DIR" "$NAME" "$SERVICE" "$MVP_ROOT"
      ;;
    verify)
      [[ -n "$MODULE" ]] || _cli_usage
      verify_module_health "$MODULE" "$MVP_ROOT" "$RUN_DIR"
      ;;
  esac
fi
