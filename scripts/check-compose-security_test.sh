#!/usr/bin/env bash
# Offline: the registry compose install is the household security profile
# (ADR-0016) with per-module mesh identity (ADR-0017, roadmap T-M3-02g).
#
# docker-compose.registry.yml (and the root docker-compose.yml that includes it):
#   - no insecure transport anywhere: no MUXCORE_INSECURE_DISABLE_TLS, legacy
#     MUXCORE_DEV_TLS_SKIP / MUXCORE_GRPC_INSECURE / ADMIN_UI_INSECURE, no
#     network_mode: service:core (the dev-profile loopback sidecars, C-18);
#   - core: MUXCORE_PROFILE defaults to household, MUXCORE_ENROLL_SECRET is
#     required (${…:?}), the CA export dir is the mesh-ca volume, and the core
#     service name is in MUXCORE_TLS_SERVER_SANS;
#   - every module service (MUXCORE_MODULE_ID): household profile, a required
#     MUXCORE_BOOTSTRAP_TOKEN=${MUXCORE_ENROLL_TOKEN_<ID>:?…}, its own
#     `<service>-id` volume at MUXCORE_TLS_DIR (never shared), mesh-ca read-only
#     at the directory of MUXCORE_TLS_CA, MUXCORE_ENROLL_DNS_NAMES naming the
#     service; where the service name is not the module ID, the ID is a network
#     alias and core's MUXCORE_ENROLL_SAN_ALLOW allows the service name;
#   - fixture acquisition defaults (ADR-0008) are still present.
# docker-compose.dev.yml is the only compose file setting the insecure flag, and
# it sets the dev profile for core and every module service.
#
# Usage: check-compose-security_test.sh [REGISTRY_COMPOSE DEV_OVERRIDE ROOT_COMPOSE]
# Without arguments it checks the repository files and then runs negative
# fixtures (mutated copies must fail).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

command -v yq >/dev/null || { echo "skip: yq not found"; exit 0; }
command -v jq >/dev/null || { echo "skip: jq not found"; exit 0; }

yq_flags=()
if yq --help 2>&1 | grep -q -- '--yaml-fix-merge-anchor-to-spec'; then
  yq_flags+=(--yaml-fix-merge-anchor-to-spec=true)
fi

INSECURE_VARS='MUXCORE_INSECURE_DISABLE_TLS|MUXCORE_DEV_TLS_SKIP|MUXCORE_GRPC_INSECURE|ADMIN_UI_INSECURE'

# check <registry> <dev> <root> — prints FAIL lines, returns 1 on any failure.
check() {
  local reg="$1" dev="$2" rootc="$3" json dev_json fail=0 line

  # Raw text: the insecure variables may only appear in comments.
  for f in "$reg" "$rootc"; do
    if line="$(grep -nE "$INSECURE_VARS" "$f" | grep -vE '^[0-9]+:[[:space:]]*#')"; then
      echo "FAIL: $(basename "$f") sets an insecure flag: $line" >&2
      fail=1
    fi
  done
  if grep -nE 'network_mode:[[:space:]]*"?service:' "$reg" "$rootc" >/dev/null; then
    echo "FAIL: network_mode: service:… in the household compose (dev-profile sidecar workaround, C-18)" >&2
    fail=1
  fi

  json="$(yq "${yq_flags[@]}" -o=json 'explode(.)' "$reg")" || { echo "FAIL: cannot parse $reg" >&2; return 1; }
  local report
  report="$(jq -r --arg insecure "^(${INSECURE_VARS})$" '
    def env($s): (.services[$s].environment // {});
    def hh: (tostring | test("^household$|^\\$\\{MUXCORE_PROFILE:-household\\}$"));
    def required_var($v): test("^\\$\\{" + $v + ":\\?.+\\}$");
    def tokvar($id): "MUXCORE_ENROLL_TOKEN_" + ($id | ascii_upcase | gsub("[-.]"; "_"));
    . as $c
    | def mounts($s): (($c.services[$s].volumes // []) | map(tostring));
    ($c.services | to_entries) as $svcs
    | (env("core")) as $core
    | [
        # insecure keys anywhere (any service, even after anchor merges)
        ($svcs[] | .key as $s | (.value.environment // {}) | keys[] | select(test($insecure))
          | "\($s): sets \(.)"),
        # core
        (if ($c.services.core // null) == null then "core service missing" else empty end),
        (if ($core.MUXCORE_PROFILE // "" | hh) then empty
          else "core: MUXCORE_PROFILE must default to household (got \($core.MUXCORE_PROFILE // "unset"))" end),
        (if ($core.MUXCORE_ENROLL_SECRET // "" | tostring | required_var("MUXCORE_ENROLL_SECRET")) then empty
          else "core: MUXCORE_ENROLL_SECRET must be required (${MUXCORE_ENROLL_SECRET:?…})" end),
        (($core.MUXCORE_CA_EXPORT_DIR // "") as $d
          | if $d == "" then "core: MUXCORE_CA_EXPORT_DIR unset"
            elif (mounts("core") | any(. == "mesh-ca:\($d)" or . == "mesh-ca:\($d):rw")) then empty
            else "core: mesh-ca volume not mounted at MUXCORE_CA_EXPORT_DIR=\($d)" end),
        (if ($core.MUXCORE_TLS_SERVER_SANS // "" | tostring | split(",") | map(gsub("\\s"; "")) | index("core")) != null then empty
          else "core: MUXCORE_TLS_SERVER_SANS must include the service name core" end),
        # module services
        ($svcs[] | select(.value.environment.MUXCORE_MODULE_ID != null)
          | .key as $s | .value as $v | ($v.environment) as $e | ($e.MUXCORE_MODULE_ID | tostring) as $id
          | ($e.MUXCORE_TLS_DIR // "") as $dir
          | (($e.MUXCORE_TLS_CA // "") | sub("/[^/]+$"; "")) as $cadir
          | (if ($e.MUXCORE_PROFILE // "" | hh) then empty
              else "\($s): MUXCORE_PROFILE must default to household" end),
            (if ($e.MUXCORE_BOOTSTRAP_TOKEN // "" | tostring | required_var(tokvar($id))) then empty
              else "\($s): MUXCORE_BOOTSTRAP_TOKEN must be ${\(tokvar($id)):?…} (got \($e.MUXCORE_BOOTSTRAP_TOKEN // "unset"))" end),
            (if $dir == "" then "\($s): MUXCORE_TLS_DIR unset"
              elif (mounts($s) | any(. == "\($s)-id:\($dir)" or . == "\($s)-id:\($dir):rw")) then empty
              else "\($s): needs its own volume \($s)-id at MUXCORE_TLS_DIR=\($dir)" end),
            (if $cadir == "" then "\($s): MUXCORE_TLS_CA unset"
              elif (mounts($s) | any(. == "mesh-ca:\($cadir):ro")) then empty
              else "\($s): mesh-ca must be mounted read-only at \($cadir) (MUXCORE_TLS_CA)" end),
            (if ($e.MUXCORE_ENROLL_DNS_NAMES // "" | tostring | split(",") | index($s)) != null then empty
              else "\($s): MUXCORE_ENROLL_DNS_NAMES must include the service name" end),
            (if $s == $id then empty
              elif (($v.networks // {}) | if type == "object" then (.muxcore.aliases // []) else [] end | index($id)) == null
                then "\($s): network alias \($id) (the module ID) missing"
              elif ($core.MUXCORE_ENROLL_SAN_ALLOW // "" | tostring | split(",") | index($s)) == null
                then "core: MUXCORE_ENROLL_SAN_ALLOW must allow \($s) (service name of module \($id))"
              else empty end)
        ),
        # identity volumes are not shared between services
        ([$svcs[] | .key as $s | (.value.volumes // [])[] | tostring | split(":")[0]
           | select(endswith("-id")) | {v: ., s: $s}]
         | group_by(.v)[] | select(length > 1) | "volume \(.[0].v) shared by \(map(.s) | join(", "))"),
        # declared top-level volumes
        ([$svcs[] | select(.value.environment.MUXCORE_MODULE_ID != null) | "\(.key)-id"] + ["mesh-ca"]
         | .[] | select(. as $v | ($c.volumes // {}) | has($v) | not) | "volume \(.) not declared"),
        # fixture acquisition defaults (ADR-0008)
        (env("media-automation").DOWNLOADER_ENGINE // "" | tostring
          | if test(":-fixture\\}$") then empty else "media-automation: DOWNLOADER_ENGINE must default to fixture" end),
        (env("downloader-native-torrent").DOWNLOADER_ENGINE // "" | tostring
          | if test(":-fixture\\}$") then empty else "downloader-native-torrent: DOWNLOADER_ENGINE must default to fixture" end),
        (env("indexer-piratebay").INDEXER_FIXTURE // "" | tostring
          | if test(":-1\\}$") then empty else "indexer-piratebay: INDEXER_FIXTURE must default to 1" end),
        (env("downloader-qbittorrent").QBIT_FIXTURE // "" | tostring
          | if test(":-1\\}$") then empty else "downloader-qbittorrent: QBIT_FIXTURE must default to 1" end),
        (env("downloader-native-usenet").USENET_ENGINE // "" | tostring
          | if test(":-fixture\\}$") then empty else "downloader-native-usenet: USENET_ENGINE must default to fixture" end)
      ] | .[] | "FAIL: " + .' <<<"$json")" || { echo "FAIL: jq evaluation error" >&2; return 1; }
  if [[ -n "$report" ]]; then
    echo "$report" >&2
    fail=1
  fi

  # Dev override: the insecure dev profile for core and every module service.
  dev_json="$(yq "${yq_flags[@]}" -o=json 'explode(.)' "$dev")" || { echo "FAIL: cannot parse $dev" >&2; return 1; }
  report="$(jq -r --argjson reg "$json" '
    [ ($reg.services | to_entries[] | select(.key == "core" or .value.environment.MUXCORE_MODULE_ID != null) | .key) as $s
      | (.services[$s].environment // {}) as $e
      | if ($e.MUXCORE_PROFILE // "") == "dev" and (($e.MUXCORE_INSECURE_DISABLE_TLS // "") | tostring) == "true" then empty
        else "docker-compose.dev.yml: \($s) must set MUXCORE_PROFILE=dev and MUXCORE_INSECURE_DISABLE_TLS=true" end
    ] | .[] | "FAIL: " + .' <<<"$dev_json")"
  if [[ -n "$report" ]]; then
    echo "$report" >&2
    fail=1
  fi
  return "$fail"
}

REG="${1:-$ROOT/docker-compose.registry.yml}"
DEV="${2:-$ROOT/docker-compose.dev.yml}"
ROOTC="${3:-$ROOT/../docker-compose.yml}"
[[ -f "$ROOTC" ]] || ROOTC="$REG"  # standalone checkout of the mvp repo
for f in "$REG" "$DEV"; do [[ -f "$f" ]] || { echo "FAIL: missing $f" >&2; exit 1; }; done

check "$REG" "$DEV" "$ROOTC" || exit 1
n="$(yq "${yq_flags[@]}" -o=json 'explode(.)' "$REG" | jq '[.services[] | select(.environment.MUXCORE_MODULE_ID != null)] | length')"
echo "OK: registry compose is household with mesh identity for $n module services; insecure flags only in $(basename "$DEV")"

[[ $# -eq 0 ]] || exit 0

# ---- negative fixtures ----
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
expect_fail() { # expect_fail <name> <sed expression> [file: reg|dev]
  local name="$1" expr="$2" which="${3:-reg}" r="$tmp/reg.yml" d="$tmp/dev.yml"
  cp "$REG" "$r"
  cp "$DEV" "$d"
  if [[ "$which" == dev ]]; then sed -i -e "$expr" "$d"; else sed -i -e "$expr" "$r"; fi
  if cmp -s "$r" "$REG" && cmp -s "$d" "$DEV"; then
    echo "FAIL: negative fixture '$name' did not change the file (sed: $expr)" >&2
    exit 1
  fi
  if check "$r" "$d" "$r" 2>/dev/null; then
    echo "FAIL: negative fixture '$name' passed the check" >&2
    exit 1
  fi
  echo "ok: rejects $name"
}
expect_fail "insecure flag in a module" 's/^\(      MUXCORE_MODULE_ID: media-movies\)$/\1\n      MUXCORE_INSECURE_DISABLE_TLS: "true"/'
expect_fail "insecure flag in the module anchor" 's/^\(    MUXCORE_GRPC_ADDR: core:9090\)$/\1\n    MUXCORE_INSECURE_DISABLE_TLS: "true"/'
# shellcheck disable=SC2016 # sed expressions with literal ${...}
expect_fail "core dev profile" 's/^\(      MUXCORE_PROFILE: \)\${MUXCORE_PROFILE:-household}$/\1dev/'
# shellcheck disable=SC2016
expect_fail "optional enrollment secret" 's/\${MUXCORE_ENROLL_SECRET:?[^}]*}/${MUXCORE_ENROLL_SECRET:-}/'
expect_fail "missing bootstrap token" '/MUXCORE_ENROLL_TOKEN_MEDIA_SCANNER/d'
expect_fail "wrong token variable" 's/MUXCORE_ENROLL_TOKEN_MEDIA_MOVIES:/MUXCORE_ENROLL_TOKEN_MEDIA_TVSHOWS:/'
expect_fail "missing identity volume" '/- request-media-id:\/data\/mesh-id/d'
expect_fail "shared identity volume" 's/- media-tvshows-id:\/data\/mesh-id/- media-movies-id:\/data\/mesh-id/'
expect_fail "writable mesh CA" '0,/mesh-ca:\/data\/mesh-ca:ro/s//mesh-ca:\/data\/mesh-ca/'
expect_fail "sidecar in core netns" 's/^\(  call-policy-default:\)$/\1\n    network_mode: "service:core"/'
expect_fail "missing module-ID alias" 's/aliases: \[jellyfin\]/aliases: []/'
# shellcheck disable=SC2016
expect_fail "live downloader default" 's/DOWNLOADER_ENGINE: \${DOWNLOADER_ENGINE:-fixture}/DOWNLOADER_ENGINE: ${DOWNLOADER_ENGINE:-live}/'
expect_fail "dev override without insecure core" '0,/MUXCORE_INSECURE_DISABLE_TLS: "true"/s//MUXCORE_INSECURE_DISABLE_TLS: "false"/' dev
echo "OK: compose security negative fixtures"
