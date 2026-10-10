#!/usr/bin/env bash
# State/backup coverage check (ADR-0013, FR-BAK-002).
#
# Validates household-manifest.yaml `state` / `stateless` / `excluded_volumes`
# against docker-compose.registry.yml:
#   - every required/recommended module is in `state` or `stateless` (not both);
#   - each state entry's service mounts <volume>:<mount>, declares the volume, and
#     sets every env var exactly (values must live under the mount);
#   - `erasure` (ADR-0035) is consumer|provider|none and only on personal entries;
#   - backed-up entries (backup != false) are mounted read-only in backup-local at
#     /source/<module-id> and listed in its default BACKUP_SOURCE_DIRS;
#   - backup: false entries and excluded_volumes are never mounted under /source;
#   - backup-local has no /source mount or default source without a state entry;
#   - any service mounting a named volume that is neither state, shares, nor
#     excluded is reported (error for required/recommended services, else warn).
# Required/recommended drift is an error; optional-module drift only warns.
#
# Usage: check-state-coverage.sh [MANIFEST] [COMPOSE]
# Needs mikefarah yq v4 and jq (both preinstalled on GitHub ubuntu runners).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
MANIFEST="${1:-$ROOT/household-manifest.yaml}"
COMPOSE="${2:-$ROOT/docker-compose.registry.yml}"

die() { echo "state-coverage: $*" >&2; exit 2; }
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

manifest_json="$(to_json "$MANIFEST")" || die "cannot parse $MANIFEST"
compose_json="$(to_json "$COMPOSE")" || die "cannot parse $COMPOSE"

# Emits lines "ERROR: ..." / "WARN: ...".
report="$(jq -nr --argjson m "$manifest_json" --argjson c "$compose_json" '
  def strip_comment: tostring | sub("\\s*#.*$"; "");
  ($m.required // [] | map(strip_comment)) as $req
  | ($m.recommended // [] | map(strip_comment)) as $rec
  | ($req + $rec) as $core_set
  | ($m.state // {}) as $state
  | ($m.stateless // [] | map(strip_comment)) as $stateless
  | ($m.excluded_volumes // [] | map(.volume)) as $excluded
  | ($c.services // {}) as $svcs
  | ($c.volumes // {} | keys) as $declared
  | ($svcs["backup-local"] // null) as $bl
  | (($bl.volumes // []) | map(tostring)) as $bl_mounts
  | ($bl.environment.BACKUP_SOURCE_DIRS // "" | tostring
     | if test("^\\$\\{BACKUP_SOURCE_DIRS:-") then sub("^\\$\\{BACKUP_SOURCE_DIRS:-"; "") | sub("\\}$"; "") else . end
     | split(",") | map(gsub("^\\s+|\\s+$"; "")) | map(select(. != ""))) as $bl_sources
  | def sev($id): if ($core_set | index($id)) then "ERROR" else "WARN" end;
    def vol_of($mnt): $mnt | split(":")[0];
    def mounts($svc): (($svcs[$svc].volumes // []) | map(tostring));
    def has_mount($svc; $v; $p): mounts($svc) | any(. == "\($v):\($p)" or . == "\($v):\($p):rw" or . == "\($v):\($p):ro");
  [
    # 1. coverage of required/recommended modules
    ($core_set[] as $id
      | if ($state[$id] == null) and (($stateless | index($id)) == null)
        then "ERROR: \($id): required/recommended module is in neither state nor stateless"
        elif ($state[$id] != null) and (($stateless | index($id)) != null)
        then "ERROR: \($id): listed in both state and stateless"
        else empty end),
    # 2. per state entry
    ($state | to_entries[] | .key as $id | .value as $e
      | ($e.service // $id) as $svc
      | sev($id) as $s
      | ($e.backup != false) as $backed
      | if ($e.volume == null or $e.mount == null) then "\($s): \($id): state entry needs volume and mount"
        elif $svcs[$svc] == null then "\($s): \($id): compose service \($svc) not found"
        else
          ( if has_mount($svc; $e.volume; $e.mount) then empty
            else "\($s): \($id): service \($svc) does not mount \($e.volume):\($e.mount)" end ),
          ( if ($declared | index($e.volume)) then empty
            else "\($s): \($id): volume \($e.volume) not declared under top-level volumes" end ),
          ( ($e.env // {}) | to_entries[] | .key as $k | (.value | tostring) as $want
            | ($svcs[$svc].environment[$k] // null) as $got
            | if $got == null then "\($s): \($id): service \($svc) does not set \($k)"
              elif ($got | tostring) != $want then "\($s): \($id): \($svc) \($k)=\($got) (manifest: \($want))"
              elif ($want == $e.mount or ($want | startswith($e.mount + "/"))) | not
              then "\($s): \($id): \($k)=\($want) is outside mount \($e.mount)"
              else empty end ),
          ( ($e.kind // "") as $kind
            | if (["sqlite","files","json"] | index($kind)) then empty
              else "\($s): \($id): kind must be sqlite|files|json (got \($kind))" end ),
          ( if ($e.personal | type) == "boolean" then empty
            else "\($s): \($id): personal must be true/false" end ),
          ( if ($e.erasure == null) then empty
            elif (["consumer","provider","none"] | index($e.erasure)) == null
            then "\($s): \($id): erasure must be consumer|provider|none (got \($e.erasure))"
            elif $e.personal != true
            then "\($s): \($id): erasure applies only to personal: true entries"
            else empty end ),
          ( if $backed then
              ( if $bl == null then "\($s): \($id): backup-local service missing from compose"
                elif ($bl_mounts | index("\($e.volume):/source/\($id):ro")) then empty
                else "\($s): \($id): backup-local must mount \($e.volume):/source/\($id):ro" end ),
              ( if ($bl_sources | index("/source/\($id)")) then empty
                else "\($s): \($id): /source/\($id) missing from backup-local default BACKUP_SOURCE_DIRS" end ),
              ( if ($excluded | index($e.volume)) then "\($s): \($id): volume \($e.volume) is backed up but listed in excluded_volumes" else empty end )
            else
              ( if ($excluded | index($e.volume)) then empty
                else "\($s): \($id): backup: false requires \($e.volume) in excluded_volumes" end )
            end )
        end),
    # 3. excluded volumes never reach backup-local
    ($bl_mounts[] | select(test(":/source/")) | select(vol_of(.) as $v | $excluded | index($v))
      | "ERROR: backup-local archives excluded volume \(vol_of(.)) (\(.))"),
    # 4. stray backup-local sources
    ($bl_mounts[] | select(test(":/source/"))
      | (split(":")[1] | sub("^/source/"; "")) as $id
      | if ($state[$id] == null) or ($state[$id].backup == false)
        then "ERROR: backup-local mounts \(.) but \($id) is not a backed-up state entry"
        elif vol_of(.) != $state[$id].volume
        then "ERROR: backup-local mounts \(vol_of(.)) at /source/\($id) (manifest volume: \($state[$id].volume))"
        else empty end),
    ($bl_sources[] | select(startswith("/source/")) | sub("^/source/"; "") as $id
      | if ($bl_mounts | any(endswith(":/source/\($id):ro"))) then empty
        else "ERROR: default BACKUP_SOURCE_DIRS lists /source/\($id) but backup-local does not mount it" end),
    # 5. optional services with unaccounted volumes
    ( ([$state[] | .volume] + [$state[] | (.shares // [])[]] + $excluded) as $known
      | $svcs | to_entries[]
      | select(.key != "backup-local")
      | .key as $svc
      | ((.value.volumes // []) | map(tostring) | map(vol_of(.))
         | map(select(startswith(".") or startswith("/") | not))
         | map(select(. as $v | $known | index($v) | not)))[]
      | "\(if ($core_set | index($svc)) then "ERROR" else "WARN" end): service \($svc) mounts volume \(.) that is neither state nor excluded_volumes")
  ] | .[]
')"

errors=0
warnings=0
while IFS= read -r line; do
  [[ -n "$line" ]] || continue
  echo "$line" >&2
  case "$line" in
    ERROR:*) errors=$((errors + 1)) ;;
    WARN:*) warnings=$((warnings + 1)) ;;
  esac
done <<<"$report"

n_state="$(jq -n --argjson m "$manifest_json" '$m.state // {} | length')"
n_stateless="$(jq -n --argjson m "$manifest_json" '$m.stateless // [] | length')"
if ((errors > 0)); then
  echo "state-coverage: FAIL ($errors error(s), $warnings warning(s))" >&2
  exit 1
fi
echo "OK: state coverage — $n_state state entries, $n_stateless stateless, $warnings warning(s)"
