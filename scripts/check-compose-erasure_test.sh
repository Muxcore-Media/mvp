#!/usr/bin/env bash
# Offline: ADR-0035 user-erasure wiring in the compose stacks.
#
# Household (docker-compose.registry.yml):
#   - auth-local requires AUTH_ERASURE_CONSUMERS / AUTH_ERASURE_REQUIRED (no
#     static default: the lists come from scripts/gen-erasure-set.sh, which
#     derives them from household-manifest.yaml);
#   - media-ui is a ledger consumer under its mesh identity: module id media-ui
#     (the certificate CN the provider checks), a core address, a durable
#     userdata dir for erasure-applied.json, and NO plaintext/insecure switch;
#   - nothing in the household file sets an insecure alias (MUXCORE_DEV_TLS_SKIP,
#     MUXCORE_GRPC_INSECURE, MUXCORE_INSECURE_DISABLE_TLS), so the ledger is
#     never served over plaintext there.
# Dev override (docker-compose.dev.yml): auth-local and media-ui carry the
#   explicit insecure flag and the dev profile, as every other module does.
# Rendered (docker compose config), when docker compose is available: the
#   generated set reaches auth-local, and without it compose refuses to render.
# Every id the generator can emit is a MUXCORE_MODULE_ID in the compose file.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
REG="$ROOT/docker-compose.registry.yml"
DEV="$ROOT/docker-compose.dev.yml"
GEN="$SCRIPT_DIR/gen-erasure-set.sh"

command -v yq >/dev/null || { echo "skip: yq not found"; exit 0; }
command -v jq >/dev/null || { echo "skip: jq not found"; exit 0; }

yq_flags=()
if yq --help 2>&1 | grep -q -- '--yaml-fix-merge-anchor-to-spec'; then
  yq_flags+=(--yaml-fix-merge-anchor-to-spec=true)
fi
yjson() { yq "${yq_flags[@]}" -o=json 'explode(.)' "$1"; }

# A caller's profile selection must not change what is asserted.
unset COMPOSE_PROFILES
failed=0
fail() { echo "FAIL: $*" >&2; failed=1; }
ok() { echo "ok: $*"; }

reg_json="$(yjson "$REG")"
dev_json="$(yjson "$DEV")"

# ---- household file ----
for var in AUTH_ERASURE_CONSUMERS AUTH_ERASURE_REQUIRED; do
  got="$(jq -r --arg v "$var" '.services["auth-local"].environment[$v] // "unset"' <<<"$reg_json")"
  case "$got" in
    "\${$var:?"*"gen-erasure-set.sh"*) ok "auth-local $var is required and points at the generator" ;;
    *) fail "auth-local $var must be \${$var:?…gen-erasure-set.sh…}, got: $got" ;;
  esac
  if grep -nE "^[[:space:]]*$var:[[:space:]]*[\"']?[A-Za-z0-9]" "$REG"; then
    fail "$var carries a hand-written list in $REG"
  fi
done

mui="$(jq -c '.services["media-ui"].environment' <<<"$reg_json")"
# assert_eq <description> <want> <got> [fail-hint]
assert_eq() {
  if [[ "$2" == "$3" ]]; then ok "$1"; else fail "$1: want '$2', got '$3' ${4:-}"; fi
}
assert_eq "media-ui module id is media-ui (the certificate CN)" media-ui "$(jq -r '.MUXCORE_MODULE_ID' <<<"$mui")" \
  "(the ledger allowlist and acknowledgements use the certificate CN)"
assert_eq "media-ui reaches core (MUXCORE_GRPC_ADDR)" core:9090 "$(jq -r '.MUXCORE_GRPC_ADDR // ""' <<<"$mui")" \
  "(the household profile refuses to start without it)"
assert_eq "media-ui runs the household profile" "\${MUXCORE_PROFILE:-household}" "$(jq -r '.MUXCORE_PROFILE // ""' <<<"$mui")"
assert_eq "media-ui keeps erasure-applied.json in its durable data volume" /data/media-ui \
  "$(jq -r '.MEDIA_UI_USERDATA_DIR // ""' <<<"$mui")"
if jq -e '.services | to_entries[] | .value.environment // {} | keys[] | select(. == "MUXCORE_DEV_TLS_SKIP" or . == "MUXCORE_GRPC_INSECURE" or . == "MUXCORE_INSECURE_DISABLE_TLS")' <<<"$reg_json" >/dev/null; then
  fail "the household compose file sets an insecure TLS switch; the ledger would be served over plaintext"
else
  ok "no insecure TLS switch anywhere in the household compose file"
fi

# ---- dev override ----
for svc in auth-local media-ui; do
  v="$(jq -r --arg s "$svc" '.services[$s].environment.MUXCORE_INSECURE_DISABLE_TLS // "unset"' <<<"$dev_json")"
  p="$(jq -r --arg s "$svc" '.services[$s].environment.MUXCORE_PROFILE // "unset"' <<<"$dev_json")"
  if [[ "$v" == "true" && "$p" == "dev" ]]; then
    ok "dev override: $svc is explicitly insecure dev"
  else
    fail "dev override: $svc must set MUXCORE_INSECURE_DISABLE_TLS=true and MUXCORE_PROFILE=dev (got $v / $p)"
  fi
done

# ---- every emitted module id is a compose MUXCORE_MODULE_ID ----
ids="$(jq -r '[.services[] | .environment.MUXCORE_MODULE_ID // empty] | unique | .[]' <<<"$reg_json")"
all="$(bash "$GEN" --compose "$REG" --profile '*' | sed -n 's/^AUTH_ERASURE_CONSUMERS=//p' | tr ',' '\n')"
[[ -n "$all" ]] || fail "generator emitted nothing for every profile"
for id in $all; do
  if grep -qx "$id" <<<"$ids"; then
    ok "generated id $id is a compose module id"
  else
    fail "generated id $id is not any service's MUXCORE_MODULE_ID in $REG"
  fi
done

# ---- rendered ----
if docker compose version >/dev/null 2>&1; then
  work="$(mktemp -d)"
  trap 'rm -rf "$work"' EXIT
  envf="$work/render.env"
  grep -ohE '\$\{[A-Z0-9_]+:\?' "$REG" "$DEV" | sed -E 's/^\$\{([A-Z0-9_]+):\?$/\1=render-fixture/' | sort -u \
    | grep -vE '^AUTH_ERASURE_' >"$envf"
  render() { docker compose --env-file "$envf" -p erasure-compose-check "$@" config --format json; }

  if out="$(render -f "$REG" 2>&1)"; then
    fail "compose rendered without the generated erasure set (it must fail closed)"
  elif grep -q "gen-erasure-set.sh" <<<"$out"; then
    ok "rendered: compose refuses to start without the generated erasure set"
  else
    fail "compose failed without saying how to fix it: $out"
  fi

  bash "$GEN" --compose "$REG" --env-file "$envf" --profile playback-guard >/dev/null
  rendered="$(render -f "$REG" --profile playback-guard)" \
    || { fail "compose cannot render with the generated set"; rendered='{}'; }
  want="admin-ui,media-ui,playback-guard,request-media,userdata-local"
  for var in AUTH_ERASURE_CONSUMERS AUTH_ERASURE_REQUIRED; do
    got="$(jq -r --arg v "$var" '.services["auth-local"].environment[$v] // "unset"' <<<"$rendered")"
    assert_eq "rendered: auth-local $var" "$want" "$got"
  done
  # media-ui is on the list under the id its certificate carries.
  mid="$(jq -r '.services["media-ui"].environment.MUXCORE_MODULE_ID' <<<"$rendered")"
  if [[ ",$want," == *",$mid,"* ]]; then ok "rendered: media-ui ($mid) is on the ledger allowlist"; else fail "media-ui ($mid) is not on the allowlist"; fi
  # The dev overlay renders the same set and stays explicitly insecure.
  devr="$(render -f "$REG" -f "$DEV" --profile playback-guard)" \
    || { fail "compose cannot render registry+dev"; devr='{}'; }
  assert_eq "rendered registry+dev: media-ui is explicitly insecure" true \
    "$(jq -r '.services["media-ui"].environment.MUXCORE_INSECURE_DISABLE_TLS // "unset"' <<<"$devr")"
else
  echo "skip: docker compose not available (rendered checks)"
fi

if ((failed)); then
  exit 1
fi
echo "check-compose-erasure_test: all passed"
