#!/usr/bin/env bash
# Offline tests for scripts/gen-enrollment.sh (ADR-0017 enrollment tokens).
#
# Fixtures: tokens printed by core's own `muxcored enroll token <id>` (core
# v0.6.14, internal/enroll.Token) for the secret below; every token the script
# writes is also recomputed with openssl HMAC-SHA256.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
GEN="$ROOT/scripts/gen-enrollment.sh"
COMPOSE="$ROOT/docker-compose.registry.yml"

command -v openssl >/dev/null || { echo "skip: openssl not found"; exit 0; }

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }
ok() { echo "ok: $*"; }

FIXTURE_SECRET=0123456789abcdef0123456789abcdef
declare -A FIXTURE=(
  [api-rest]=mct_2_api-rest_5d337c18a66da96b244b1d5a0d8044eefcfc966bc7e743e65a9d0f8978ac4b2a
  [jellyfin]=mct_2_jellyfin_484dca64bf61f7b0bc8f4e028e97b62c07d8e7f6f554b18d492bcf7425cb31ab
  [media_x.y]=mct_2_media_x.y_102b9b5c9f6244b100ad2da3b5a4ff287e8f76ee8251b0900cfec7e9c20bb252
)

impls=(openssl)
command -v python3 >/dev/null && impls+=(python3)

# 1. --print-token matches core's fixtures (both HMAC implementations).
for impl in "${impls[@]}"; do
  for id in "${!FIXTURE[@]}"; do
    got="$(GEN_ENROLLMENT_HMAC="$impl" MUXCORE_ENROLL_SECRET="$FIXTURE_SECRET" "$GEN" --print-token "$id")"
    [[ "$got" == "${FIXTURE[$id]}" ]] || fail "$impl token for $id: got $got want ${FIXTURE[$id]}"
  done
  ok "--print-token matches muxcored enroll token ($impl)"
done

# 2. Fresh env file: 0600, a valid secret, one token per compose module ID.
env_file="$tmp/env"
printf 'MVP_ADMIN_PASSWORD=keep-me\n# a comment\n' >"$env_file"
chmod 644 "$env_file"
"$GEN" --env-file "$env_file" >/dev/null
[[ "$(stat -c %a "$env_file")" == 600 ]] || fail "env file mode $(stat -c %a "$env_file"), want 600"
grep -qx 'MVP_ADMIN_PASSWORD=keep-me' "$env_file" || fail "unrelated line not preserved"
grep -qx '# a comment' "$env_file" || fail "comment not preserved"
secret="$(sed -n 's/^MUXCORE_ENROLL_SECRET=//p' "$env_file")"
[[ "$secret" =~ ^[0-9a-f]{64}$ ]] || fail "generated secret is not 64 hex chars: $secret"
mapfile -t ids < <(sed -n 's/^[[:space:]]*MUXCORE_MODULE_ID:[[:space:]]*\([a-z0-9.-]*\).*/\1/p' "$COMPOSE" | sort -u)
[[ ${#ids[@]} -gt 20 ]] || fail "parsed only ${#ids[@]} module IDs from $COMPOSE"
n_tokens="$(grep -c '^MUXCORE_ENROLL_TOKEN_' "$env_file")"
[[ "$n_tokens" -eq ${#ids[@]} ]] || fail "$n_tokens tokens for ${#ids[@]} module IDs"
for id in "${ids[@]}"; do
  var="MUXCORE_ENROLL_TOKEN_$(tr 'a-z.-' 'A-Z__' <<<"$id")"
  tok="$(sed -n "s/^${var}=//p" "$env_file")"
  mac="$(printf '%s' "$id" | openssl dgst -sha256 -hmac "$secret" -r | cut -d' ' -f1)"
  [[ "$tok" == "mct_2_${id}_${mac}" ]] || fail "$var=$tok, want mct_2_${id}_${mac}"
done
grep -q '^MUXCORE_ENROLL_TOKEN_JELLYFIN=mct_2_jellyfin_' "$env_file" || fail "jellyfin-bridge token not keyed by module ID"
ok "fresh env file: 0600, secret, ${#ids[@]} tokens verified with openssl"

# 3. Idempotent: byte-identical on rerun, secret kept.
sum1="$(sha256sum <"$env_file")"
out="$("$GEN" --env-file "$env_file")"
[[ "$(sha256sum <"$env_file")" == "$sum1" ]] || fail "rerun changed the env file"
grep -q 'secret kept' <<<"$out" || fail "rerun did not report the kept secret: $out"
ok "rerun is byte-identical"

# 4. An existing secret (outside the managed block) is adopted; stale tokens dropped.
printf 'FOO=1\nMUXCORE_ENROLL_SECRET=%s\nMUXCORE_ENROLL_TOKEN_GONE=mct_2_gone_x\n' "$FIXTURE_SECRET" >"$tmp/env2"
"$GEN" --env-file "$tmp/env2" --compose "$tmp/none.yml" 2>/dev/null && fail "missing compose file accepted"
printf 'services:\n  a:\n    environment:\n      MUXCORE_MODULE_ID: api-rest\n' >"$tmp/c.yml"
"$GEN" --env-file "$tmp/env2" --compose "$tmp/c.yml" --id jellyfin >/dev/null
grep -qx "MUXCORE_ENROLL_SECRET=$FIXTURE_SECRET" "$tmp/env2" || fail "existing secret not kept"
grep -qx "MUXCORE_ENROLL_TOKEN_API_REST=${FIXTURE[api-rest]}" "$tmp/env2" || fail "api-rest token wrong"
grep -qx "MUXCORE_ENROLL_TOKEN_JELLYFIN=${FIXTURE[jellyfin]}" "$tmp/env2" || fail "--id token wrong"
grep -q 'MUXCORE_ENROLL_TOKEN_GONE' "$tmp/env2" && fail "stale token kept"
[[ "$(grep -c '^MUXCORE_ENROLL_SECRET=' "$tmp/env2")" -eq 1 ]] || fail "secret duplicated"
grep -qx 'FOO=1' "$tmp/env2" || fail "FOO=1 lost"
ok "existing secret adopted, stale tokens dropped, --compose/--id honoured"

# 5. --print-token reads the env file when the variable is unset.
got="$(env -u MUXCORE_ENROLL_SECRET "$GEN" --env-file "$tmp/env2" --print-token jellyfin)"
[[ "$got" == "${FIXTURE[jellyfin]}" ]] || fail "--print-token from env file: $got"
ok "--print-token reads the secret from the env file"

# 6. --rotate replaces the secret; invalid secrets and IDs are refused.
"$GEN" --env-file "$tmp/env2" --compose "$tmp/c.yml" --rotate >/dev/null
grep -qx "MUXCORE_ENROLL_SECRET=$FIXTURE_SECRET" "$tmp/env2" && fail "--rotate kept the secret"
printf 'MUXCORE_ENROLL_SECRET=short\n' >"$tmp/env3"
"$GEN" --env-file "$tmp/env3" --compose "$tmp/c.yml" 2>/dev/null && fail "short secret accepted"
grep -qx 'MUXCORE_ENROLL_SECRET=short' "$tmp/env3" || fail "refused run modified the file"
"$GEN" --env-file "$tmp/env2" --compose "$tmp/c.yml" --id 'bad id' 2>/dev/null && fail "invalid module ID accepted"
printf 'services:\n  a:\n    environment:\n      MUXCORE_MODULE_ID: a-b\n  b:\n    environment:\n      MUXCORE_MODULE_ID: a.b\n' >"$tmp/clash.yml"
"$GEN" --env-file "$tmp/env2" --compose "$tmp/clash.yml" 2>/dev/null && fail "variable-name clash accepted"
ok "--rotate, invalid secret / module ID / variable clash"

echo "OK gen-enrollment tests"
