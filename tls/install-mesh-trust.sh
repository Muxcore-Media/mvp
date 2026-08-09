#!/usr/bin/env bash
# Install MuxCore Mesh CA + *.gringotts hosts entries on this machine.
# Usage: sudo ./install-mesh-trust.sh [/path/to/ca.crt]
# On client mesh hosts: MESH_HOST_IP=10.10.0.3 (default).
# On gringotts itself:  MESH_HOST_IP=127.0.0.1 ./install-mesh-trust.sh
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
CA_SRC="${1:-$ROOT/ca/ca.crt}"
MESH_IP="${MESH_HOST_IP:-10.10.0.3}"
HOSTS_MARKER="# muxcore-mesh-gringotts"

if [[ ! -f "$CA_SRC" ]]; then
  echo "CA not found: $CA_SRC" >&2
  exit 1
fi

if [[ "$(id -u)" -ne 0 ]]; then
  echo "re-running with sudo..."
  exec sudo MESH_HOST_IP="$MESH_IP" "$0" "$CA_SRC"
fi

install_hosts() {
  local tmp
  tmp="$(mktemp)"
  if [[ -f /etc/hosts ]]; then
    grep -v "$HOSTS_MARKER" /etc/hosts >"$tmp" || true
  else
    : >"$tmp"
  fi
  {
    echo "$MESH_IP admin.gringotts media.gringotts api.gringotts auth.gringotts core.gringotts health.gringotts gringotts $HOSTS_MARKER"
  } >>"$tmp"
  install -m 644 "$tmp" /etc/hosts
  rm -f "$tmp"
  echo "updated /etc/hosts ($MESH_IP → *.gringotts)"
}

install_ca_debian() {
  install -d -m 755 /usr/local/share/ca-certificates
  install -m 644 "$CA_SRC" /usr/local/share/ca-certificates/muxcore-mesh-ca.crt
  if command -v update-ca-certificates >/dev/null 2>&1; then
    update-ca-certificates
    echo "installed CA via update-ca-certificates"
    return 0
  fi
  return 1
}

install_ca_nixos() {
  install -d -m 755 /etc/nixos
  install -m 644 "$CA_SRC" /etc/nixos/muxcore-mesh-ca.crt
  local frag="$ROOT/nixos-mesh-trust.nix"
  if [[ -f "$frag" ]]; then
    install -m 644 "$frag" /etc/nixos/muxcore-mesh-trust.nix
  fi
  cat <<EOF

NixOS: CA written to /etc/nixos/muxcore-mesh-ca.crt
Add to configuration.nix (or flake):

  imports = [ ./muxcore-mesh-trust.nix ];

Then: sudo nixos-rebuild switch

Until rebuild, curl can use:
  curl --cacert /etc/nixos/muxcore-mesh-ca.crt https://admin.gringotts/
EOF
}

install_hosts

if [[ -f /etc/NIXOS ]] || [[ -d /etc/nixos ]]; then
  install_ca_nixos
elif install_ca_debian; then
  :
else
  install -d -m 755 /etc/muxcore
  install -m 644 "$CA_SRC" /etc/muxcore/mesh-ca.crt
  echo "CA copied to /etc/muxcore/mesh-ca.crt (manual trust store install required)"
fi

echo
echo "Restart browsers after CA install so they pick up the new trust anchor."
echo "Test: curl --cacert $CA_SRC -sI https://admin.gringotts/ | head -5"
