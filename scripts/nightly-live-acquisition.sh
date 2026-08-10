#!/usr/bin/env bash
# Nightly live acquisition smoke — NOT for PR CI.
# Requires VPN/WG + PIRATEBAY_API_BASE + DOWNLOADER_ENGINE=anacrolix on this host.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

if [[ -f .env ]]; then
  set -a
  # shellcheck disable=SC1091
  source .env
  set +a
fi

: "${PIRATEBAY_API_BASE:?set PIRATEBAY_API_BASE (e.g. https://apibay.org)}"
if [[ "${DOWNLOADER_ENGINE:-}" == "fixture" || -z "${DOWNLOADER_ENGINE:-}" ]]; then
  echo "DOWNLOADER_ENGINE must be anacrolix (or non-fixture) for live acquisition" >&2
  exit 2
fi
if [[ "${WG_KILL_SWITCH:-false}" == "true" ]]; then
  echo "WG_KILL_SWITCH=true is forbidden on mesh hub nightly runs" >&2
  exit 2
fi

export SMOKE_LIVE_ACQUISITION=1
export SMOKE_LIVE_TIMEOUT="${SMOKE_LIVE_TIMEOUT:-5m}"
export SMOKE_LIVE_MIN_SEEDERS="${SMOKE_LIVE_MIN_SEEDERS:-1}"
export SMOKE_LIVE_MIN_BYTES="${SMOKE_LIVE_MIN_BYTES:-131072}"

mkdir -p run
ts="$(date -Iseconds)"
echo "==> nightly live acquisition start $ts"

if [[ ! -f run/core.pid ]] || ! kill -0 "$(cat run/core.pid)" 2>/dev/null; then
  echo "==> host stack not up; starting run-host.sh"
  ./run-host.sh up
  sleep 8
fi

./smoke.sh
echo "==> nightly live acquisition OK $ts"
