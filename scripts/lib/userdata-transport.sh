# shellcheck shell=bash
# userdata-local transport preflight for run-host.sh (ADR-0033).
#
# The host runner has no supported mesh identities for the authenticated
# userdata transport: scripts/issue-staging-module-certs.sh does not issue
# userdata-local/media-ui/admin-ui certificates by default, and the ones it
# issues carry only localhost/127.0.0.1/::1 SANs, so neither the checked client
# (fixed SAN/CN userdata-local) nor the provider's own identity check can
# succeed. Secure host modes (staging, MUXCORE_REQUIRE_TLS=1, or any run
# without the explicit insecure flag) are therefore unsupported for the
# userdata transport: refuse to start userdata-local or the media-ui BFF instead
# of failing at runtime. Explicit insecure dev (the run-host default) is
# unaffected; docker-compose.registry.yml is the supported household path.

# mvp_userdata_transport_preflight [START_ONLY] — non-zero (with a message) when
# this run would start userdata-local or media-ui in a secure host mode.
mvp_userdata_transport_preflight() {
  local only="${1:-}"
  case "${MUXCORE_INSECURE_DISABLE_TLS:-}" in
    true|1) return 0 ;;
  esac
  local -a affected=()
  if [[ "${MVP_ENABLE_USERDATA_LOCAL:-1}" == "1" ]] && [[ -z "$only" || "$only" == "userdata-local" ]]; then
    affected+=(userdata-local)
  fi
  if [[ "${MVP_ENABLE_MEDIA_UI:-1}" != "0" ]] && [[ -z "$only" || "$only" == "media-ui" ]]; then
    affected+=(media-ui)
  fi
  [[ ${#affected[@]} -eq 0 ]] && return 0
  echo "FAIL: run-host.sh: the userdata-local transport (ADR-0033) is unsupported in secure host mode" >&2
  echo "      (MUXCORE_PROFILE=${MUXCORE_PROFILE:-unset}, MUXCORE_REQUIRE_TLS=${MUXCORE_REQUIRE_TLS:-unset}): host certificates do not" >&2
  echo "      identify userdata-local/media-ui (scripts/issue-staging-module-certs.sh issues no such certs or SANs)." >&2
  echo "      Refusing to start: ${affected[*]}. Use docker-compose.registry.yml for a household stack, run" >&2
  echo "      explicit insecure dev (the run-host default), or set MVP_ENABLE_USERDATA_LOCAL=0 MVP_ENABLE_MEDIA_UI=0." >&2
  return 1
}
