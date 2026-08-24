#!/usr/bin/env bash
# Add contracts-scanner/automation/metadata requires + monorepo replaces to a module go.mod.
# Usage: ./scripts/ensure-contract-deps.sh ../media-scanner [scanner|automation|metadata|all]
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WS="$(cd "$ROOT/.." && pwd)"
MOD_DIR="$(cd "$1" && pwd)"
WHICH="${2:-all}"
GOMOD="$MOD_DIR/go.mod"
[[ -f "$GOMOD" ]] || { echo "no go.mod in $MOD_DIR" >&2; exit 1; }

add_require() {
  local pkg="$1" ver="$2"
  if grep -q "$pkg" "$GOMOD"; then return 0; fi
  awk -v pkg="$pkg" -v ver="$ver" '
    /^require \(/ { print; inreq=1; next }
    inreq && /^\)/ {
      print "\t" pkg " " ver
      print ")"
      inreq=0
      next
    }
    { print }
  ' "$GOMOD" > "$GOMOD.tmp" && mv "$GOMOD.tmp" "$GOMOD"
}

add_replace() {
  local pkg="$1" relname="$2"
  if grep -q "replace $pkg" "$GOMOD"; then return 0; fi
  echo "" >> "$GOMOD"
  echo "replace $pkg => $relname" >> "$GOMOD"
}

case "$WHICH" in
  scanner|all) add_require github.com/Muxcore-Media/contracts-scanner v0.1.0; add_replace github.com/Muxcore-Media/contracts-scanner "../contracts-scanner" ;;
esac
case "$WHICH" in
  automation|all) add_require github.com/Muxcore-Media/contracts-automation v0.1.0; add_replace github.com/Muxcore-Media/contracts-automation "../contracts-automation" ;;
esac
case "$WHICH" in
  metadata|all) add_require github.com/Muxcore-Media/contracts-metadata v0.1.0; add_replace github.com/Muxcore-Media/contracts-metadata "../contracts-metadata" ;;
esac

echo "updated $GOMOD ($WHICH)"
