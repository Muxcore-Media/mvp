#!/usr/bin/env bash
# Offline tests for the fixture acquisition smoke step (scripts/lib/acquisition-smoke.sh):
# gating, grpcurl JSON parsing, and the registry-mode flow with a stubbed grpcurl.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
fail() { echo "FAIL: $*" >&2; exit 1; }

command -v python3 >/dev/null || { echo "skip: python3 not found"; exit 0; }

# shellcheck disable=SC1091
source "$ROOT/scripts/lib/acquisition-smoke.sh"

# ---- gating ----
REGISTERED=""
acquisition_module_registered() { [[ " $REGISTERED " == *" $1 "* ]]; }

decide() {
  acquisition_smoke_detect
  acquisition_smoke_decide
}

REGISTERED="indexer-piratebay downloader-native-torrent"
[[ "$(decide)" == run ]] || fail "both peers registered should run"
acquisition_smoke_detect
[[ "$ACQ_INDEXER" == indexer-piratebay && "$ACQ_DOWNLOADER" == downloader-native-torrent ]] ||
  fail "detect picked $ACQ_INDEXER / $ACQ_DOWNLOADER"

REGISTERED="indexer-piratebay downloader-qbittorrent"
[[ "$(decide)" == run ]] || fail "qbittorrent (QBIT_FIXTURE) counts as a fixture downloader"

REGISTERED="downloader-native-torrent"
[[ "$(SMOKE_REQUIRE_ACQUISITION=0 decide)" == skip ]] || fail "missing indexer should skip by default"
REGISTERED="indexer-piratebay"
[[ "$(decide)" == skip ]] || fail "missing downloader should skip by default"
REGISTERED=""
[[ "$(decide)" == skip ]] || fail "nothing registered should skip"

REGISTERED="downloader-native-torrent"
if err="$(SMOKE_REQUIRE_ACQUISITION=1 decide 2>&1 >/dev/null)"; then
  fail "SMOKE_REQUIRE_ACQUISITION=1 without indexer should fail"
fi
grep -q 'indexer' <<<"$err" || fail "require failure should name the missing indexer: $err"
grep -q 'downloader (' <<<"$err" && fail "require failure must not blame the registered downloader: $err"

# Only fixture-capable indexers count; a live Torznab peer does not enable the step.
REGISTERED="indexer-torznab downloader-native-torrent"
[[ "$(decide)" == skip ]] || fail "torznab alone must not enable the fixture step"
REGISTERED="indexer-torznab downloader-native-torrent"
[[ "$(SMOKE_ACQUISITION_INDEXERS="indexer-piratebay,indexer-torznab" decide)" == run ]] ||
  fail "SMOKE_ACQUISITION_INDEXERS override (comma list) should be honoured"
echo "OK gating"

# ---- JSON parsing (grpcurl emits camelCase, int64 as strings) ----
search='{"matches":[
 {"guid":"live-1","title":"Fight.Club.2160p","indexerName":"Live Torznab","score":900},
 {"guid":"fixture-12346","title":"Fight.Club.1999.720p.WEB-DL","indexerName":"The Pirate Bay (fixture)","score":40,"size":"4294967296"},
 {"guid":"fixture-12345","title":"Fight.Club.1999.1080p.BluRay.x264","indexerName":"The Pirate Bay (fixture)","score":80,"seeders":42,
  "size":"8589934592","downloadUrl":"magnet:?xt=urn:btih:abc","downloadProtocol":"torrent"},
 {"guid":"fixture-9","title":"Fight.Club.CAM","indexerName":"The Pirate Bay (fixture)","score":99,"rejected":true,"rejectionReason":"quality"}]}'
pick="$(acquisition_pick_fixture <<<"$search")"
IFS=$'\t' read -r guid rel url proto size score indexer <<<"$pick"
[[ "$guid" == fixture-12345 && "$rel" == Fight.Club.1999.1080p.BluRay.x264 ]] || fail "pick: $pick"
[[ "$url" == magnet:* && "$proto" == torrent && "$size" == 8589934592 && "$score" == 80 ]] || fail "pick fields: $pick"
[[ "$indexer" == "The Pirate Bay (fixture)" ]] || fail "pick indexer: $indexer"

snake='{"matches":[{"guid":"g","title":"T","indexer_name":"TPB (fixture)","download_url":"u","score":1}]}'
[[ "$(acquisition_pick_fixture <<<"$snake" | cut -f1)" == g ]] || fail "snake_case match not parsed"

if err="$(acquisition_pick_fixture <<<'{"matches":[{"title":"x","indexerName":"Live"}]}' 2>&1)"; then
  fail "live-only matches must not pick"
fi
grep -q 'no fixture-indexer match among 1' <<<"$err" || fail "no-fixture message: $err"
acquisition_pick_fixture <<<'{}' >/dev/null 2>&1 && fail "empty search must not pick"
if err="$(acquisition_pick_fixture <<<'{"matches":[{"title":"x","indexerName":"(fixture)","rejected":true,"rejectionReason":"cutoff"}]}' 2>&1)"; then
  fail "all-rejected must not pick"
fi
grep -q 'cutoff' <<<"$err" || fail "rejected message: $err"
echo "OK pick"

hist='{"records":[
 {"id":"h0","downloadId":"dl-0","status":"import_failed","statusDetail":"stale"},
 {"id":"h1","downloadId":"dl-1","guid":"fixture-12345","status":"sent"}]}'
[[ "$(acquisition_history_state dl-1 <<<"$hist")" == $'pending\tsent' ]] || fail "pending: $(acquisition_history_state dl-1 <<<"$hist")"
[[ "$(acquisition_history_state dl-1 <<<"${hist/\"sent\"/\"completed\"}")" == completed ]] || fail "completed"
st="$(acquisition_history_state dl-1 <<<'{"records":[{"downloadId":"dl-1","status":"import_failed","statusDetail":"no importable files found"}]}')"
[[ "$st" == $'failed\timport_failed: no importable files found' ]] || fail "import_failed: $st"
st="$(acquisition_history_state dl-1 <<<'{"records":[{"downloadId":"dl-1","status":"failed","statusLabel":"Download failed"}]}')"
[[ "$st" == $'failed\tfailed: Download failed' ]] || fail "failed label fallback: $st"
[[ "$(acquisition_history_state dl-9 <<<"$hist")" == missing ]] || fail "missing"
[[ "$(acquisition_history_state dl-9 fixture-12345 <<<"$hist")" == $'pending\tsent' ]] || fail "guid fallback"
[[ "$(acquisition_history_state dl-1 <<<'{}')" == missing ]] || fail "empty history"
echo "OK history"

[[ "$(acquisition_movie_id 550 <<<'{"movies":[{"id":"mv_1","tmdbId":603},{"id":"mv_2","tmdbId":550}]}')" == mv_2 ]] || fail "movie id"
[[ -z "$(acquisition_movie_id 550 <<<'{"movies":[]}')" ]] || fail "movie id absent"
files='{"files":[{"id":"f1","filePath":"/lib/Old.mkv"},{"id":"f2","filePath":"/lib/Fight Club (1999)/Fight.Club.1999.1080p.BluRay.x264.mkv"}]}'
[[ "$(acquisition_file_ids <<<"$files" | tr '\n' ' ')" == "f1 f2 " ]] || fail "file ids"
[[ "$(acquisition_file_match Fight.Club.1999.1080p.BluRay.x264 $'f1\nf2' <<<"$files")" == *BluRay.x264.mkv ]] || fail "file by release name"
[[ "$(acquisition_file_match Other.Release f1 <<<"$files")" == *BluRay.x264.mkv ]] || fail "file new since dispatch"
[[ -z "$(acquisition_file_match Other.Release $'f1\nf2' <<<"$files")" ]] || fail "no new / matching file"
[[ "$(acquisition_bff_stream_url mv_2 <<<'{"items":[{"id":"mv_2","has_file":true,"stream_url":"/stream/movies/mv_2"}]}')" == /stream/movies/mv_2 ]] ||
  fail "bff stream url"
[[ -z "$(acquisition_bff_stream_url mv_2 <<<'{"items":[{"id":"mv_2","has_file":false}]}')" ]] || fail "bff no file"
echo "OK helpers"

# ---- registry-mode flow with stubbed grpcurl ----
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
HIST_STATUS=completed
registry_smoke_grpcurl() {
  local addr="$1" method="${*: -1}" body=""
  shift
  while [[ $# -gt 0 ]]; do
    [[ "$1" == -d ]] && body="$2"
    shift
  done
  echo "$addr ${method##*/} $body" >>"$tmp/calls"
  case "${method##*/}" in
    ListMovies) echo '{"movies":[{"id":"mv_550","tmdbId":550}]}' ;;
    ListFiles)
      if [[ -f "$tmp/dispatched" ]]; then
        echo '{"files":[{"id":"f-new","filePath":"/data/movies/Fight Club (1999)/Fight.Club.1999.1080p.BluRay.x264.mkv"}]}'
      else
        echo '{"files":[]}'
      fi
      ;;
    AddToQueue) echo '{"queueId":"w_movie_mv_550"}' ;;
    SearchItem) echo "$search" ;;
    Dispatch) touch "$tmp/dispatched"; echo '{"downloadId":"dl-1","status":"sent"}' ;;
    GetHistory) echo "{\"records\":[{\"downloadId\":\"dl-1\",\"status\":\"${HIST_STATUS}\",\"statusDetail\":\"no importable files found\"}]}" ;;
    GetMovie) echo '{"movie":{"id":"mv_550","hasFile":true}}' ;;
    *) echo "unexpected $method" >&2; return 1 ;;
  esac
}
out="$(registry_smoke_cmd_acquirefixture -movies-addr media-movies:9420 -automation-addr media-automation:9460 -root /data/movies)" ||
  fail "registry acquirefixture failed: $out"
grep -q 'OK acquisition fixture: movie=mv_550 release="Fight.Club.1999.1080p.BluRay.x264"' <<<"$out" || fail "registry output: $out"
grep -q 'bff=skipped' <<<"$out" || fail "BFF should be skipped without -media-ui: $out"
grep -q 'media-automation:9460 Dispatch .*"guid": "fixture-12345"' "$tmp/calls" || fail "dispatch body: $(cat "$tmp/calls")"
grep -q '"item_id": "mv_550"' "$tmp/calls" || fail "dispatch item_id"
grep -q 'AddMovie' "$tmp/calls" && fail "AddMovie must not be called when the movie exists"

rm -f "$tmp/dispatched" "$tmp/calls"
HIST_STATUS=import_failed
if out="$(registry_smoke_cmd_acquirefixture 2>&1)"; then
  fail "import_failed should fail the registry step"
fi
grep -q 'import_failed: no importable files found' <<<"$out" || fail "import_failed detail: $out"
echo "OK registry flow"

# ---- smoke.sh wiring ----
smoke="$ROOT/smoke.sh"
acq_line="$(grep -n 'smoke_cmd acquirefixture' "$smoke" | head -1 | cut -d: -f1)"
scan_line="$(grep -n 'smoke_cmd importscan' "$smoke" | head -1 | cut -d: -f1)"
[[ -n "$acq_line" && -n "$scan_line" && "$acq_line" -lt "$scan_line" ]] || fail "acquisition step must run before importscan"
# shellcheck disable=SC2016 # literal ${acq_label} in smoke.sh
grep -q 'PASS: MVP smoke (.*${acq_label}' "$smoke" || fail "PASS line must name acquisition"
grep -q 'acquirefixture) registry_smoke_cmd_acquirefixture' "$ROOT/scripts/lib/registry-smoke.sh" || fail "registry dispatch missing"
grep -Eq 'grpcurl:(latest)?"' "$ROOT/scripts/lib/registry-smoke.sh" && fail "grpcurl image must be pinned to a version tag"
grep -Eq 'GRPCURL_IMAGE:-[^}]*grpcurl:v[0-9]' "$ROOT/scripts/lib/registry-smoke.sh" || fail "grpcurl image tag not pinned"
if grep -nE '(^|[^_A-Za-z])docker (run|compose)' "$smoke" "$ROOT/scripts/lib/registry-smoke.sh" | grep -v '^\S*:[0-9]*:\s*#' | grep -v 'MUXCORE_COMPOSE:-docker compose'; then
  fail "smoke helpers must use \${MUXCORE_CONTAINER_CLI:-docker} / \${MUXCORE_COMPOSE:-docker compose}"
fi
echo "OK smoke.sh wiring"

echo "OK smoke-acquisition script tests"
