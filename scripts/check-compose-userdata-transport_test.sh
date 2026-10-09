#!/usr/bin/env bash
# Offline: userdata-local HTTP transport wiring (ADR-0033 slice S9d) in every
# stack, plus negative fixtures (mutated copies must fail).
#
# Household (docker-compose.registry.yml):
#   - media-ui USERDATA_LOCAL_URL and admin-ui ADMIN_UI_USERDATA_URL are
#     https://userdata-local:9672 (service-name SAN); no plaintext
#     http://userdata-local value anywhere;
#   - userdata-local publishes no host port, its readiness is the packaged
#     /app/userdata-health probe (no curl/wget probe), it binds :9672 and its
#     module ID / enrollment DNS name is userdata-local;
#   - jellyfin-bridge: USERDATA_SYNC is "0" and no provider URL is wired.
# Dev override (docker-compose.dev.yml): explicit plaintext http:// for both
#   clients and a dev-only host port. Reference docker-compose.yml (insecure
#   dev): http:// BFF URL, the packaged probe, jellyfin sync off.
# run-host.sh: https://127.0.0.1:9672 unless insecure dev, userdata-health is
#   built and used by `status`, jellyfin USERDATA_SYNC=0 without a URL.
# With docker compose / helm / kubeconform available, the merged compose stacks
# and the Helm chart are rendered and checked as well (Helm with TLS on must
# refuse media.enabled; kustomize overlays wire no userdata-local).
#
# Usage: check-compose-userdata-transport_test.sh [REGISTRY DEV REFERENCE RUN_HOST]
# shellcheck disable=SC2016 # literal $ in the expected run-host.sh/compose text
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

command -v yq >/dev/null || { echo "skip: yq not found"; exit 0; }
command -v jq >/dev/null || { echo "skip: jq not found"; exit 0; }

yq_flags=()
if yq --help 2>&1 | grep -q -- '--yaml-fix-merge-anchor-to-spec'; then
  yq_flags+=(--yaml-fix-merge-anchor-to-spec=true)
fi
yjson() { yq "${yq_flags[@]}" -o=json 'explode(.)' "$1"; }

HTTPS_URL='https://userdata-local:9672'
HTTP_URL='http://userdata-local:9672'
PROBE='["CMD","/app/userdata-health"]'

# household_report <json> — FAIL lines for a household (registry) stack.
household_report() {
  jq -r --arg https "$HTTPS_URL" --argjson probe "$PROBE" '
    def env($s): (.services[$s].environment // {});
    (.services["userdata-local"] // {}) as $ud
    | (.services["jellyfin-bridge"] // .services["jellyfin"] // {}) as $jf
    | [
        (if env("media-ui").USERDATA_LOCAL_URL == $https then empty
          else "media-ui: USERDATA_LOCAL_URL must be \($https) (got \(env("media-ui").USERDATA_LOCAL_URL // "unset"))" end),
        (if env("admin-ui").ADMIN_UI_USERDATA_URL == $https then empty
          else "admin-ui: ADMIN_UI_USERDATA_URL must be \($https) (got \(env("admin-ui").ADMIN_UI_USERDATA_URL // "unset"))" end),
        (.services | to_entries[] | .key as $s | (.value.environment // {}) | to_entries[]
          | select(.value | tostring | test("http://userdata-local"))
          | "\($s): \(.key) points at plaintext userdata-local (\(.value))"),
        (if ($ud.ports // []) == [] then empty else "userdata-local: publishes a host port in the household stack" end),
        (if ($ud.healthcheck.test // null) == $probe then empty
          else "userdata-local: healthcheck must be \($probe | tostring) (got \($ud.healthcheck.test // "none" | tostring))" end),
        (.services | to_entries[] | .key as $s | (.value.healthcheck.test // []) | tostring
          | select(test("curl|wget") and test("9672|userdata-local"))
          | "\($s): bare HTTP userdata probe"),
        (if ($ud.environment.USERDATA_LOCAL_HTTP_ADDR // "") == ":9672" then empty
          else "userdata-local: USERDATA_LOCAL_HTTP_ADDR must be :9672" end),
        (if ($ud.environment.MUXCORE_MODULE_ID // "") == "userdata-local" then empty
          else "userdata-local: MUXCORE_MODULE_ID must be userdata-local (certificate CN/SAN)" end),
        (if ($ud.environment.MUXCORE_ENROLL_DNS_NAMES // "" | tostring | split(",") | index("userdata-local")) != null then empty
          else "userdata-local: MUXCORE_ENROLL_DNS_NAMES must include userdata-local" end),
        (if ($jf.environment // {}) == {} then "jellyfin bridge service missing"
          elif ($jf.environment.USERDATA_SYNC | tostring) != "0" then "jellyfin bridge: USERDATA_SYNC must be \"0\" (got \($jf.environment.USERDATA_SYNC // "unset"))"
          elif ($jf.environment | has("USERDATA_LOCAL_URL")) then "jellyfin bridge: no userdata provider URL may be wired"
          else empty end)
      ] | .[] | "FAIL: " + .'
}

# dev_report <json> <label> — FAIL lines for an explicit insecure dev stack
# (merged registry+dev render, or the reference docker-compose.yml).
dev_report() {
  jq -r --arg http "$HTTP_URL" --argjson probe "$PROBE" --arg label "$2" '
    def env($s): (.services[$s].environment // {});
    (.services["jellyfin-bridge"] // .services["jellyfin"] // {}) as $jf
    | [
        (if env("media-ui").USERDATA_LOCAL_URL == $http then empty
          else "\($label) media-ui: USERDATA_LOCAL_URL must be \($http) in explicit insecure dev" end),
        (if (env("admin-ui") | has("ADMIN_UI_USERDATA_URL") | not) or env("admin-ui").ADMIN_UI_USERDATA_URL == $http then empty
          else "\($label) admin-ui: ADMIN_UI_USERDATA_URL must be \($http) in explicit insecure dev" end),
        (if (.services["userdata-local"].healthcheck.test // null) == $probe then empty
          else "\($label) userdata-local: healthcheck must be the packaged probe" end),
        (if ($jf.environment.USERDATA_SYNC | tostring) == "0" and ($jf.environment | has("USERDATA_LOCAL_URL") | not) then empty
          else "\($label) jellyfin: USERDATA_SYNC must be \"0\" with no provider URL" end)
      ] | .[] | "FAIL: " + .' <<<"$1"
}

# check <registry> <dev> <reference> <run-host> — returns 1 on any failure.
check() {
  local reg="$1" dev="$2" ref="$3" runhost="$4" fail=0 report reg_json dev_json ref_json

  reg_json="$(yjson "$reg")" || { echo "FAIL: cannot parse $reg" >&2; return 1; }
  report="$(household_report <<<"$reg_json")"
  [[ -z "$report" ]] || { echo "$report" >&2; fail=1; }

  # Dev override, as written (the merged render below checks the result).
  dev_json="$(yjson "$dev")" || { echo "FAIL: cannot parse $dev" >&2; return 1; }
  report="$(jq -r --arg http "$HTTP_URL" '
    [ (if .services["media-ui"].environment.USERDATA_LOCAL_URL == $http then empty
        else "docker-compose.dev.yml media-ui: must override USERDATA_LOCAL_URL to \($http)" end),
      (if .services["admin-ui"].environment.ADMIN_UI_USERDATA_URL == $http then empty
        else "docker-compose.dev.yml admin-ui: must override ADMIN_UI_USERDATA_URL to \($http)" end),
      (if ((.services["userdata-local"].ports // []) | map(tostring) | index("${USERDATA_LOCAL_PORT:-9672}:9672")) != null then empty
        else "docker-compose.dev.yml userdata-local: dev-only plaintext port missing" end),
      (if ((.services["jellyfin-bridge"].environment // {}) | (.USERDATA_SYNC // "0" | tostring) == "0" and (has("USERDATA_LOCAL_URL") | not)) then empty
        else "docker-compose.dev.yml jellyfin-bridge: must not re-enable userdata sync" end)
    ] | .[] | "FAIL: " + .' <<<"$dev_json")"
  [[ -z "$report" ]] || { echo "$report" >&2; fail=1; }

  ref_json="$(yjson "$ref")" || { echo "FAIL: cannot parse $ref" >&2; return 1; }
  report="$(dev_report "$ref_json" "docker-compose.yml")"
  [[ -z "$report" ]] || { echo "$report" >&2; fail=1; }

  # run-host.sh (text): secure default origin, insecure-only plaintext, probe, sync off.
  local rh jf_block
  rh="$(cat "$runhost")"
  grep -qx '_userdata_http_url=https://127.0.0.1:9672' <<<"$rh" \
    || { echo "FAIL: run-host.sh: default userdata origin must be https://127.0.0.1:9672" >&2; fail=1; }
  if [[ "$(grep -c 'http://127\.0\.0\.1:9672' <<<"$rh")" != 1 ]] || ! grep -qx '  _userdata_http_url=http://127.0.0.1:9672' <<<"$rh"; then
    echo "FAIL: run-host.sh: plaintext http://127.0.0.1:9672 may only be the insecure-dev origin" >&2
    fail=1
  fi
  for want in 'ADMIN_UI_USERDATA_URL="${ADMIN_UI_USERDATA_URL:-$_userdata_http_url}"' \
    'USERDATA_LOCAL_URL="${USERDATA_LOCAL_URL:-$_userdata_http_url}"' \
    'go build -o "$BIN/userdata-health" ./cmd/userdata-health' \
    '"$BIN/userdata-health" >/dev/null'; do
    grep -qF -- "$want" <<<"$rh" || { echo "FAIL: run-host.sh: missing $want" >&2; fail=1; }
  done
  jf_block="$(awk '/maybe_start jellyfin env/{p=1} p{print} p && /"\$BIN\/jellyfin"/{exit}' <<<"$rh")"
  grep -qE '^\s*USERDATA_SYNC=0 \\$' <<<"$jf_block" \
    || { echo "FAIL: run-host.sh: jellyfin must run with USERDATA_SYNC=0" >&2; fail=1; }
  grep -q 'USERDATA_LOCAL_URL' <<<"$jf_block" \
    && { echo "FAIL: run-host.sh: jellyfin must not get a userdata provider URL" >&2; fail=1; }
  return "$fail"
}

REG="${1:-$ROOT/docker-compose.registry.yml}"
DEV="${2:-$ROOT/docker-compose.dev.yml}"
REF="${3:-$ROOT/docker-compose.yml}"
RUNHOST="${4:-$ROOT/run-host.sh}"
for f in "$REG" "$DEV" "$REF" "$RUNHOST"; do [[ -f "$f" ]] || { echo "FAIL: missing $f" >&2; exit 1; }; done

check "$REG" "$DEV" "$REF" "$RUNHOST" || exit 1
echo "OK userdata transport wiring (static)"
[[ $# -eq 0 ]] || exit 0  # fixture mode: static checks only

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# ---- rendered compose stacks (docker compose config) ----
if docker compose version >/dev/null 2>&1; then
  envf="$work/render.env"
  grep -ohE '\$\{[A-Z0-9_]+:\?' "$REG" "$DEV" "$REF" | sed -E 's/^\$\{([A-Z0-9_]+):\?$/\1=render-fixture/' | sort -u >"$envf"
  render() { docker compose --env-file "$envf" -p userdata-transport-check "$@" --profile '*' config --format json; }
  hh="$(render -f "$REG")" || { echo "FAIL: docker compose cannot render $REG" >&2; exit 1; }
  report="$(household_report <<<"$hh")"
  [[ -z "$report" ]] || { echo "$report" >&2; exit 1; }
  devm="$(render -f "$REG" -f "$DEV")" || { echo "FAIL: docker compose cannot render $REG + $DEV" >&2; exit 1; }
  report="$(dev_report "$devm" "registry+dev")"
  [[ -z "$report" ]] || { echo "$report" >&2; exit 1; }
  jq -e '.services["userdata-local"].ports | map(.target) | index(9672) != null' <<<"$devm" >/dev/null \
    || { echo "FAIL: registry+dev: dev-only userdata port not rendered" >&2; exit 1; }
  refm="$(render -f "$REF")" || { echo "FAIL: docker compose cannot render $REF" >&2; exit 1; }
  report="$(dev_report "$refm" "docker-compose.yml (rendered)")"
  [[ -z "$report" ]] || { echo "$report" >&2; exit 1; }
  echo "OK rendered compose: household https + probe + no port; registry+dev and reference plaintext dev"
else
  echo "skip: docker compose not available (rendered compose checks)"
fi

# ---- Helm (explicit insecure dev only) and kustomize (unsupported) ----
CHART="$ROOT/deploy/helm/muxcore"
if command -v helm >/dev/null 2>&1; then
  helm template fixture "$CHART" --set media.enabled=true --set-string healthMonitor.token=fixture-only >"$work/helm.yaml"
  ud="$(yq "${yq_flags[@]}" -o=json 'select(.kind == "Deployment" and .metadata.name == "userdata-local") | .spec.template.spec.containers[0]' "$work/helm.yaml")"
  jq -e '.readinessProbe.exec.command == ["/app/userdata-health"] and (.readinessProbe | has("httpGet") | not)' <<<"$ud" >/dev/null \
    || { echo "FAIL: helm userdata-local readiness must be the packaged exec probe: $(jq -c .readinessProbe <<<"$ud")" >&2; exit 1; }
  jq -e '[.env[] | select(.name == "USERDATA_LOCAL_HTTP_ADDR" and .value == ":9672")] | length == 1' <<<"$ud" >/dev/null \
    || { echo "FAIL: helm userdata-local must bind USERDATA_LOCAL_HTTP_ADDR=:9672" >&2; exit 1; }
  if helm template fixture "$CHART" --set media.enabled=true --set insecureDisableTLS=false \
    --set-string healthMonitor.token=fixture-only >/dev/null 2>"$work/helm.err"; then
    echo "FAIL: helm renders media.enabled with TLS on (ADR-0033 transport unsupported in the chart)" >&2
    exit 1
  fi
  grep -q 'ADR-0033' "$work/helm.err" || { echo "FAIL: helm secure-media refusal must cite ADR-0033: $(cat "$work/helm.err")" >&2; exit 1; }
  if command -v kubeconform >/dev/null 2>&1; then
    kubeconform -strict -summary "$work/helm.yaml" >"$work/kc.out" 2>&1 \
      || { echo "FAIL: kubeconform on the media chart render: $(cat "$work/kc.out")" >&2; exit 1; }
  fi
  echo "OK helm: exec userdata-health probe in insecure dev; TLS-on media render refused (ADR-0033)"
else
  echo "skip: helm not found (chart render checks)"
fi
KOVERLAY="$ROOT/deploy/kustomize/overlays/media-stack/media-stack.yaml"
k_ud="$(yq ea '[select(.metadata.name == "userdata-local")] | length' "$KOVERLAY")"
k_url="$(yq ea '[.. | select(type == "!!map" and .name == "USERDATA_LOCAL_URL")] | length' "$KOVERLAY")"
if [[ "$k_ud" != 0 || "$k_url" != 0 ]]; then
  echo "FAIL: kustomize media-stack wires userdata-local without ADR-0033 identities" >&2
  exit 1
fi
if ! grep -q 'unsupported' "$ROOT/deploy/README.md" || ! grep -q 'ADR-0033' "$ROOT/deploy/README.md"; then
  echo "FAIL: deploy/README.md must mark the userdata transport unsupported for Helm/kustomize" >&2
  exit 1
fi
echo "OK kustomize: no userdata-local wiring (explicitly unsupported)"

# ---- negative fixtures: each mutated copy must fail ----
expect_fail() {
  local name="$1" r="$2" d="$3" f="$4" h="$5"
  if bash "$0" "$r" "$d" "$f" "$h" >/dev/null 2>"$work/err"; then
    echo "FAIL: fixture '$name' passed" >&2
    exit 1
  fi
  echo "OK fixture '$name' rejected: $(head -1 "$work/err")"
}
mutate() { # <src> <dst> <yq expression>
  cp "$1" "$2"
  yq -i "$3" "$2"
}
r="$work/reg.yml" d="$work/dev.yml" f="$work/ref.yml" h="$work/run-host.sh"
mutate "$REG" "$r" '.services["media-ui"].environment.USERDATA_LOCAL_URL = "http://userdata-local:9672"'
expect_fail "household BFF plaintext URL" "$r" "$DEV" "$REF" "$RUNHOST"
mutate "$REG" "$r" '.services["admin-ui"].environment.ADMIN_UI_USERDATA_URL = "http://userdata-local:9672"'
expect_fail "household admin-ui plaintext URL" "$r" "$DEV" "$REF" "$RUNHOST"
mutate "$REG" "$r" '.services["userdata-local"].ports = ["${USERDATA_LOCAL_PORT:-9672}:9672"]'
expect_fail "household userdata host port" "$r" "$DEV" "$REF" "$RUNHOST"
mutate "$REG" "$r" 'del(.services["userdata-local"].healthcheck)'
expect_fail "household probe missing" "$r" "$DEV" "$REF" "$RUNHOST"
mutate "$REG" "$r" '.services["userdata-local"].healthcheck.test = ["CMD-SHELL", "curl -sf http://127.0.0.1:9672/health"]'
expect_fail "household bare HTTP probe" "$r" "$DEV" "$REF" "$RUNHOST"
mutate "$REG" "$r" '.services["jellyfin-bridge"].environment.USERDATA_SYNC = "${USERDATA_SYNC:-1}"'
expect_fail "household jellyfin sync enabled" "$r" "$DEV" "$REF" "$RUNHOST"
mutate "$REG" "$r" '.services["jellyfin-bridge"].environment.USERDATA_LOCAL_URL = "https://userdata-local:9672"'
expect_fail "household jellyfin provider URL" "$r" "$DEV" "$REF" "$RUNHOST"
mutate "$DEV" "$d" 'del(.services["media-ui"].environment.USERDATA_LOCAL_URL)'
expect_fail "dev BFF without explicit plaintext URL" "$REG" "$d" "$REF" "$RUNHOST"
mutate "$REF" "$f" '.services["jellyfin"].environment.USERDATA_SYNC = "1"'
expect_fail "reference jellyfin sync enabled" "$REG" "$DEV" "$f" "$RUNHOST"
sed 's/^        USERDATA_SYNC=0 \\$/        USERDATA_SYNC="${USERDATA_SYNC:-1}" \\/' "$RUNHOST" >"$h"
expect_fail "run-host jellyfin sync default on" "$REG" "$DEV" "$REF" "$h"
sed 's|USERDATA_LOCAL_URL="${USERDATA_LOCAL_URL:-$_userdata_http_url}" \\|USERDATA_LOCAL_URL="${USERDATA_LOCAL_URL:-http://127.0.0.1:9672}" \\|' "$RUNHOST" >"$h"
expect_fail "run-host BFF plaintext default" "$REG" "$DEV" "$REF" "$h"
echo "OK check-compose-userdata-transport"
