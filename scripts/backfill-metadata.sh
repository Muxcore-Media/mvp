#!/usr/bin/env bash
# Backfill TMDB metadata (posters, overviews, etc.) for library rows missing artwork.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${BIN:-$ROOT/bin}"

MOVIES_ADDR="${MOVIES_GRPC_ADDR:-127.0.0.1:9420}"
TV_ADDR="${TVSHOWS_GRPC_ADDR:-127.0.0.1:9440}"

echo "==> Movies (missing posters only) via $MOVIES_ADDR"
"$BIN/refreshmovies" -addr "$MOVIES_ADDR" -missing-posters

echo "==> TV shows (missing posters only) via $TV_ADDR"
"$BIN/refreshtvshows" -addr "$TV_ADDR" -missing-posters

echo "==> Backfill complete"
