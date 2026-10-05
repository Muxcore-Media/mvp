#!/usr/bin/env bash
# shellcheck shell=bash
# household-manifest.yaml `state:` mapping helpers (ADR-0013, FR-BAK-002/004).
#
# One row per state entry and deployment mode, used by run-host.sh (backup
# sources), scripts/restore-drill.sh and docs/RESTORE.md:
#
#   state_map_tsv <manifest> <host|compose>
#     id <TAB> archive-prefix <TAB> location <TAB> backup(true|false)
#
#   host:    location = run-host data/ subdirectory (`host:`), archive-prefix =
#            its basename (backup-local names a source by its basename). Entries
#            without `host:` are skipped (no host-mode state).
#   compose: location = named volume (`volume:`, without the compose project
#            prefix), archive-prefix = module id (/source/<id> in backup-local).
#
# Needs mikefarah yq v4 and jq.

state_map_require_tools() {
  local t
  for t in yq jq; do
    command -v "$t" >/dev/null 2>&1 || { echo "state-map: $t not found (need mikefarah yq v4 + jq)" >&2; return 1; }
  done
  yq --version 2>&1 | grep -q 'mikefarah' || { echo "state-map: yq must be mikefarah yq v4" >&2; return 1; }
}

state_map_tsv() {
  local manifest="$1" mode="$2"
  case "$mode" in
    host|compose) ;;
    *) echo "state-map: mode must be host or compose (got: $mode)" >&2; return 2 ;;
  esac
  [[ -f "$manifest" ]] || { echo "state-map: missing manifest $manifest" >&2; return 2; }
  state_map_require_tools || return 2
  local json
  json="$(yq -o=json '.state // {}' "$manifest")" || { echo "state-map: cannot parse $manifest" >&2; return 2; }
  jq -r --arg mode "$mode" '
    def bad_seg: (. == "" or . == "." or . == ".." or test("/") or test("^\\s|\\s$") or test("[\\t\\n]"));
    to_entries[] | .key as $id | .value as $e
    | ($e.backup != false | tostring) as $b
    | if $mode == "host" then
        select(($e.host // "") != "")
        | if ($e.host | tostring | bad_seg) then error("state.\($id).host must be one path segment under data/ (got \($e.host))") else . end
        | [$id, ($e.host | tostring), ($e.host | tostring), $b]
      else
        if (($e.volume // "") | tostring | bad_seg) then error("state.\($id).volume missing or invalid") else . end
        | [$id, $id, ($e.volume | tostring), $b]
      end
    | @tsv' <<<"$json"
}

# state_map_host_sources <manifest> <data-dir>: comma-separated absolute host
# state dirs to archive (backed-up entries only, de-duplicated, manifest order).
# This is run-host.sh's default BACKUP_SOURCE_DIRS.
state_map_host_sources() {
  local manifest="$1" data="$2" rows
  rows="$(state_map_tsv "$manifest" host)" || return $?
  awk -F'\t' -v d="$data" '$4 == "true" && !seen[$3]++ { printf "%s%s/%s", (n++ ? "," : ""), d, $3 } END { print "" }' <<<"$rows"
}
