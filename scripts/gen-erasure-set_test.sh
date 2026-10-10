#!/usr/bin/env bash
# Offline tests for gen-erasure-set.sh (ADR-0035 §2.3): the ledger consumer set
# is derived from the household manifest (personal: true AND enabled), never
# hand-written. A disabled personal module is omitted, an enabled one included.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
GEN="$SCRIPT_DIR/gen-erasure-set.sh"
failed=0

command -v yq >/dev/null || { echo "skip: yq not found"; exit 0; }
command -v jq >/dev/null || { echo "skip: jq not found"; exit 0; }

fail() { echo "FAIL: $*" >&2; failed=1; }
ok() { echo "ok: $*"; }

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# Gate variables and profiles of the calling shell must not leak into a case.
clean_env() {
  local -a unset=(-u COMPOSE_PROFILES)
  local v
  while IFS= read -r v; do
    [[ -z "$v" ]] || unset+=(-u "$v")
  done < <(env | sed -n 's/^\(MVP_ENABLE_[A-Za-z0-9_]*\)=.*/\1/p')
  env "${unset[@]}" "$@"
}

# consumers [VAR=value ...] -- [gen args...]: sorted AUTH_ERASURE_CONSUMERS list.
run_gen() {
  local -a envs=() args=()
  while [[ $# -gt 0 && "$1" != "--" ]]; do envs+=("$1"); shift; done
  [[ "${1:-}" == "--" ]] && shift
  args=("$@")
  clean_env "${envs[@]+"${envs[@]}"}" bash "$GEN" "${args[@]+"${args[@]}"}"
}
consumers() { run_gen "$@" | sed -n 's/^AUTH_ERASURE_CONSUMERS=//p'; }
required() { run_gen "$@" | sed -n 's/^AUTH_ERASURE_REQUIRED=//p'; }

expect_eq() { # expect_eq name want got
  if [[ "$2" == "$3" ]]; then ok "$1"; else fail "$1: want '$2' got '$3'"; fi
}
expect_has() { # expect_has name list member
  if [[ ",$2," == *",$3,"* ]]; then ok "$1"; else fail "$1: '$3' missing from '$2'"; fi
}
expect_lacks() {
  if [[ ",$2," != *",$3,"* ]]; then ok "$1"; else fail "$1: '$3' must not be in '$2'"; fi
}

BASE="admin-ui,media-ui,request-media,userdata-local"

# ---- the repo manifest and registry compose ----
expect_eq "repo: default set is the always-on personal consumers" "$BASE" "$(consumers --)"
expect_eq "repo: AUTH_ERASURE_REQUIRED equals AUTH_ERASURE_CONSUMERS" "$(consumers --)" "$(required --)"

got="$(consumers --)"
expect_lacks "repo: disabled playback-guard is omitted" "$got" playback-guard
expect_lacks "repo: disabled playback-monitor is omitted" "$got" playback-monitor
expect_lacks "repo: disabled jellyfin bridge is omitted" "$got" jellyfin

got="$(consumers MVP_ENABLE_PLAYBACK_GUARD=1 --)"
expect_has "repo: playback-guard enabled by its gate is included" "$got" playback-guard
expect_has "repo: the always-on consumers stay" "$got" userdata-local
got="$(consumers MVP_ENABLE_PLAYBACK_GUARD=true --)"
expect_has "repo: gate value true enables" "$got" playback-guard
got="$(consumers MVP_ENABLE_PLAYBACK_GUARD=0 --)"
expect_lacks "repo: gate value 0 stays disabled" "$got" playback-guard
got="$(consumers --  --profile playback-monitor)"
expect_has "repo: an active compose profile enables" "$got" playback-monitor
got="$(consumers COMPOSE_PROFILES=playback-guard,jellyfin --)"
expect_has "repo: COMPOSE_PROFILES enables (guard)" "$got" playback-guard
expect_has "repo: COMPOSE_PROFILES enables, id is the compose MUXCORE_MODULE_ID (jellyfin-bridge -> jellyfin)" "$got" jellyfin
expect_lacks "repo: a service name is not a module id" "$got" jellyfin-bridge

# Providers and modules with no ledger disposition are never listed, enabled or not.
got="$(consumers MVP_ENABLE_AUTH_OIDC=1 MVP_ENABLE_PLEX=1 MVP_ENABLE_EMBY=1 COMPOSE_PROFILES='*' --)"
for m in auth-local auth-oidc database-sqlite plex emby; do
  expect_lacks "repo: $m (erasure: provider|none) is never listed" "$got" "$m"
done
expect_has "repo: everything personal and enabled under profile *" "$got" playback-guard
# A non-personal module that is enabled is not a ledger consumer.
got="$(consumers MVP_ENABLE_MEDIA_MUSIC=1 MVP_ENABLE_MEDIA_TRANSCODER=1 --)"
expect_lacks "repo: an enabled non-personal module is not listed" "$got" media-music
expect_lacks "repo: an enabled non-personal module is not listed (transcoder)" "$got" media-transcoder

# ---- a fixture manifest: the rules without the repo's content ----
FM="$work/manifest.yaml"
FC="$work/compose.yml"
cat >"$FM" <<'YAML'
version: "1"
core_tag: v0.0.0
required:
  - idp
  - always-on   # personal, always enabled
  - plain       # not personal
recommended:
  - rec-personal
optional_env_gated:
  - name: opt-on
    env: MVP_ENABLE_OPT_ON
  - name: opt-off
    env: MVP_ENABLE_OPT_OFF
  - name: opt-profile
    env: MVP_ENABLE_OPT_PROFILE
  - name: opt-none
    env: MVP_ENABLE_OPT_NONE
  - name: opt-plain
    env: MVP_ENABLE_OPT_PLAIN
state:
  idp: {volume: v1, mount: /d, kind: sqlite, personal: true, erasure: provider}
  always-on: {volume: v2, mount: /d, kind: sqlite, personal: true}
  plain: {volume: v3, mount: /d, kind: sqlite, personal: false}
  rec-personal: {volume: v4, mount: /d, kind: files, personal: true, erasure: consumer}
  opt-on: {volume: v5, mount: /d, kind: files, personal: true}
  opt-off: {volume: v6, mount: /d, kind: files, personal: true}
  opt-profile: {service: opt-profile-svc, volume: v7, mount: /d, kind: files, personal: true}
  opt-none: {volume: v8, mount: /d, kind: files, personal: true, erasure: none}
  opt-plain: {volume: v9, mount: /d, kind: files, personal: false}
YAML
cat >"$FC" <<'YAML'
services:
  idp: {environment: {MUXCORE_MODULE_ID: idp}}
  always-on: {environment: {MUXCORE_MODULE_ID: always-on}}
  rec-personal: {environment: {MUXCORE_MODULE_ID: rec-personal}}
  opt-on: {environment: {MUXCORE_MODULE_ID: opt-on}}
  opt-off: {environment: {MUXCORE_MODULE_ID: opt-off}}
  opt-profile-svc:
    profiles: [special]
    environment: {MUXCORE_MODULE_ID: opt-profile-id}
  opt-none: {environment: {MUXCORE_MODULE_ID: opt-none}}
YAML
F=(--manifest "$FM" --compose "$FC")
expect_eq "fixture: only required/recommended personal consumers by default" "always-on,rec-personal" "$(consumers -- "${F[@]}")"
expect_eq "fixture: an enabled optional personal module is included" "always-on,opt-on,rec-personal" \
  "$(consumers MVP_ENABLE_OPT_ON=1 -- "${F[@]}")"
expect_eq "fixture: a disabled optional personal module is omitted" "always-on,opt-on,rec-personal" \
  "$(consumers MVP_ENABLE_OPT_ON=1 MVP_ENABLE_OPT_OFF=0 -- "${F[@]}")"
expect_eq "fixture: a profile enables the service and the module id comes from compose" "always-on,opt-profile-id,rec-personal" \
  "$(consumers -- "${F[@]}" --profile special)"
expect_eq "fixture: erasure: none stays out when enabled; non-personal stays out" "always-on,rec-personal" \
  "$(consumers MVP_ENABLE_OPT_NONE=1 MVP_ENABLE_OPT_PLAIN=1 -- "${F[@]}")"

# ---- configuration errors fail loudly ----
bad() { # bad name pattern manifest-text
  local name="$1" pattern="$2" out rc=0
  printf '%s\n' "$3" >"$work/bad.yaml"
  out="$(clean_env bash "$GEN" --manifest "$work/bad.yaml" --compose "$FC" 2>&1)" || rc=$?
  if [[ "$rc" -ne 0 ]] && grep -qE "$pattern" <<<"$out"; then ok "$name"; else fail "$name: rc=$rc out=$out"; fi
}
bad "unknown erasure value" "erasure must be consumer, provider or none" 'required: [a]
state:
  a: {volume: v, mount: /d, kind: files, personal: true, erasure: maybe}'
bad "personal module that is neither required nor optional" "none of required, recommended or optional_env_gated" 'required: [a]
state:
  a: {volume: v, mount: /d, kind: files, personal: true}
  b: {volume: w, mount: /d, kind: files, personal: true}'
bad "empty set" "empty erasure set" 'required: [a]
state:
  a: {volume: v, mount: /d, kind: files, personal: false}'

# ---- env file: written once, idempotent, checkable ----
ENVF="$work/.env"
printf 'MUXCORE_ENROLL_SECRET=keepme\nAUTH_ERASURE_CONSUMERS=stale-hand-edit\nMVP_ENABLE_PLAYBACK_GUARD=1\n' >"$ENVF"
clean_env bash "$GEN" --env-file "$ENVF" >/dev/null
if grep -qx 'AUTH_ERASURE_CONSUMERS=admin-ui,media-ui,playback-guard,request-media,userdata-local' "$ENVF" \
  && grep -qx 'AUTH_ERASURE_REQUIRED=admin-ui,media-ui,playback-guard,request-media,userdata-local' "$ENVF"; then
  ok "env file: gate read from the env file, both variables written"
else
  fail "env file content: $(cat "$ENVF")"
fi
if grep -qx 'MUXCORE_ENROLL_SECRET=keepme' "$ENVF"; then ok "env file: unrelated lines kept"; else fail "env file lost a line"; fi
if [[ "$(grep -c '^AUTH_ERASURE_CONSUMERS=' "$ENVF")" == 1 ]]; then ok "env file: the stale hand-written line is gone"; else fail "stale line kept"; fi
if [[ "$(stat -c %a "$ENVF")" == 600 ]]; then ok "env file: mode 0600"; else fail "mode $(stat -c %a "$ENVF")"; fi
cp "$ENVF" "$work/first"
clean_env bash "$GEN" --env-file "$ENVF" >/dev/null
if cmp -s "$ENVF" "$work/first"; then ok "env file: a second run is byte-identical"; else fail "second run changed the file"; fi
if clean_env bash "$GEN" --env-file "$ENVF" --check >/dev/null; then ok "--check: current file passes"; else fail "--check failed on a current file"; fi
printf 'COMPOSE_PROFILES=jellyfin\n' >>"$ENVF"
if clean_env bash "$GEN" --env-file "$ENVF" --check >/dev/null 2>"$work/check.err"; then
  fail "--check passed although a profile was enabled after the file was written"
elif grep -q "stale" "$work/check.err"; then
  ok "--check: a module enabled after generation is reported as stale"
else
  fail "--check failed without saying why: $(cat "$work/check.err")"
fi
clean_env bash "$GEN" --env-file "$ENVF" >/dev/null
if grep -qx 'AUTH_ERASURE_REQUIRED=admin-ui,jellyfin,media-ui,playback-guard,request-media,userdata-local' "$ENVF"; then
  ok "env file: COMPOSE_PROFILES read from the env file"
else
  fail "COMPOSE_PROFILES in the env file ignored: $(cat "$ENVF")"
fi

# ---- docker-compose.registry.yml: the variables are required and not defaulted ----
REG="$ROOT/docker-compose.registry.yml"
for var in AUTH_ERASURE_CONSUMERS AUTH_ERASURE_REQUIRED; do
  got="$(yq -o=json 'explode(.)' "$REG" | jq -r --arg v "$var" '.services["auth-local"].environment[$v] // "unset"')"
  case "$got" in
    "\${$var:?"*"scripts/gen-erasure-set.sh"*) ok "registry compose: auth-local $var is required (no static default)" ;;
    *) fail "registry compose: auth-local $var must be \${$var:?...gen-erasure-set.sh...}, got: $got" ;;
  esac
done

if ((failed)); then
  exit 1
fi
echo "gen-erasure-set_test: all passed"
