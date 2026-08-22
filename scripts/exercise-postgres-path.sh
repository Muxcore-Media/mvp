#!/usr/bin/env bash
# Exercise database-postgres against a live Postgres (non-soak verification).
# Vault soak stays on database-sqlite; this script is for laptop/lab proof per MIGRATION-SQLITE.md.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
MODULE="$ROOT/database-postgres"

export PGHOST="${PGHOST:-localhost}"
export PGPORT="${PGPORT:-5432}"
export PGUSER="${PGUSER:-muxcore}"
export PGDATABASE="${PGDATABASE:-muxcore}"
export PGSSLMODE="${PGSSLMODE:-disable}"

echo "== database-postgres integration (PGHOST=$PGHOST PGUSER=$PGUSER PGDATABASE=$PGDATABASE) =="

if ! command -v pg_isready >/dev/null 2>&1; then
  echo "pg_isready not found; set PG* and ensure Postgres is listening" >&2
  exit 1
fi

if ! pg_isready -h "$PGHOST" -p "$PGPORT" -q; then
  echo "Postgres not accepting connections on $PGHOST:$PGPORT" >&2
  echo "Start with: docker run --rm -d --name muxcore-pg -e POSTGRES_USER=muxcore -e POSTGRES_PASSWORD=muxcore -e POSTGRES_DB=muxcore -p 5432:5432 postgres:16-alpine" >&2
  exit 1
fi

cd "$MODULE"
go test -count=1 ./...

echo "OK: database-postgres tests passed against live Postgres"
