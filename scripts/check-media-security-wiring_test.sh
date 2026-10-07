#!/usr/bin/env bash
# Guard roots must name media mounts, never module state or mesh identity data.
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
print("OK: media guard roots match mounts; playback origins and dev credentials agree")
PY
