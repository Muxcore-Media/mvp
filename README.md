# MuxCore MVP stack

Local reference compose for the media MVP path. Sibling clones under `/home/user/Projects/MuxCore` are the build context.

**P0 Media MVP status: met** (Wave 21 stack + smoke; Wave 22 packaging freeze). Operator URLs after `./run-host.sh up`: [`run/VIEW-ME.txt`](run/VIEW-ME.txt).

**Core pin:** published [`v0.6.15`](https://github.com/Muxcore-Media/core/releases/tag/v0.6.15) (release train `train-2026.10.3`; see umbrella `release-train.env`). Modules pinning without `replace` (`GOPRIVATE=github.com/Muxcore-Media/*`, `gh` HTTPS):

Historical wave pins (still accurate as of each wave; later patch tags supersede — see spool catalog **2.5.0**):
- **Waves 25–26 (core-adjacent):** `auth-local`, `call-policy-default`, `publish-policy-default`, `secrets-file`, `encryption-aesgcm`, `api-rest`, `jellyfin@v0.2.1`, `media-root-folders@v0.1.1`, `health-monitor`, `metadata-tmdb@v0.1.1`, `database-sqlite`, `secrets-vault`
- **Wave 27 (native media stack):** `contracts-media-admin@v0.1.0`, `contracts-notification@v0.1.1`, `media-movies@v0.1.1`, `media-tvshows@v0.1.1`, `media-scanner@v0.1.1`, `media-automation@v0.1.0`, `request-media@v0.2.2`, `notification-default@v0.1.0`, `admin-ui@v0.1.3`
- **Wave 28 (leaves + smoke helpers):** `media-rename@v0.2.2`, `media-ffprobe@v0.1.2`, `media-subtitles@v0.4.2`, `media-custom-formats@v0.1.2`. Host [`go.mod`](go.mod) smoke helpers pin published module tags (sibling replaces removed); local `replace => ../core*` kept for host convenience.
- **Wave 29 (non-host sibling pins):** `media-list-sync@v0.1.1`, `notification-apprise@v0.1.1`, `workflow-tapestry@v0.1.0` (not started by default `run-host.sh`). Polluted org `media-ui` dump archived; use `media-ui-app`.

**Consumer SPA:** [`Muxcore-Media/media-ui-app`](https://github.com/Muxcore-Media/media-ui-app) (private; host uses sibling `../media-ui-app/dist-app`).

Authoritative docs: [`../core.wiki/Getting-Started.md`](../core.wiki/Getting-Started.md), [`../core.wiki/Deployment.md`](../core.wiki/Deployment.md).  
Spool presets mirrored: [`../spool/tags/minimal.json`](../spool/tags/minimal.json), [`../spool/tags/media.json`](../spool/tags/media.json).

Operator references in this repo: [`PORTS.md`](PORTS.md) (default gRPC/HTTP ports), [`BFF-API.md`](BFF-API.md) (mediauiprox JSON contracts), [`tls/`](tls/) (mTLS staging + secret rotation).

## Prerequisites

- Docker Compose v2 **or** host Go toolchain (`run-host.sh`)
- Org repos cloned as siblings (same layout as this workspace)
- Dev default: `MUXCORE_INSECURE_DISABLE_TLS=true` via `./run-host.sh`. Staging mTLS: `./run-host-staging.sh` (see [`tls/MTLS-STAGING.md`](tls/MTLS-STAGING.md)).

## Non-developer install (registry compose)

**Primary non-dev path:** [`docker-compose.registry.yml`](docker-compose.registry.yml) pulls prebuilt images from `${MUXCORE_REGISTRY}` (default LAN registry `localhost:5000/muxcore`; `ghcr.io/muxcore-media` once images are published there). No sibling Go builds; no GHCR `write:packages`. Step-by-step: [`docs/PUBLIC-INSTALL.md`](docs/PUBLIC-INSTALL.md).

```bash
export MUXCORE_REGISTRY=localhost:5000/muxcore   # or ghcr.io/muxcore-media once published
export MUXCORE_IMAGE_TAG=v0.6.15
export DOWNLOADER_ENGINE=fixture
./scripts/gen-enrollment.sh   # household mesh enrollment secret + tokens into .env (ADR-0017)
docker compose -f docker-compose.registry.yml pull
docker compose -f docker-compose.registry.yml up -d
./scripts/smoke-registry.sh   # or ./smoke.sh (auto-detects registry mode)
```

Publish `muxcored` (podman or docker):

```bash
./local-registry.sh start                   # LAN registry on localhost:5000
./scripts/publish-muxcored-local.sh v0.6.15  # MUXCORE_REGISTRY defaults to localhost:5000/muxcore
```

Default compose (`docker compose up --build`) and `./run-host.sh` remain the developer paths. GHCR uses the same registry compose with `MUXCORE_REGISTRY=ghcr.io/muxcore-media`; publishing there (`publish-muxcored-ghcr.sh`) is blocked until a `write:packages` token exists.

## Kubernetes (Phase 3 scaffold)

Kustomize overlays + Helm chart for the **minimal platform** slice live under [`deploy/`](deploy/README.md):

```bash
kubectl apply -k deploy/kustomize/overlays/dev
# or
helm upgrade --install muxcore deploy/helm/muxcore -n muxcore --create-namespace
```

Images default to the LAN registry `localhost:5000/muxcore/*` at `household-manifest.yaml` `core_tag` (currently **v0.6.15**), matching Helm. GHCR (`ghcr.io/muxcore-media`) is optional once published. Host `./run-host.sh` remains the verified operator path.

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
# Dev container playback requires a shared token; save it in .env for restarts.
export TRANSCODER_HTTP_TOKEN="$(openssl rand -hex 32)"
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
# Or rebuild many peers to spool catalog tags: ./scripts/rebuild-catalog-peers.sh media-scanner api-rest
(cd ../media-automation && go build -o ../_mvp/bin/media-automation ./cmd/module)

./run-host.sh up
# Single-module ops (core stays up; clears stale mesh registration on stop/restart):
# ./run-host.sh stop-one media-scanner
# ./run-host.sh restart admin-ui
# ./run-host.sh status          # pidfiles + HTTP health
# ./run-host.sh cleanup-stale   # remove dead pidfiles after crash
# ./run-host.sh unregister media-scanner
# Pin scanner/automation/downloader to origin/main (refuses unpublished versions):
# ./scripts/install-origin-module.sh media-scanner --verify-only
./bootstrap-auth.sh
./smoke.sh
./run-host.sh stop
```

Admin UI: `http://localhost:8082`. Jellyfin bridge HTTP: `http://127.0.0.1:8475/healthz` (optional `JELLYFIN_BASE_URL` + `JELLYFIN_API_KEY` for a live server).

Scanner watches `_mvp/data/downloads` and imports into `_mvp/data/library` (`SCANNER_IMPORT_MODE=copy`).

Automation: soft queue APIs (`AddToQueue` / `GetQueue` / Search). Host stack sets `MUXCORE_MESH_DIAL_LOCAL=true` and absolute `PUBLISH_POLICY_FILE` / `CALL_POLICY_FILE`.

### Smoke checks

1. Core `/health` 200  
2. Discovery resolve (incl. `media-tvshows`, `jellyfin`, `media-scanner`, `media-automation`)  
3. Bearer `/api/v1/modules`  
4. AddMovie / AddTVShow  
5. Admin UI login via auth-local + `/modules` + `/dashboard/monitor` + `/automation` + `/jellyfin`  
6. Jellyfin `/healthz` + gRPC `Status` (soft OK when unconfigured)  
7. Jellyfin soft `UpsertItemLink` / `ListItemLinks` / `SyncLibrary` skip + fixture `POST /webhook` PlaybackStart  
8. Scanner `ImportPath` fixture → organized library file under `data/library/Movies/...`  
9. Automation queue soft (`AddToQueue` / `GetQueue`)
11. Health-monitor `ReportHealth` + HTTP `/status` + mesh fan-out of `module.degraded` (visible on admin-ui `/events?filter=health`)  
12. Media-ui SPA (`:5173`) auth + shell + `/api/movies` / stream / `/api/tv` via mediauiprox BFF (skip if not running). Before any stream step the smoke seeds an explicit `unrestricted` parental policy for its admin through userdata-local `PUT /api/parental-policy` (ADR-0030; host port `USERDATA_LOCAL_PORT`, default `9672`; override with `SMOKE_USERDATA_URL`), never overwriting a restricted one, and afterwards proves a fresh account with no policy gets `403 parental.policy_unconfigured` (ADR-0031; `scripts/lib/parental-smoke.sh`)  
13. Media-ui → request-media: search + `POST /api/request` (`TMDB_FIXTURE=1` offline Fight Club hit, or live `TMDB_API_KEY`)  
14. Soft `/api/jellyfin/play` (200 linked / 404 unlinked or unconfigured)  
15. Optional live Jellyfin (`SMOKE_LIVE_JELLYFIN=1`, or auto when `JELLYFIN_BASE_URL` + `JELLYFIN_API_KEY` are set): Status configured + RefreshLibrary + SyncLibrary + sample PlayURL via `cmd/jellyfinlive`  

Consumer SPA source/build: **[`../media-ui-app/`](../media-ui-app/)** → org [`Muxcore-Media/media-ui-app`](https://github.com/Muxcore-Media/media-ui-app) (`dist-app`). Build with `(cd ../media-ui-app && npm ci && npm run build)`.

### Profiles

| Profile | Extra services |
|---------|----------------|
| *(default)* | platform + media path + admin-ui + scanner + automation + request-media + **media-ui** (host) |
| `jellyfin` | optional Jellyfin playback bridge (`:9475` gRPC / `:8475` HTTP); `MVP_ENABLE_JELLYFIN=1` on run-host |
| `plex` | optional Plex playback bridge (`:9476` gRPC / `:8476` HTTP); `MVP_ENABLE_PLEX=1` on run-host |
| `emby` | optional Emby playback bridge (`:9477` gRPC / `:8477` HTTP); `MVP_ENABLE_EMBY=1` on run-host |
| `observability` | compose-only: `metrics-prometheus` (`:9901` scrape) + `tracing-otlp` (slog fallback unless `OTEL_EXPORTER_OTLP_ENDPOINT` set) |
| `media-ui` | compose-only: consumer SPA + BFF on `:5173` |
| `cache-local` | optional process-local cache (`:9602`); `MVP_ENABLE_CACHE_LOCAL=1` on run-host |
| `secrets-vault` | use the final `docker-compose.secrets-vault.yml` overlay, **not** the additive profile; host flag `MVP_ENABLE_SECRETS_VAULT=1` selects Vault instead of file. [Switching/rollback](docs/INFRA-BACKENDS.md#secrets-file--secrets-vault). |

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
| plex gRPC / HTTP | 9476 / 8476 |
| emby gRPC / HTTP | 9477 / 8477 |
| health-monitor gRPC / HTTP | 9202 / 9203 |
| media-ui (consumer SPA) | 5173 |
| request-media HTTP / gRPC | 9380 / 9481 |
| media-root-folders gRPC | 9540 |
