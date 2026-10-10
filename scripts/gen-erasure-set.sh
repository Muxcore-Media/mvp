#!/usr/bin/env bash
# User-erasure ledger consumer set for the household install (ADR-0035 §2.3,
# roadmap T-M4-07).
#
# auth-local serves the erasure ledger only to the modules on
# AUTH_ERASURE_CONSUMERS, and an erasure is "complete" only when every module on
# AUTH_ERASURE_REQUIRED acknowledged it. Both are derived here from
# household-manifest.yaml, never written by hand:
#
#   a module is in the set when its `state:` entry is `personal: true`, its
#   `erasure:` is `consumer` (the default), AND it is enabled.
#
# Enabled means required/recommended (always), or an `optional_env_gated` module
# whose gate variable (MVP_ENABLE_*) is 1/true, or whose compose service is in an
# active compose profile (COMPOSE_PROFILES, or --profile). The module id written
# is the service's MUXCORE_MODULE_ID in the compose file (the mesh certificate
# CN the provider checks); the manifest name when the service sets none.
#
# `erasure:` (state entries, manifest) is one of:
#   consumer   default for personal: true; runs an ADR-0035 reconciler
#   provider   the identity provider itself (auth-local, auth-oidc): it erases in
#              its own transaction and never acknowledges its own ledger
#   none       personal state with no ledger disposition (ADR-0035 §3, §7); the
#              entry's notes give the reason
# Listing a provider or a non-consumer would make every erasure incomplete for
# ever, which is why they are excluded by the manifest rather than by this script.
#
# Both variables carry the same list: every consumer is also required.
#
# Usage:
#   scripts/gen-erasure-set.sh [--manifest FILE] [--compose FILE] [--profile NAME]...
#       print AUTH_ERASURE_CONSUMERS=… and AUTH_ERASURE_REQUIRED=… to stdout
#   scripts/gen-erasure-set.sh --env-file FILE [...]
#       write the two variables into FILE (0600) between marker lines; every other
#       line is kept. Gate variables and COMPOSE_PROFILES not set in the
#       environment are read from FILE. Idempotent.
#   scripts/gen-erasure-set.sh --env-file FILE --check [...]
#       write nothing; exit 1 when FILE's values differ from the derived set
#       (a stale list after a module was enabled or disabled)
#
#   --manifest FILE  default: household-manifest.yaml
#   --compose FILE   default: docker-compose.registry.yml
#   --profile NAME   an active compose profile (repeatable), the same names given
#                    to `docker compose --profile`; COMPOSE_PROFILES is read too
#
# Needs mikefarah yq v4 and jq (as check-state-coverage.sh does).
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

MANIFEST="$ROOT/household-manifest.yaml"
COMPOSE="$ROOT/docker-compose.registry.yml"
ENV_FILE=""
CHECK=0
PROFILES=()

die() { echo "gen-erasure-set: $*" >&2; exit 1; }
usage() { sed -n '2,/^set -euo/p' "${BASH_SOURCE[0]}" | sed '$d; s/^# \{0,1\}//'; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --manifest) MANIFEST="${2:?--manifest needs a path}"; shift 2 ;;
    --manifest=*) MANIFEST="${1#*=}"; shift ;;
    --compose) COMPOSE="${2:?--compose needs a path}"; shift 2 ;;
    --compose=*) COMPOSE="${1#*=}"; shift ;;
    --env-file) ENV_FILE="${2:?--env-file needs a path}"; shift 2 ;;
    --env-file=*) ENV_FILE="${1#*=}"; shift ;;
    --profile) PROFILES+=("${2:?--profile needs a name}"); shift 2 ;;
    --profile=*) PROFILES+=("${1#*=}"); shift ;;
    --check) CHECK=1; shift ;;
    --print) shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; die "unknown argument: $1" ;;
  esac
done
[[ "$CHECK" -eq 0 || -n "$ENV_FILE" ]] || die "--check needs --env-file"
[[ -f "$MANIFEST" ]] || die "missing $MANIFEST"
[[ -f "$COMPOSE" ]] || die "missing $COMPOSE"
command -v jq >/dev/null || die "jq not found"
command -v yq >/dev/null || die "yq (mikefarah v4) not found"
yq --version 2>&1 | grep -q 'mikefarah' || die "yq must be mikefarah yq v4 (found: $(yq --version 2>&1 | head -1))"

yq_flags=()
if yq --help 2>&1 | grep -q -- '--yaml-fix-merge-anchor-to-spec'; then
  yq_flags+=(--yaml-fix-merge-anchor-to-spec=true)
fi
to_json() { yq "${yq_flags[@]}" -o=json 'explode(.)' "$1"; }

# Last NAME=value in the env file (optional export and quotes stripped).
file_var() { # file_var FILE NAME
  [[ -f "$1" ]] || return 0
  sed -n "s/^[[:space:]]*\\(export[[:space:]]\\{1,\\}\\)\\{0,1\\}$2=//p" "$1" | tail -n 1 \
    | sed "s/^[\"']//; s/[\"'][[:space:]]*\$//; s/[[:space:]]*\$//"
}
# NAME from the process environment, else from the env file.
lookup() { # lookup NAME
  if [[ -n "${!1:-}" ]]; then printf '%s' "${!1}"; return; fi
  [[ -n "$ENV_FILE" ]] && file_var "$ENV_FILE" "$1"
  return 0
}

manifest_json="$(to_json "$MANIFEST")" || die "cannot parse $MANIFEST"
compose_json="$(to_json "$COMPOSE")" || die "cannot parse $COMPOSE"

# Gate variables of the optional modules, and the active compose profiles.
gates='{}'
while IFS= read -r var; do
  [[ -n "$var" ]] || continue
  [[ "$var" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || die "invalid gate variable name in manifest: $var"
  gates="$(jq -c --arg k "$var" --arg v "$(lookup "$var")" '. + {($k): $v}' <<<"$gates")"
done < <(jq -r '(.optional_env_gated // [])[] | .env // empty' <<<"$manifest_json")

all_profiles=()
[[ ${#PROFILES[@]} -eq 0 ]] || all_profiles+=("${PROFILES[@]}")
IFS=',' read -r -a from_env <<<"$(lookup COMPOSE_PROFILES)"
for p in "${from_env[@]}"; do
  p="${p//[[:space:]]/}"
  [[ -z "$p" ]] || all_profiles+=("$p")
done
profiles_json="$(printf '%s\n' "${all_profiles[@]+"${all_profiles[@]}"}" | jq -R . | jq -sc 'map(select(. != ""))')"

set_value="$(jq -nr --argjson m "$manifest_json" --argjson c "$compose_json" \
  --argjson gates "$gates" --argjson profiles "$profiles_json" '
  def strip_comment: tostring | sub("\\s*#.*$"; "");
  def truthy: (. // "" | tostring | ascii_downcase) as $v | ($v == "1" or $v == "true");
  ($m.required // [] | map(strip_comment)) as $req
  | ($m.recommended // [] | map(strip_comment)) as $rec
  | ($m.optional_env_gated // []) as $opt
  | ($m.state // {}) as $state
  | ($c.services // {}) as $svcs
  | ($profiles | index("*")) as $all
  | def svc($n): ($state[$n].service // $n);
    def modid($n): ($svcs[svc($n)].environment.MUXCORE_MODULE_ID // $n | tostring);
    def listed($n): ((($req + $rec) | index($n)) != null) or ([$opt[] | select(.name == $n)] | length > 0);
    def enabled($n):
      ((($req + $rec) | index($n)) != null)
      or ([$opt[] | select(.name == $n) | (.env // "") as $e | ($gates[$e] // "") | truthy] | any)
      or (($svcs[svc($n)].profiles // []) as $p
          | ($p | length) > 0 and (($all != null) or ($p | any(. as $x | $profiles | index($x) != null))));
  [ $state | to_entries[]
    | select(.value.personal == true)
    | .key as $n
    | (.value.erasure // "consumer") as $kind
    | if (["consumer", "provider", "none"] | index($kind)) == null
      then error("state.\($n).erasure must be consumer, provider or none (got \($kind))")
      else . end
    | select($kind == "consumer")
    | if listed($n) | not
      then error("state.\($n) is personal but is in none of required, recommended or optional_env_gated: cannot tell whether it is enabled")
      else . end
    | select(enabled($n))
    | modid($n)
    | if test("^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$") then . else error("invalid module id: \(.)") end
  ] | unique | join(",")')" || die "cannot derive the erasure set from $MANIFEST and $COMPOSE"

[[ -n "$set_value" ]] || die "derived an empty erasure set: no enabled personal module in $MANIFEST"

if [[ -z "$ENV_FILE" ]]; then
  printf 'AUTH_ERASURE_CONSUMERS=%s\nAUTH_ERASURE_REQUIRED=%s\n' "$set_value" "$set_value"
  exit 0
fi

if [[ "$CHECK" -eq 1 ]]; then
  rc=0
  for var in AUTH_ERASURE_CONSUMERS AUTH_ERASURE_REQUIRED; do
    have="$(file_var "$ENV_FILE" "$var")"
    # Compare as sets: order does not matter.
    if [[ "$(tr ',' '\n' <<<"$have" | sed '/^$/d' | sort -u | paste -sd,)" != "$set_value" ]]; then
      echo "gen-erasure-set: $var in $ENV_FILE is stale: have '${have}', want '${set_value}' (run scripts/gen-erasure-set.sh --env-file $ENV_FILE)" >&2
      rc=1
    fi
  done
  [[ "$rc" -eq 0 ]] && echo "gen-erasure-set: $ENV_FILE is current ($set_value)"
  exit "$rc"
fi

BEGIN_MARK="# >>> erasure ledger set (scripts/gen-erasure-set.sh, ADR-0035) — do not edit, rerun the script"
END_MARK="# <<< erasure ledger set"
dir="$(dirname "$ENV_FILE")"
mkdir -p "$dir"
tmp="$(umask 077 && mktemp "$dir/.env.gen-erasure-set.XXXXXX")"
trap 'rm -f "$tmp"' EXIT
{
  if [[ -f "$ENV_FILE" ]]; then
    awk -v b="$BEGIN_MARK" -v e="$END_MARK" '
      $0 == b { skip = 1; next }
      $0 == e { skip = 0; next }
      skip { next }
      /^[[:space:]]*(export[[:space:]]+)?AUTH_ERASURE_(CONSUMERS|REQUIRED)=/ { next }
      { print }' "$ENV_FILE"
  fi
  echo "$BEGIN_MARK"
  echo "AUTH_ERASURE_CONSUMERS=$set_value"
  echo "AUTH_ERASURE_REQUIRED=$set_value"
  echo "$END_MARK"
} >"$tmp"
chmod 600 "$tmp"
mv -f "$tmp" "$ENV_FILE"
trap - EXIT
echo "gen-erasure-set: $ENV_FILE (0600): $set_value"
