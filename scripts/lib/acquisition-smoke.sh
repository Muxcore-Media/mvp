#!/usr/bin/env bash
# Fixture acquisition smoke step (roadmap T-M2-03, FR-INS-003; ADR-0008 fixture-only,
# ADR-0014 compose fixture smoke = release gate).
#
# Gate: the step runs when a fixture-capable indexer AND downloader are registered
# on the mesh; otherwise it is skipped, unless SMOKE_REQUIRE_ACQUISITION=1, which
# turns their absence into a failure. Only releases from an indexer whose name
# contains "fixture" are dispatched — never a live indexer / swarm / paid service.
#
# Host mode runs cmd/acquirefixture (Go). Registry mode (no host Go) runs the same
# flow below with containerized grpcurl + curl; the JSON parsing helpers are shared
# with scripts/smoke-acquisition_test.sh.
#
# Env:
#   SMOKE_REQUIRE_ACQUISITION=1        fail when indexer/downloader are not registered
#   SMOKE_ACQUISITION_INDEXERS          candidates (default: indexer-piratebay)
#   SMOKE_ACQUISITION_DOWNLOADERS       candidates (default: downloader-native-torrent downloader-qbittorrent)
#   SMOKE_ACQUISITION_TIMEOUT_SEC       wait for download completed (default 120)
#   SMOKE_ACQUISITION_FILE_TIMEOUT_SEC  wait for media-movies has_file (default 60)

ACQ_INDEXER=""
ACQ_DOWNLOADER=""

# Overridable probe (tests stub this). Uses the same discovery Resolve as smoke.sh.
acquisition_module_registered() {
  SMOKE_MODULES="$1" MUXCORE_GRPC_ADDR="${MESH:-${MUXCORE_MESH_ADDR:-127.0.0.1:9090}}" \
    smoke_cmd listmodules >/dev/null 2>&1
}

# Sets ACQ_INDEXER / ACQ_DOWNLOADER to the first registered candidate (or "").
acquisition_smoke_detect() {
  ACQ_INDEXER=""
  ACQ_DOWNLOADER=""
  local id indexers downloaders
  # Space- or comma-separated candidate lists.
  indexers="${SMOKE_ACQUISITION_INDEXERS:-indexer-piratebay}"
  downloaders="${SMOKE_ACQUISITION_DOWNLOADERS:-downloader-native-torrent downloader-qbittorrent}"
  for id in ${indexers//,/ }; do
    if acquisition_module_registered "$id"; then
      ACQ_INDEXER="$id"
      break
    fi
  done
  for id in ${downloaders//,/ }; do
    if acquisition_module_registered "$id"; then
      ACQ_DOWNLOADER="$id"
      break
    fi
  done
}

# Prints "run" or "skip"; prints a FAIL line to stderr and returns 1 when the
# step is required but a peer is missing. Reads ACQ_INDEXER / ACQ_DOWNLOADER.
acquisition_smoke_decide() {
  if [[ -n "$ACQ_INDEXER" && -n "$ACQ_DOWNLOADER" ]]; then
    echo run
    return 0
  fi
  local missing=()
  [[ -n "$ACQ_INDEXER" ]] || missing+=("indexer (${SMOKE_ACQUISITION_INDEXERS:-indexer-piratebay})")
  [[ -n "$ACQ_DOWNLOADER" ]] || missing+=("downloader (${SMOKE_ACQUISITION_DOWNLOADERS:-downloader-native-torrent downloader-qbittorrent})")
  case "${SMOKE_REQUIRE_ACQUISITION:-0}" in
    1 | true | yes)
      echo "FAIL: SMOKE_REQUIRE_ACQUISITION=1 but not registered: ${missing[*]} (run-host: MVP_ENABLE_ACQUISITION=1; compose: --profile indexer-piratebay --profile downloader-torrent)" >&2
      return 1
      ;;
  esac
  echo skip
}

# ---- JSON helpers (stdin = grpcurl JSON; accepts camelCase and snake_case) ----

# SearchItem response → one TSV line: guid title download_url protocol size score indexer
# for the best non-rejected match whose indexer name contains "fixture".
acquisition_pick_fixture() {
  python3 -c '
import json, sys
d = json.load(sys.stdin)
def g(m, *ks, default=""):
    for k in ks:
        if k in m and m[k] is not None:
            return m[k]
    return default
ms = d.get("matches") or []
fx = [m for m in ms if "fixture" in str(g(m, "indexerName", "indexer_name")).lower()]
ok = [m for m in fx if not g(m, "rejected", default=False)]
if not fx:
    names = ", ".join("%s@%s" % (g(m, "title"), g(m, "indexerName", "indexer_name")) for m in ms)
    sys.stderr.write("no fixture-indexer match among %d matches [%s]\n" % (len(ms), names))
    sys.exit(1)
if not ok:
    why = "; ".join("%s: %s" % (g(m, "title"), g(m, "rejectionReason", "rejection_reason")) for m in fx)
    sys.stderr.write("all %d fixture matches rejected [%s]\n" % (len(fx), why))
    sys.exit(1)
ok.sort(key=lambda m: (-int(g(m, "score", default=0)), -int(g(m, "seeders", default=0))))
m = ok[0]
print("\t".join(str(x).replace("\t", " ") for x in (
    g(m, "guid"), g(m, "title"), g(m, "downloadUrl", "download_url"),
    g(m, "downloadProtocol", "download_protocol"), g(m, "size", default=0),
    g(m, "score", default=0), g(m, "indexerName", "indexer_name"))))
'
}

# GetHistory response + DOWNLOAD_ID [GUID] → "completed" | "failed<TAB>status: detail"
# | "pending<TAB>status" | "missing".
acquisition_history_state() {
  python3 -c '
import json, sys
dl, guid = sys.argv[1], (sys.argv[2] if len(sys.argv) > 2 else "")
d = json.load(sys.stdin)
recs = d.get("records") or []
def g(r, *ks):
    for k in ks:
        if r.get(k):
            return str(r[k])
    return ""
rec = next((r for r in recs if g(r, "downloadId", "download_id") == dl), None)
if rec is None and guid:
    rec = next((r for r in recs if g(r, "guid") == guid), None)
if rec is None:
    print("missing"); sys.exit(0)
st = g(rec, "status")
if st == "completed":
    print("completed")
elif st in ("import_failed", "failed"):
    detail = g(rec, "statusDetail", "status_detail") or g(rec, "statusLabel", "status_label")
    print("failed\t%s: %s" % (st, detail))
else:
    print("pending\t%s" % (st or "unknown"))
' "$@"
}

# ListMovies response + TMDB → movie id (empty when absent).
acquisition_movie_id() {
  python3 -c '
import json, sys
t = int(sys.argv[1])
for m in json.load(sys.stdin).get("movies") or []:
    if int(m.get("tmdbId", m.get("tmdb_id", 0)) or 0) == t:
        print(m.get("id", "")); break
' "$1"
}

# ListFiles response → file ids, one per line.
acquisition_file_ids() {
  python3 -c '
import json, sys
for f in json.load(sys.stdin).get("files") or []:
    print(f.get("id", ""))
'
}

# ListFiles response + RELEASE + BEFORE_IDS (newline list) → path of a file that is
# new since dispatch or carries the release name (empty when none). The name match
# is token-wise, ignoring case and punctuation, like cmd/acquirefixture
# releaseMatches: media-rename turns "Fight.Club.1999.720p.WEB-DL" into
# "Fight Club (1999) [720p.WEB-DL].mkv", and a re-run (or smoke after a restore)
# re-imports onto the same file id, so the name is the only evidence there.
acquisition_file_match() {
  python3 -c '
import json, re, sys
def toks(x):
    return [t for t in re.split(r"[^a-z0-9]+", x.lower()) if t]
want, before = toks(sys.argv[1]), set(sys.argv[2].split())
for f in json.load(sys.stdin).get("files") or []:
    p = f.get("filePath", f.get("file_path", ""))
    have = set(toks(p))
    if f.get("id", "") not in before or (want and all(t in have for t in want)):
        print(p); break
' "$1" "$2"
}

# /api/movies JSON + MOVIE_ID → stream_url (empty when not listed / no file).
acquisition_bff_stream_url() {
  python3 -c '
import json, sys
mid = sys.argv[1]
for it in json.load(sys.stdin).get("items") or []:
    if it.get("id") == mid and it.get("has_file") and it.get("stream_url"):
        print(it["stream_url"]); break
' "$1"
}

# ---- registry mode (containerized grpcurl; mirrors cmd/acquirefixture) ----

registry_smoke_cmd_acquirefixture() {
  local movies_addr="${MOVIES_GRPC_ADDR:-media-movies:9420}" auto_addr="${AUTOMATION_GRPC_CLIENT_ADDR:-media-automation:9460}"
  local root="" media_ui="" auth_url="http://127.0.0.1:9401" require_bff=0
  local tmdb=550 title="Fight Club" year=1999
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -movies-addr) movies_addr="$2"; shift 2 ;;
      -automation-addr) auto_addr="$2"; shift 2 ;;
      -root) root="$2"; shift 2 ;;
      -media-ui) media_ui="${2%/}"; shift 2 ;;
      -auth-url) auth_url="${2%/}"; shift 2 ;;
      -require-bff) require_bff=1; shift ;;
      -tmdb) tmdb="$2"; shift 2 ;;
      -title) title="$2"; shift 2 ;;
      -year) year="$2"; shift 2 ;;
      *) shift ;;
    esac
  done
  local mv="muxcore.media.movies.v1.MovieManagementService"
  local au="muxcore.automation.v1.AutomationService"
  local out movie_id before pick guid rel dl_url proto size score indexer disp download_id disp_status

  out="$(registry_smoke_grpcurl "$movies_addr" -d '{"page":1,"page_size":100}' "$mv/ListMovies")" || return 1
  movie_id="$(acquisition_movie_id "$tmdb" <<<"$out")"
  if [[ -z "$movie_id" ]]; then
    out="$(registry_smoke_grpcurl "$movies_addr" \
      -d "{\"tmdb_id\":${tmdb},\"title\":\"${title}\",\"year\":${year},\"root_folder_path\":\"${root}\"}" \
      "$mv/AddMovie")" || return 1
    movie_id="$(python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get("movieId", d.get("movie_id","")))' <<<"$out")"
  fi
  [[ -n "$movie_id" ]] || { echo "FAIL acquisition: no movie id for tmdb=${tmdb}" >&2; return 1; }
  echo "movie id=${movie_id}"
  before="$(registry_smoke_grpcurl "$movies_addr" -d "{\"movie_id\":\"${movie_id}\"}" "$mv/ListFiles" | acquisition_file_ids)"

  registry_smoke_grpcurl "$auto_addr" \
    -d "{\"item_type\":\"movie\",\"item_id\":\"${movie_id}\",\"tmdb_id\":${tmdb},\"title\":\"${title}\",\"year\":${year}}" \
    "$au/AddToQueue" >/dev/null || return 1

  out="$(registry_smoke_grpcurl "$auto_addr" \
    -d "{\"item_type\":\"movie\",\"query\":\"${title}\",\"tmdb_id\":${tmdb},\"year\":${year},\"limit\":25}" \
    "$au/SearchItem")" || return 1
  pick="$(acquisition_pick_fixture <<<"$out")" || { echo "FAIL acquisition: SearchItem" >&2; return 1; }
  IFS=$'\t' read -r guid rel dl_url proto size score indexer <<<"$pick"
  echo "fixture pick=${rel} indexer=${indexer} score=${score}"

  disp="$(registry_smoke_grpcurl "$auto_addr" -d "$(GUID="$guid" REL="$rel" URL="$dl_url" PROTO="$proto" SIZE="$size" \
    SCORE="$score" IDX="$indexer" MID="$movie_id" TMDB="$tmdb" python3 -c '
import json, os
e = os.environ
print(json.dumps({"guid": e["GUID"], "title": e["REL"], "download_url": e["URL"], "download_protocol": e["PROTO"],
  "size": int(e["SIZE"] or 0), "score": int(e["SCORE"] or 0), "indexer_name": e["IDX"],
  "item_type": "movie", "item_id": e["MID"], "tmdb_id": int(e["TMDB"])}))')" "$au/Dispatch")" || {
    echo "FAIL acquisition: Dispatch ${rel}" >&2
    return 1
  }
  read -r download_id disp_status < <(python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get("downloadId", d.get("download_id","")) or "-", d.get("status","") or "-")' <<<"$disp")
  [[ "$disp_status" == "sent" && "$download_id" != "-" ]] || {
    echo "FAIL acquisition: dispatch status=${disp_status} download_id=${download_id}" >&2
    return 1
  }
  echo "dispatched download_id=${download_id}"

  local deadline=$((SECONDS + ${SMOKE_ACQUISITION_TIMEOUT_SEC:-120})) state="missing" kind detail
  while :; do
    state="$(registry_smoke_grpcurl "$auto_addr" -d '{"page":1,"page_size":100}' "$au/GetHistory" \
      | acquisition_history_state "$download_id" "$guid" || echo "missing")"
    IFS=$'\t' read -r kind detail <<<"$state"
    case "$kind" in
      completed) break ;;
      failed) echo "FAIL acquisition: download ${download_id} ${detail}" >&2; return 1 ;;
    esac
    if ((SECONDS >= deadline)); then
      echo "FAIL acquisition: download ${download_id} not completed (last: ${state})" >&2
      return 1
    fi
    sleep 2
  done
  echo "history completed download_id=${download_id}"

  local path=""
  deadline=$((SECONDS + ${SMOKE_ACQUISITION_FILE_TIMEOUT_SEC:-60}))
  while :; do
    out="$(registry_smoke_grpcurl "$movies_addr" -d "{\"movie_id\":\"${movie_id}\"}" "$mv/GetMovie" || true)"
    if grep -Eq '"hasFile"[[:space:]]*:[[:space:]]*true|"has_file"[[:space:]]*:[[:space:]]*true' <<<"$out"; then
      path="$(registry_smoke_grpcurl "$movies_addr" -d "{\"movie_id\":\"${movie_id}\"}" "$mv/ListFiles" \
        | acquisition_file_match "$rel" "$before")"
      [[ -n "$path" ]] && break
    fi
    if ((SECONDS >= deadline)); then
      echo "FAIL acquisition: movie ${movie_id} has no imported file for ${rel}" >&2
      return 1
    fi
    sleep 2
  done
  echo "movie has_file path=${path}"

  local bff="skipped (media-ui not checked)"
  if [[ -n "$media_ui" ]] && curl -sf -o /dev/null "${media_ui}/healthz"; then
    registry_smoke_acquisition_bff "$media_ui" "$auth_url" "$movie_id" || return 1
    bff="stream + playback resolve OK"
  elif [[ "$require_bff" -eq 1 ]]; then
    echo "FAIL acquisition: media-ui ${media_ui:-<unset>} not reachable" >&2
    return 1
  fi
  echo "OK acquisition fixture: movie=${movie_id} release=\"${rel}\" indexer=\"${indexer}\" download=${download_id} file=${path} bff=${bff}"
}

registry_smoke_acquisition_bff() {
  local base="$1" auth="$2" movie_id="$3"
  local jar hdr csrf loc code stream redir_enc
  jar="$(mktemp)"
  hdr="$(mktemp)"
  redir_enc="$(python3 -c 'import sys, urllib.parse; print(urllib.parse.quote(sys.argv[1], safe=""))' "${base}/auth/callback")"
  curl -s -c "$jar" -b "$jar" "${auth}/login?redirect=${redir_enc}" >/dev/null
  csrf="$(awk -F'\t' '($6=="muxcore-auth-csrf" || $6=="csrf-token"){print $7}' "$jar" | tr -d '\r')"
  code="$(curl -s -c "$jar" -b "$jar" -D "$hdr" -o /dev/null -w '%{http_code}' -X POST "${auth}/login/password" \
    --data-urlencode "username=${MVP_ADMIN_USER:-admin}" --data-urlencode "password=${MVP_ADMIN_PASSWORD:?MVP_ADMIN_PASSWORD is not set}" \
    --data-urlencode "csrf_token=${csrf}" --data-urlencode "redirect=${base}/auth/callback")"
  loc="$(awk -F': ' 'tolower($1)=="location"{gsub(/\r/,"",$2); print $2; exit}' "$hdr")"
  if [[ "$code" != 30[23] || -z "$loc" ]]; then
    echo "FAIL acquisition: BFF login HTTP ${code}" >&2
    rm -f "$jar" "$hdr"
    return 1
  fi
  curl -s -c "$jar" -b "$jar" -o /dev/null "$loc"
  stream="$(curl -sf -c "$jar" -b "$jar" "${base}/api/movies?page=1&page_size=100" | acquisition_bff_stream_url "$movie_id")"
  if [[ -z "$stream" ]]; then
    echo "FAIL acquisition: BFF /api/movies has no stream_url for ${movie_id}" >&2
    rm -f "$jar" "$hdr"
    return 1
  fi
  code="$(curl -s -c "$jar" -b "$jar" -o /dev/null -w '%{http_code}' -r 0-1023 "${base}${stream}")"
  if [[ "$code" != "200" && "$code" != "206" ]]; then
    echo "FAIL acquisition: BFF stream ${stream} HTTP ${code}" >&2
    rm -f "$jar" "$hdr"
    return 1
  fi
  if ! curl -sf -c "$jar" -b "$jar" -G "${base}/api/playback/resolve" --data-urlencode "src=${stream}" | grep -q '"stream_url"'; then
    echo "FAIL acquisition: BFF /api/playback/resolve for ${stream}" >&2
    rm -f "$jar" "$hdr"
    return 1
  fi
  rm -f "$jar" "$hdr"
  echo "BFF stream ${stream} HTTP ${code} + playback resolve OK"
}
