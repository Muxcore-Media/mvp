#!/usr/bin/env bash
# Offline guard: stock compose must share password-reset JSON between media-ui and admin-ui.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
fail() { echo "FAIL: $*" >&2; exit 1; }

reset_path='/data/media-ui/password-resets.json'
compose_files=(
  docker-compose.yml
  docker-compose.registry.yml
  docker-compose.ghcr.yml
)

for file in "${compose_files[@]}"; do
  path="$ROOT/$file"
  grep -q "ADMIN_UI_PASSWORD_RESET_FILE: $reset_path" "$path" \
    || fail "$file missing ADMIN_UI_PASSWORD_RESET_FILE -> $reset_path"
  grep -q "MEDIA_UI_PASSWORD_RESET_FILE: $reset_path" "$path" \
    || fail "$file missing MEDIA_UI_PASSWORD_RESET_FILE -> $reset_path"
  grep -q 'media-ui-data:/data/media-ui' "$path" \
    || fail "$file missing shared media-ui-data volume mount"
done

echo "OK compose password-reset shared file wiring"
