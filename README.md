# MuxCore MVP stack

Local reference compose for the media MVP path. Sibling clones under `/home/user/Projects/MuxCore` are the build context.

**P0 Media MVP status: met** (Wave 21 stack + smoke; Wave 22 packaging freeze). Operator URLs after `./run-host.sh up`: [`run/VIEW-ME.txt`](run/VIEW-ME.txt).

**Core pin:** published [`v0.5.3`](https://github.com/Muxcore-Media/core/releases/tag/v0.5.3) (Settings mesh + chunked Put; mTLS dial from v0.5.1). Nested SDK tags: `pkg/contracts` / `sdk/go/module` / `sdk/go/client` @ **v0.5.3**. Modules pinning without `replace` (`GOPRIVATE=github.com/Muxcore-Media/*`, `gh` HTTPS):

Historical wave pins (still accurate as of each wave; later patch tags supersede — see spool catalog **2.3.12**):
- **Waves 25–26 (core-adjacent):** `auth-local`, `call-policy-default`, `publish-policy-default`, `secrets-file`, `encryption-aesgcm`, `api-rest`, `jellyfin@v0.2.1`, `media-root-folders@v0.1.1`, `health-monitor`, `metadata-tmdb@v0.1.1`, `database-sqlite`, `secrets-vault`
- **Wave 27 (native media stack):** `contracts-{media-admin,downloader,indexer}@v0.1.0`, `contracts-notification@v0.1.1`, `downloader-native-torrent@v0.2.1`, `media-movies@v0.1.1`, `media-tvshows@v0.1.1`, `media-scanner@v0.1.1`, `media-automation@v0.1.0`, `request-media@v0.2.2`, `notification-default@v0.1.0`, `admin-ui@v0.1.3`
- **Wave 28 (leaves + indexer + smoke helpers):** `media-rename@v0.2.2`, `media-ffprobe@v0.1.2`, `media-subtitles@v0.4.2`, `media-custom-formats@v0.1.2`, `indexer-piratebay@v0.1.1`. Host [`go.mod`](go.mod) smoke helpers pin those published module tags (sibling replaces removed); local `replace => ../core*` kept for host convenience.
- **Wave 29 (non-host sibling pins):** `media-list-sync@v0.1.1`, `notification-apprise@v0.1.1`, `workflow-tapestry@v0.1.0` (not started by default `run-host.sh`). Polluted org `media-ui` dump archived; use `media-ui-app`.

**Consumer SPA:** [`Muxcore-Media/media-ui-app`](https://github.com/Muxcore-Media/media-ui-app) (private; host uses sibling `../media-ui-app/dist-app`).

Authoritative docs: [`../core.wiki/Getting-Started.md`](../core.wiki/Getting-Started.md), [`../core.wiki/Deployment.md`](../core.wiki/Deployment.md).  
Spool presets mirrored: [`../spool/tags/minimal.json`](../spool/tags/minimal.json), [`../spool/tags/media.json`](../spool/tags/media.json), [`../spool/tags/acquisition.json`](../spool/tags/acquisition.json).

Operator references in this repo: [`PORTS.md`](PORTS.md) (default gRPC/HTTP ports), [`LIVE-ACQUISITION.md`](LIVE-ACQUISITION.md) (VPN + live torrent smoke), [`BFF-API.md`](BFF-API.md) (mediauiprox JSON contracts), [`tls/`](tls/) (mTLS staging + secret rotation).

## Prerequisites

- Docker Compose v2 **or** host Go toolchain (`run-host.sh`)
- Org repos cloned as siblings (same layout as this workspace)
- Dev default: `MUXCORE_INSECURE_DISABLE_TLS=true` via `./run-host.sh`. Staging mTLS: `./run-host-staging.sh` (see [`tls/MTLS-STAGING.md`](tls/MTLS-STAGING.md)).

## GHCR images (optional)

Default compose builds from sibling trees. To pull prebuilt images instead, use [`docker-compose.ghcr.yml`](docker-compose.ghcr.yml) once `muxcored` (and peers) are published.

Publish `muxcored` from a host with rootless podman (e.g. gringotts):

```bash
./scripts/publish-muxcored-ghcr.sh v0.5.3
```

Requires `gh` token scopes `repo` + `write:packages`. Image build is verified locally as `localhost/muxcored:v0.5.0` (rebuild for v0.5.1 before publish); GHCR push is blocked until the token has packages write.

## Quick start

Guided host setup (recommended):

```bash
cd mvp   # or _mvp in older layouts
./setup.sh
```

Prompts for TMDB (live key or offline fixtures), admin credentials, library paths, and optional Jellyfin; writes `.env`, starts `./run-host.sh up`, bootstraps auth, and can run `./smoke.sh`.

Manual path:

```bash
cd mvp
cp .env.example .env
# edit TMDB_API_KEY=… (or TMDB_FIXTURE=1) and MVP_ADMIN_* as needed
```

### Docker Compose (preferred for containers)

```bash
docker compose up --build -d
./bootstrap-auth.sh
./smoke.sh
```

### Host binaries (no Docker)

```bash
(cd ../core && go build -o ../_mvp/bin/muxcored ./cmd/muxcored)
(cd ../api-rest && go build -o ../_mvp/bin/api-rest ./cmd/module)
(cd ../media-tvshows && go build -o ../_mvp/bin/media-tvshows ./cmd/module)
(cd ../admin-ui && npx --yes @tailwindcss/cli@4.1.6 -i ./input.css -o ./assets/dist/styles.css --minify && go build -o ../_mvp/bin/admin-ui .)
(cd ../auth-local && go build -o ../_mvp/bin/auth-local ./cmd/module)
(cd ../jellyfin && go build -o ../_mvp/bin/jellyfin ./cmd/module)
(cd ../media-scanner && go build -o ../_mvp/bin/media-scanner ./cmd/module)
(cd ../media-automation && go build -o ../_mvp/bin/media-automation ./cmd/module)
(cd ../downloader-native-torrent && go build -o ../_mvp/bin/downloader-native-torrent ./cmd/module)

./run-host.sh up
# Single-module ops (core stays up; clears stale mesh registration on stop/restart):
# ./run-host.sh stop-one media-scanner
# ./run-host.sh restart admin-ui
# ./run-host.sh unregister media-scanner
./bootstrap-auth.sh
./smoke.sh
./run-host.sh stop
```

Admin UI: `http://localhost:8082`. Jellyfin bridge HTTP: `http://127.0.0.1:8475/healthz` (optional `JELLYFIN_BASE_URL` + `JELLYFIN_API_KEY` for a live server).

Scanner watches `_mvp/data/downloads` and imports into `_mvp/data/library` (`SCANNER_IMPORT_MODE=copy`).

Automation: soft queue APIs plus **fixture Dispatch** via `DOWNLOADER_ENGINE=fixture` (default; writes a local `.mkv`, no BitTorrent). Admin UI `/automation` can Dispatch fixture or Search→best. For live torrents set `DOWNLOADER_ENGINE=anacrolix` + `PIRATEBAY_API_BASE` (VPN); smoke then skips fixture Dispatch and uses `liveacquisition` when `SMOKE_LIVE_ACQUISITION=1`. Host stack sets `MUXCORE_MESH_DIAL_LOCAL=true` and absolute `PUBLISH_POLICY_FILE` / `CALL_POLICY_FILE`.

### Smoke checks

1. Core `/health` 200  
2. Discovery resolve (incl. `media-tvshows`, `jellyfin`, `media-scanner`, `media-automation`, `downloader-native-torrent`)  
3. Bearer `/api/v1/modules`  
4. AddMovie / AddTVShow  
5. Admin UI login via auth-local + `/modules` + `/dashboard/monitor` + `/automation` + `/jellyfin`  
6. Jellyfin `/healthz` + gRPC `Status` (soft OK when unconfigured)  
7. Jellyfin soft `UpsertItemLink` / `ListItemLinks` / `SyncLibrary` skip + fixture `POST /webhook` PlaybackStart  
8. Scanner `ImportPath` fixture → organized library file under `data/library/Movies/...`  
9. Automation queue soft (`AddToQueue` / `GetQueue`; `SearchItem` empty without indexer)  
10. Automation `Dispatch` → fixture download.completed → scanner import → history `completed`  
11. Health-monitor `ReportHealth` + HTTP `/status` + mesh fan-out of `module.degraded` (visible on admin-ui `/events?filter=health`)  
12. Media-ui SPA (`:5173`) auth + shell + `/api/movies` / stream / `/api/tv` via mediauiprox BFF (skip if not running)  
13. Media-ui → request-media: search + `POST /api/request` (`TMDB_FIXTURE=1` offline Fight Club hit, or live `TMDB_API_KEY`)  
14. Soft `/api/jellyfin/play` (200 linked / 404 unlinked or unconfigured)  
15. Optional live Jellyfin (`SMOKE_LIVE_JELLYFIN=1`, or auto when `JELLYFIN_BASE_URL` + `JELLYFIN_API_KEY` are set): Status configured + RefreshLibrary + SyncLibrary + sample PlayURL via `cmd/jellyfinlive`  
16. Optional live acquisition (`SMOKE_LIVE_ACQUISITION=1`): Apibay Search + real torrent Dispatch/progress (VPN; set `PIRATEBAY_API_BASE` + `DOWNLOADER_ENGINE=anacrolix`)

Consumer SPA source/build: **[`../media-ui-app/`](../media-ui-app/)** → org [`Muxcore-Media/media-ui-app`](https://github.com/Muxcore-Media/media-ui-app) (`dist-app`). Build with `(cd ../media-ui-app && npm ci && npm run build)`.

### Profiles

| Profile | Extra services |
|---------|----------------|
| *(default)* | platform + media path + admin-ui + jellyfin + scanner + downloader + automation + request-media + **media-ui** (host); indexer when `PIRATEBAY_API_BASE` set |
| `media-ui` | compose-only: consumer SPA + BFF on `:5173` |
| `acquisition` | indexer-piratebay (`PIRATEBAY_API_BASE`, default `https://apibay.org`) |

Polluted `media-ui/` dump is quarantined — shippable SPA is **`media-ui-app/`**. Operator admin remains `admin-ui`.

## Endpoints

| Service | Host port (default) |
|---------|---------------------|
| core HTTP | 8080 |
| admin-ui | 8082 |
| api-rest HTTP | 18080 |
| auth-local gRPC / HTTP | 9403 / 9401 |
| media-movies gRPC | 9420 |
| media-tvshows gRPC / HTTP | 9440 / 9450 |
| media-automation gRPC | 9460 |
| downloader gRPC | 9461 |
| media-scanner gRPC | 9470 |
| jellyfin gRPC / HTTP | 9475 / 8475 |
| health-monitor gRPC / HTTP | 9202 / 9203 |
| media-ui (consumer SPA) | 5173 |
| request-media HTTP / gRPC | 9380 / 9481 |
| media-root-folders gRPC | 9540 |
