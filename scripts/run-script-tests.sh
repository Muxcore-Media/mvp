#!/usr/bin/env bash
# Run offline script tests under _mvp/scripts/.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
failed=0
WS="$(cd "$ROOT/.." && pwd)"
# Proto-drift checks live in the umbrella (../scripts). When this repo is
# checked out on its own (e.g. GitHub Actions for Muxcore-Media/mvp), skip them.
for check in check-proto-drift.sh check-proto-drift_test.sh; do
  echo "==> $check"
  if [[ ! -f "$WS/scripts/$check" ]]; then
    echo "skip: $WS/scripts/$check not found (not inside the umbrella workspace)"
    continue
  fi
  if ! bash "$WS/scripts/$check"; then
    failed=1
  fi
done
echo "==> check-household-manifest.sh"
if ! bash "$ROOT/scripts/check-household-manifest.sh"; then
  failed=1
fi
echo "==> check-state-coverage.sh"
if ! bash "$ROOT/scripts/check-state-coverage.sh"; then
  failed=1
fi
for t in "$SCRIPT_DIR"/*_test.sh; do
  [[ -f "$t" ]] || continue
  echo "==> $(basename "$t")"
  if ! bash "$t"; then
    failed=1
  fi
done
exit "$failed"
