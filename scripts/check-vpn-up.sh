#!/usr/bin/env bash
# Prove Proton WireGuard egress is up via the source-routed iface from WG_CONF.
# Usable from gringotts smoke before any live indexer/torrent grab.
#
# Usage (on gringotts):
#   ./scripts/check-vpn-up.sh
#   WG_CONF=/home/ender/Projects/muxcore/wg-mux.conf ./scripts/check-vpn-up.sh
#
# Exit 0 only when WG_CONF is readable, iface is present, and HTTP egress via
# that iface succeeds. Never enables wg-quick / kill-switch.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MVP_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
ENV_FILE="${MUXCORE_ENV_FILE:-$MVP_ROOT/.env}"

load_env_wg() {
  if [[ -n "${WG_CONF:-}" ]]; then
    return 0
  fi
  if [[ -f "$ENV_FILE" && -s "$ENV_FILE" ]]; then
    while IFS= read -r line || [[ -n "$line" ]]; do
      case "$line" in
        ''|\#*) continue ;;
        WG_*=*)
          key="${line%%=*}"
          val="${line#*=}"
          val="${val%\"}"
          val="${val#\"}"
          val="${val%\'}"
          val="${val#\'}"
          export "$key=$val"
          ;;
      esac
    done <"$ENV_FILE"
  fi
}

iface_from_conf() {
  local conf="$1"
  local base
  base="$(basename "$conf")"
  echo "${base%.*}"
}

die() {
  echo "check-vpn-up: $*" >&2
  exit 1
}

load_env_wg

: "${WG_CONF:?WG_CONF missing — set env or WG_CONF= in $ENV_FILE}"

[[ -f "$WG_CONF" ]] || die "WG_CONF file not found: $WG_CONF"
[[ -r "$WG_CONF" ]] || die "WG_CONF unreadable: $WG_CONF"

IFACE="${WG_IFACE:-$(iface_from_conf "$WG_CONF")}"
EGRESS_URL="${VPN_EGRESS_URL:-https://ifconfig.co/ip}"
TIMEOUT_SEC="${VPN_CHECK_TIMEOUT:-15}"

if ! ip link show "$IFACE" &>/dev/null; then
  die "interface $IFACE not present (bring up source-routed WireGuard from $WG_CONF; do not use wg-quick on gringotts)"
fi

EGRESS_IP=""
if command -v curl >/dev/null 2>&1; then
  EGRESS_IP="$(curl -fsS --max-time "$TIMEOUT_SEC" --interface "$IFACE" "$EGRESS_URL" 2>/dev/null | tr -d '[:space:]' || true)"
fi

if [[ -z "$EGRESS_IP" ]]; then
  die "no egress IP via $IFACE (curl $EGRESS_URL failed). Is Proton handshake up?"
fi

PROTON_HINT=""
if curl -fsS --max-time "$TIMEOUT_SEC" --interface "$IFACE" "https://api.protonvpn.ch/vpn/location" >/tmp/muxcore-vpn-location.json 2>/dev/null; then
  PROTON_HINT=" (proton location json ok)"
fi

echo "check-vpn-up: OK iface=$IFACE egress_ip=$EGRESS_IP conf=$WG_CONF$PROTON_HINT"

if [[ "${MUXCORE_HOST_ROLE:-}" == "gringotts" ]] || hostname 2>/dev/null | grep -qi gringotts; then
  if [[ "${WG_USE_WG_QUICK:-0}" == "1" ]]; then
    die "WG_USE_WG_QUICK=1 is forbidden on gringotts"
  fi
  if [[ "${WG_KILL_SWITCH:-false}" == "true" ]]; then
    die "WG_KILL_SWITCH=true is forbidden on gringotts"
  fi
fi

exit 0
