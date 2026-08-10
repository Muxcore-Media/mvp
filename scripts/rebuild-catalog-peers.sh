#!/usr/bin/env bash
# Rebuild sibling module binaries into mvp/bin at spool catalog tags.
# Usage:
#   ./scripts/rebuild-catalog-peers.sh                 # all catalog modules with a local tree
#   ./scripts/rebuild-catalog-peers.sh media-scanner api-rest
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WS="$(cd "$ROOT/.." && pwd)"
BIN="$ROOT/bin"
CATALOG="${SPOOL_CATALOG:-$WS/spool/catalog.json}"
mkdir -p "$BIN"

if [[ ! -f "$CATALOG" ]]; then
  echo "FAIL: catalog not found at $CATALOG" >&2
  exit 1
fi

mapfile -t LINES < <(python3 - "$CATALOG" "$@" <<'PY'
import json, sys
cat = json.load(open(sys.argv[1]))
want = set(sys.argv[2:])
for m in cat["modules"]:
    name, ver = m["name"], m["version"]
    if want and name not in want:
        continue
    print(f"{name}\t{ver}")
PY
)

if [[ ${#LINES[@]} -eq 0 ]]; then
  echo "FAIL: no modules selected" >&2
  exit 1
fi

for line in "${LINES[@]}"; do
  name="${line%%$'\t'*}"
  tag="${line#*$'\t'}"
  dir="$WS/$name"
  if [[ ! -d "$dir" ]]; then
    echo "SKIP missing tree $name"
    continue
  fi
  echo "==> $name @$tag"
  git -C "$dir" fetch --tags
  git -C "$dir" checkout -q "$tag"
  if [[ "$name" == "admin-ui" ]]; then
    ver="${tag#v}"
    (cd "$dir" && go build -ldflags="-s -w -X main.version=${ver}" -o "$BIN/admin-ui" .)
  elif [[ -d "$dir/cmd/module" ]]; then
    (cd "$dir" && go build -o "$BIN/$name" ./cmd/module)
  elif [[ -f "$dir/main.go" ]]; then
    (cd "$dir" && go build -o "$BIN/$name" .)
  else
    echo "SKIP unknown layout $name"
  fi
done
echo "done"
