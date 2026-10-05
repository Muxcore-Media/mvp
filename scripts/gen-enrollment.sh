#!/usr/bin/env bash
# Mesh enrollment secrets for the household compose install (ADR-0017, T-M3-02g).
#
# Writes into an env file (default _mvp/.env, mode 0600):
#   MUXCORE_ENROLL_SECRET            random, generated once and then kept
#   MUXCORE_ENROLL_TOKEN_<ID>        one single-use enrollment token per module ID
#
# Module IDs are the MUXCORE_MODULE_ID values of the compose file(s), plus --id.
# <ID> is the module ID upper-cased with '-' and '.' as '_' (media-movies ->
# MUXCORE_ENROLL_TOKEN_MEDIA_MOVIES). Tokens are computed exactly as core does
# (`muxcored enroll token <id>`):
#   mct_2_<id>_ + hex(HMAC-SHA256(key = MUXCORE_ENROLL_SECRET, msg = <id>))
#
# Idempotent: an existing MUXCORE_ENROLL_SECRET is kept, the token lines are
# recomputed from it, every other line of the file is left as it is. Running it
# again gives a byte-identical file. A token works once per module ID (core keeps
# a ledger in its CA dir); after losing a module's identity volume run
# `muxcored enroll reset <id>` in the core container.
#
# Usage:
#   scripts/gen-enrollment.sh [--env-file FILE] [--compose FILE]... [--id ID]... [--rotate]
#   scripts/gen-enrollment.sh --print-token ID [--env-file FILE]
#       print one token; the secret comes from MUXCORE_ENROLL_SECRET in the
#       environment, else from the env file
#
#   --env-file FILE  env file to update (default: _mvp/.env)
#   --compose FILE   compose file to read module IDs from (repeatable; default
#                    _mvp/docker-compose.registry.yml)
#   --id ID          extra module ID (repeatable)
#   --rotate         generate a new secret (already enrolled modules keep their
#                    certificates; only future enrollments use the new tokens)
#
# HMAC: python3 when available (the secret never appears in a command line),
# else openssl. GEN_ENROLLMENT_HMAC=python3|openssl forces one (tests).
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

ENV_FILE="$ROOT/.env"
COMPOSE_FILES=()
EXTRA_IDS=()
ROTATE=0
PRINT_ID=""

die() { echo "gen-enrollment: $*" >&2; exit 1; }
usage() { sed -n '2,/^set -euo/p' "${BASH_SOURCE[0]}" | sed '$d; s/^# \{0,1\}//'; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --env-file) ENV_FILE="${2:?--env-file needs a path}"; shift 2 ;;
    --env-file=*) ENV_FILE="${1#*=}"; shift ;;
    --compose) COMPOSE_FILES+=("${2:?--compose needs a path}"); shift 2 ;;
    --compose=*) COMPOSE_FILES+=("${1#*=}"); shift ;;
    --id) EXTRA_IDS+=("${2:?--id needs a module ID}"); shift 2 ;;
    --id=*) EXTRA_IDS+=("${1#*=}"); shift ;;
    --rotate) ROTATE=1; shift ;;
    --print-token) PRINT_ID="${2:?--print-token needs a module ID}"; shift 2 ;;
    --print-token=*) PRINT_ID="${1#*=}"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; die "unknown argument: $1" ;;
  esac
done
[[ ${#COMPOSE_FILES[@]} -gt 0 ]] || COMPOSE_FILES=("$ROOT/docker-compose.registry.yml")

# Same rules as core (internal/enroll): module IDs are safe as a CN and a token
# segment; the secret is at least 16 bytes. The secret is also restricted to
# characters that survive .env parsing and compose interpolation unquoted.
valid_id() { [[ "$1" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]]; }
valid_secret() { [[ ${#1} -ge 16 && "$1" =~ ^[A-Za-z0-9._~+/=-]+$ ]]; }

token_var() {
  local v="${1^^}"
  v="${v//-/_}"
  printf 'MUXCORE_ENROLL_TOKEN_%s' "${v//./_}"
}

hmac_hex() { # hmac_hex SECRET ID
  local impl="${GEN_ENROLLMENT_HMAC:-}"
  if [[ -z "$impl" ]]; then
    if command -v python3 >/dev/null 2>&1; then impl=python3; else impl=openssl; fi
  fi
  case "$impl" in
    python3)
      GEN_ENROLLMENT_KEY="$1" python3 -c '
import hashlib, hmac, os, sys
print(hmac.new(os.environ["GEN_ENROLLMENT_KEY"].encode(), sys.argv[1].encode(), hashlib.sha256).hexdigest())' "$2" ;;
    openssl)
      command -v openssl >/dev/null 2>&1 || die "neither python3 nor openssl available for HMAC-SHA256"
      printf '%s' "$2" | openssl dgst -sha256 -hmac "$1" -r | cut -d' ' -f1 ;;
    *) die "GEN_ENROLLMENT_HMAC must be python3 or openssl (got $impl)" ;;
  esac
}

token_for() { # token_for SECRET ID
  local mac
  mac="$(hmac_hex "$1" "$2")"
  [[ "$mac" =~ ^[0-9a-f]{64}$ ]] || die "HMAC for $2 failed"
  printf 'mct_2_%s_%s' "$2" "$mac"
}

# Last MUXCORE_ENROLL_SECRET=… value in the env file (optional quotes stripped).
secret_from_file() {
  [[ -f "$1" ]] || return 0
  sed -n 's/^[[:space:]]*\(export[[:space:]]\{1,\}\)\{0,1\}MUXCORE_ENROLL_SECRET=//p' "$1" | tail -n 1 \
    | sed "s/^[\"']//; s/[\"'][[:space:]]*\$//; s/[[:space:]]*\$//"
}

if [[ -n "$PRINT_ID" ]]; then
  valid_id "$PRINT_ID" || die "invalid module ID: $PRINT_ID"
  secret="${MUXCORE_ENROLL_SECRET:-$(secret_from_file "$ENV_FILE")}"
  [[ -n "$secret" ]] || die "MUXCORE_ENROLL_SECRET is not set and not in $ENV_FILE (run $0 first)"
  valid_secret "$secret" || die "MUXCORE_ENROLL_SECRET is invalid (>= 16 characters of [A-Za-z0-9._~+/=-])"
  token_for "$secret" "$PRINT_ID"
  echo
  exit 0
fi

# ---- module IDs ----
ids=()
for f in "${COMPOSE_FILES[@]}"; do
  [[ -f "$f" ]] || die "compose file not found: $f"
  while IFS= read -r id; do ids+=("$id"); done < <(
    sed -n 's/^[[:space:]]*MUXCORE_MODULE_ID:[[:space:]]*["'"'"']\{0,1\}\([^"'"'"'[:space:]#]*\).*/\1/p' "$f")
done
ids+=("${EXTRA_IDS[@]}")
mapfile -t ids < <(printf '%s\n' "${ids[@]}" | sed '/^$/d' | sort -u)
[[ ${#ids[@]} -gt 0 ]] || die "no MUXCORE_MODULE_ID found in ${COMPOSE_FILES[*]}"
declare -A var_owner=()
for id in "${ids[@]}"; do
  [[ "$id" != *'$'* ]] || die "MUXCORE_MODULE_ID must be a literal in compose (got $id)"
  valid_id "$id" || die "invalid module ID: $id"
  v="$(token_var "$id")"
  [[ -z "${var_owner[$v]:-}" ]] || die "module IDs ${var_owner[$v]} and $id map to the same variable $v"
  var_owner[$v]="$id"
done

# ---- secret ----
secret=""
state="kept"
if [[ "$ROTATE" -eq 0 ]]; then
  secret="$(secret_from_file "$ENV_FILE")"
fi
if [[ -z "$secret" ]]; then
  command -v openssl >/dev/null 2>&1 || die "openssl is required to generate MUXCORE_ENROLL_SECRET"
  secret="$(openssl rand -hex 32)"
  state="generated"
  [[ "$ROTATE" -eq 1 ]] && state="rotated"
fi
valid_secret "$secret" || die "MUXCORE_ENROLL_SECRET in $ENV_FILE is invalid (>= 16 characters of [A-Za-z0-9._~+/=-]); fix it or use --rotate"

# ---- write (atomic, 0600) ----
BEGIN_MARK="# >>> mesh enrollment (scripts/gen-enrollment.sh, ADR-0017) — do not edit, rerun the script"
END_MARK="# <<< mesh enrollment"
dir="$(dirname "$ENV_FILE")"
mkdir -p "$dir"
tmp="$(umask 077 && mktemp "$dir/.env.gen-enrollment.XXXXXX")"
trap 'rm -f "$tmp"' EXIT
{
  if [[ -f "$ENV_FILE" ]]; then
    awk -v b="$BEGIN_MARK" -v e="$END_MARK" '
      $0 == b { skip = 1; next }
      $0 == e { skip = 0; next }
      skip { next }
      /^[[:space:]]*(export[[:space:]]+)?MUXCORE_ENROLL_(SECRET|TOKEN_[A-Z0-9_]+)=/ { next }
      { print }' "$ENV_FILE"
  fi
  echo "$BEGIN_MARK"
  echo "MUXCORE_ENROLL_SECRET=$secret"
  for id in "${ids[@]}"; do
    printf '%s=%s\n' "$(token_var "$id")" "$(token_for "$secret" "$id")"
  done
  echo "$END_MARK"
} >"$tmp"
chmod 600 "$tmp"
mv -f "$tmp" "$ENV_FILE"
trap - EXIT
echo "gen-enrollment: $ENV_FILE (0600): secret $state, ${#ids[@]} module tokens"
