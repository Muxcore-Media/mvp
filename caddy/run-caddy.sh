#!/usr/bin/env bash
# Run Caddy edge proxy for the MVP host stack (ports 80/443).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TLS_DIR="${MVP_TLS_DIR:-$ROOT/tls}"
export MVP_TLS_DIR="$TLS_DIR"
CADDYFILE="${MVP_CADDYFILE:-$ROOT/Caddyfile}"

# Bypass down local nix binary cache (ministry).
export NIX_CONFIG="${NIX_CONFIG:-substituters = https://cache.nixos.org/
trusted-public-keys = cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=}"

if [[ ! -f "$TLS_DIR/certs/gringotts.crt" || ! -f "$TLS_DIR/certs/gringotts.key" ]]; then
  echo "missing TLS certs — run: $TLS_DIR/gen-mesh-ca.sh && $TLS_DIR/gen-host-cert.sh gringotts" >&2
  exit 1
fi
if [[ ! -f "$CADDYFILE" ]]; then
  echo "missing Caddyfile at $CADDYFILE" >&2
  exit 1
fi

cd "$ROOT"

# Prefer a writable caddy binary with CAP_NET_BIND_SERVICE; fall back to sudo -n.
CADDY_BIN="${MVP_CADDY_BIN:-$ROOT/bin/caddy}"
mkdir -p "$ROOT/bin"
if [[ ! -x "$CADDY_BIN" ]]; then
  echo "resolving caddy via nix into $CADDY_BIN"
  nix shell --option substituters 'https://cache.nixos.org/' \
    --option trusted-public-keys 'cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=' \
    nixpkgs#caddy \
    -c bash -lc "cp \"\$(command -v caddy)\" \"$CADDY_BIN\" && chmod 755 \"$CADDY_BIN\""
fi
if command -v setcap >/dev/null 2>&1; then
  if ! getcap "$CADDY_BIN" 2>/dev/null | grep -q 'cap_net_bind_service'; then
    sudo -n setcap 'cap_net_bind_service=+ep' "$CADDY_BIN" 2>/dev/null \
      || echo "WARN: setcap failed; will try sudo -n caddy" >&2
  fi
fi

run_caddy() {
  exec "$@" run --config "$CADDYFILE" --adapter caddyfile
}

if getcap "$CADDY_BIN" 2>/dev/null | grep -q 'cap_net_bind_service'; then
  run_caddy "$CADDY_BIN"
fi

# Last resort: sudo (NOPASSWD on gringotts).
if sudo -n true 2>/dev/null; then
  exec sudo -n -E "$CADDY_BIN" run --config "$CADDYFILE" --adapter caddyfile
fi

echo "cannot bind :80/:443 — setcap failed and sudo -n unavailable" >&2
exit 1
