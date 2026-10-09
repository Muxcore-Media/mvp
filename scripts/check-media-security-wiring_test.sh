#!/usr/bin/env bash
# Guard roots must name media mounts, never module state or mesh identity data;
# the BFF's parental policy provider (USERDATA_LOCAL_URL) is wired in every stack.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
command -v yq >/dev/null || { echo "skip: yq not found"; exit 0; }
python3 - "$ROOT" <<'PY'
import json
import pathlib
import subprocess
import sys
from urllib.parse import urlsplit

root = pathlib.Path(sys.argv[1])
flags = ["--yaml-fix-merge-anchor-to-spec=true"] if "--yaml-fix-merge-anchor-to-spec" in subprocess.check_output(["yq", "--help"], text=True) else []
def load(name):
    return json.loads(subprocess.check_output(["yq", *flags, "-o=json", "explode(.)", str(root / name)], text=True))

guards = {
    "media-root-folders": ("ROOTS_ALLOWED_PREFIXES", ","),
    "media-rename": ("RENAME_ALLOWED_ROOTS", ":"),
    "media-subtitles": ("SUBS_MEDIA_ROOTS", ":"),
    "media-scanner": ("SCANNER_ALLOWED_ROOTS", ":"),
    "media-transcoder": ("TRANSCODER_MEDIA_ROOTS", ":"),
}
for filename in ("docker-compose.registry.yml", "docker-compose.yml"):
    services = load(filename)["services"]
    for name, (key, delimiter) in guards.items():
        service = services[name]
        paths = service["environment"][key].split(delimiter)
        mounts = [value.split(":")[1] for value in service.get("volumes", [])]
        for path in paths:
            assert path.startswith("/") and path not in ("/", "/data"), (filename, key, path)
            assert path in mounts, (filename, key, "guard root has no matching media mount", path)
            assert not any(piece in path for piece in ("mesh-id", "mesh-ca")), (filename, key, path)
    transcoder = services["media-transcoder"]["environment"]
    bff = services["media-ui"]["environment"]
    assert str(transcoder["TRANSCODER_ALLOW_URL_SOURCES"]) == "1"
    allowed_hosts = set(transcoder["TRANSCODER_URL_SOURCE_HOSTS"].split(","))
    assert allowed_hosts == {urlsplit(bff[key]).netloc for key in ("MOVIES_HTTP_URL", "TVSHOWS_HTTP_URL")}
    for name in ("downloader-native-torrent", "downloader-qbittorrent"):
        assert services[name]["environment"]["DOWNLOADER_INDEXER_HOSTS"] == "${DOWNLOADER_INDEXER_HOSTS:-}"
    # qBittorrent's roots describe the external daemon's paths, not local mounts.
    assert "QBIT_DOWNLOAD_ROOTS" in services["downloader-qbittorrent"]["environment"]

household = load("docker-compose.registry.yml")["services"]
dev = load("docker-compose.dev.yml")["services"]
assert household["media-ui"]["environment"]["TRANSCODER_HTTP_URL"] == "${TRANSCODER_HTTP_URL-https://media-transcoder:9526}"
assert dev["media-ui"]["environment"]["TRANSCODER_HTTP_URL"] == "${TRANSCODER_HTTP_URL-http://media-transcoder:9526}"
for services in (dev, load("docker-compose.yml")["services"]):
    token = services["media-ui"]["environment"]["TRANSCODER_HTTP_TOKEN"]
    assert token.startswith("${TRANSCODER_HTTP_TOKEN:?")
    assert services["media-transcoder"]["environment"]["TRANSCODER_HTTP_TOKEN"] == token
# ADR-0031/ADR-0033: the BFF gate reads USERDATA_LOCAL_URL; without it every
# gated request is 503. Household dials the mTLS provider by service name; the
# explicit insecure dev stacks stay plaintext. The household provider publishes
# no host port (scripts/check-compose-userdata-transport_test.sh renders these).
reference = load("docker-compose.yml")["services"]
assert household["media-ui"]["environment"]["USERDATA_LOCAL_URL"] == "https://userdata-local:9672"
assert dev["media-ui"]["environment"]["USERDATA_LOCAL_URL"] == "http://userdata-local:9672"
assert reference["media-ui"]["environment"]["USERDATA_LOCAL_URL"] == "http://userdata-local:9672"
assert "ports" not in household["userdata-local"]
assert "${USERDATA_LOCAL_PORT:-9672}:9672" in dev["userdata-local"]["ports"]
assert "${USERDATA_LOCAL_PORT:-9672}:9672" in reference["userdata-local"]["ports"]
for services in (household, reference):
    assert services["userdata-local"]["environment"]["USERDATA_LOCAL_HTTP_ADDR"] == ":9672"
    # userdata-local validates bearers against auth-local; the localhost default
    # is the container itself, which made /api/parental-policy answer 503.
    assert services["userdata-local"]["environment"]["AUTH_LOCAL_GRPC_ADDR"] == "auth-local:9403"
run_host = (root / "run-host.sh").read_text()
media_ui_start = run_host[run_host.index("maybe_start media-ui env"):]
media_ui_start = media_ui_start[:media_ui_start.index('"$BIN/mediauiprox"')]
assert 'USERDATA_LOCAL_URL="${USERDATA_LOCAL_URL:-$_userdata_http_url}"' in media_ui_start
assert "_userdata_http_url=https://127.0.0.1:9672" in run_host
assert 'USERDATA_LOCAL_HTTP_ADDR=":9672"' in run_host
print("OK: media guard roots match mounts; playback origins and dev credentials agree; BFF policy provider wired")
PY
