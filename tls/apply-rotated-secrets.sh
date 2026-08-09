#!/usr/bin/env bash
# Apply newly rotated TMDB + WireGuard secrets on the MVP host.
# Requires: NEW_TMDB_API_KEY and NEW_WG_CONF_PATH (absolute path to new conf).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WS="$(cd "$ROOT/.." && pwd)"

: "${NEW_TMDB_API_KEY:?set NEW_TMDB_API_KEY to the rotated TMDB v3 api key}"
: "${NEW_WG_CONF_PATH:?set NEW_WG_CONF_PATH to the new WireGuard conf file}"

[[ -f "$NEW_WG_CONF_PATH" ]] || { echo "missing $NEW_WG_CONF_PATH" >&2; exit 1; }

umask 077
ENVF="$ROOT/.env"
touch "$ENVF"
chmod 600 "$ENVF"

if rg -q '^TMDB_API_KEY=' "$ENVF"; then
  sed -i "s|^TMDB_API_KEY=.*|TMDB_API_KEY=${NEW_TMDB_API_KEY}|" "$ENVF"
else
  printf '\nTMDB_API_KEY=%s\n' "$NEW_TMDB_API_KEY" >>"$ENVF"
fi

# Disable fixture mode so live key is used.
if rg -q '^TMDB_FIXTURE=' "$ENVF"; then
  sed -i 's|^TMDB_FIXTURE=.*|TMDB_FIXTURE=0|' "$ENVF"
fi

DEST="$WS/wg-mux.conf"
install -m 600 "$NEW_WG_CONF_PATH" "$DEST"
if rg -q '^WG_CONF=' "$ENVF"; then
  sed -i "s|^WG_CONF=.*|WG_CONF=${DEST}|" "$ENVF"
else
  printf 'WG_CONF=%s\n' "$DEST" >>"$ENVF"
fi

# Ensure kill-switch stays off on mesh hub.
if rg -q '^WG_USE_WG_QUICK=' "$ENVF"; then
  sed -i 's|^WG_USE_WG_QUICK=.*|WG_USE_WG_QUICK=0|' "$ENVF"
fi

echo "Updated $ENVF and $DEST (mode 600)."
echo "Restart stack: (cd $ROOT && ./run-host.sh stop && ./run-host.sh up)"
