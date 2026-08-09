# MuxCore (Muxcore-Media) Repository Status Report

Generated: 2026-08-08 (Wave 30 update 2026-08-09)  
Scope: all cloned org repos under `/home/user/Projects/MuxCore` (**62**), excluding `claude-working-directory`.

## Executive summary

MuxCore is a Go modular media platform: a `core` runtime plus many capability sidecars (auth, cache, media, contracts, observability). Most modules sit at **v0.1.x active development** with CI/Docker scaffolding; few look production-hardened.

### P0 Media MVP — **met** (2026-08-08)

The acquire → library → admin/consumer UI path is closed on the host reference stack ([`_mvp/`](_mvp/)): auth, request/search, fixture (and optional live) acquisition, scanner import, admin-ui, media-ui-app playback, health mesh, notifications. Operators: [`_mvp/README.md`](_mvp/README.md) + [`_mvp/run/VIEW-ME.txt`](_mvp/run/VIEW-ME.txt).

**Packaging (Waves 24–29):** [`core@v0.5.0`](https://github.com/Muxcore-Media/core/releases/tag/v0.5.0) (nested tags `pkg/contracts|sdk/go/module|sdk/go/client`); consumer SPA [`media-ui-app`](https://github.com/Muxcore-Media/media-ui-app); **all MVP Go modules pin published tags with no sibling `replace`** (Waves 25–28); sibling extras `media-list-sync`, `notification-apprise`, `workflow-tapestry` pinned (Wave 29); `_mvp` smoke helpers pin published tags (local `replace => ../core*` kept). Host smoke **PASS**.

**Wave 30 (2026-08-09):** Live Apibay + anacrolix on VPN (**PASS** — search/dispatch/history completed; ~2.4 GiB Fight Club tree under `_mvp/data/downloads`); consumer `media-ui-app` session APIs **PASS**; `core@v0.5.0` release assets uploaded (linux/darwin amd64/arm64 + checksums). GHCR image push still blocked (no Docker here; token lacks `packages` scope). [media-automation#17](https://github.com/Muxcore-Media/media-automation/pull/17) / tag `v0.1.1` — non-blocking Dispatch during large ImportPath.

**Remaining packaging:** org-wide core-only pin batch (~20 non-MVP modules still using `replace => ../core`); restore GitHub Actions billing so Dependabot/CI can run without admin-merge; GHCR `muxcored` image; admin-ui `/automation` page hang under load.

**Polluted dumps archived:** `cache-memory`, `custom-scripts`, `media-jellyfin`, `muxcorectl`, `media-ui` (canonical SPA is `media-ui-app`). Tracked ELF binaries in those dumps stay frozen under archive. `cache.memory` capability lives in `cache-local`. `spool` is a JSON tag catalog (no `go.mod`).

### Status counts (workspace inventory; historical)

| Status | Count | Notes |
|--------|------:|-------|
| Production-ready | 2 | `auth-local`, `encryption-aesgcm` |
| Active development | ~40 | MVP + platform sidecars; many now pin `core@v0.5.0` |
| Early/scaffold | ~9 | e.g. `auth-oidc`, `media-transcoder`, `muxcore-module-starter` |
| Broken/incomplete | ~8 | e.g. `circuitbreaker-simple`, `metrics-prometheus`, `tracing-otlp` (dumps archived separately) |
| Empty | 1 | `database-postgres` |
| Archived dumps | 5 | `cache-memory`, `custom-scripts`, `media-jellyfin`, `muxcorectl`, `media-ui` |

### Org-wide critical themes (do these next)

1. **Org-wide core-only pin** — drop `replace => ../core*` on remaining non-MVP modules (`backup-local`, `cache-*`, `logging-file`, `scheduler-cron`, …) using the Wave 25–29 HTTPS/`GOPRIVATE` pattern.
2. **Unblock Actions billing** — queued CI blocks Dependabot and forces admin-merge; local `go test`/`go build` remains the gate until spend is restored.
3. **Stand up `database-postgres`** — empty remote today; required if Postgres is the intended production store.
4. **Fix default port collisions** — e.g. historical overlaps on `:9480` / `:9460` (host stack already remaps; module defaults still diverge).
5. **Release hygiene** — CHANGELOGs stuck on “Unreleased” / “initial scaffold” while features already shipped.

### Suggested priority lanes

| Lane | Goal | Key repos |
|------|------|-----------|
| P0 Platform | Bootable mesh | `core`, `spool`, `api-rest`, `auth-local`, `call-policy-default`, `publish-policy-default` |
| P0 Media | Acquire → library → UI | `media-automation`, `media-scanner`, `media-movies`, `media-tvshows`, `metadata-tmdb`, `media-ui-app`, `admin-ui` |
| P1 Contracts | Stable module APIs | `contracts-*`, `contracts-reconciler` |
| P1 Data | Persistence & secrets | `database-sqlite` → `database-postgres`, `secrets-vault`, `encryption-aesgcm` |
| P2 Observability | Ops readiness | `metrics-prometheus`, `tracing-otlp`, `health-monitor`, `logging-file` |
| P3 Cleanup | Hygiene | empty/scaffold modules; org-wide pin; Actions billing |

---

## Wave 2 outcomes (2026-08-07)

MVP path: wiki + runnable stack + GHCR publish fix.

### Wiki
- Cloned [`core.wiki`](core.wiki) (`https://github.com/Muxcore-Media/core.wiki.git`) beside `core` — Getting-Started / Deployment / Configuration-Reference drive stack env (`MUXCORE_INSECURE_DISABLE_TLS`, `:8080` HTTP, `:9090` gRPC).

### GHCR lowercase
- Org-wide CI/release fix: set `IMAGE_NAME`/`IMAGE_OWNER` via `${GITHUB_REPOSITORY,,}` before docker push (mixed-case `Muxcore-Media` was rejected by GHCR).
- Branch `fix/ghcr-lowercase-image` opened across ~49 module repos; majority merged after green checks.

### MVP stack (`_mvp/`)
- [`_mvp/docker-compose.yml`](_mvp/docker-compose.yml) — platform + media path (profiles: `tv`, `ui`, `acquisition`, `jellyfin`); builds from sibling clones via [`_mvp/dockerfiles/module.Dockerfile`](_mvp/dockerfiles/module.Dockerfile).
- [`_mvp/run-host.sh`](_mvp/run-host.sh) — host binary runner when Docker is unavailable (used successfully in this environment).
- [`_mvp/smoke.sh`](_mvp/smoke.sh) — discovery `Resolve` for required module IDs (**PASS** on host stack). Extended in Wave 3 for auth + movies.
- Note: Wave 2 smoke used gRPC discovery; Wave 3 adds bearer `/api/v1/modules`.

### Still out of scope
- Real torrent/Pirate Bay E2E; tagged `core` publish to drop `replace`; `database-postgres`; Dependabot backlog.

## Wave 3 outcomes (2026-08-07)

Auth bootstrap + authenticated API smoke + storage health on the MVP host stack.

### Storage health
- Set `MUXCORE_STORAGE_DIR` in [`_mvp/run-host.sh`](_mvp/run-host.sh), compose, and [`.env.example`](_mvp/.env.example) so core registers the built-in local FS provider.
- Core [#30](https://github.com/Muxcore-Media/core/pull/30): `_`-prefixed health probe keys are informational only — `GET /health` returns **HTTP 200** with `status:ok`.

### Auth bootstrap
- [`_mvp/bootstrap-auth.sh`](_mvp/bootstrap-auth.sh) + `authctl` / [`_mvp/cmd/gettoken`](_mvp/cmd/gettoken) → `run/admin.token` (dev defaults `admin` / `admin-dev-only`).

### Identity forwarding (local fixes)
- **core** [`#31`](https://github.com/Muxcore-Media/core/pull/31): `SidecarIdentityProvider` populates `ExtractIdentityRequest` from gRPC `authorization` / `x-caller-id` metadata (was sending an empty request).
- **api-rest** [`#3`](https://github.com/Muxcore-Media/api-rest/pull/3): `BearerAuth` appends `authorization` to outgoing gRPC metadata so core can resolve the session on lifecycle RPCs.

### Smoke (**PASS**)
- [`_mvp/smoke.sh`](_mvp/smoke.sh): health 200 → discovery resolve → bearer `GET /api/v1/modules` → register library root + `AddMovie`/`ListMovies` (TMDB 550).
- Helpers: [`_mvp/cmd/addmovie`](_mvp/cmd/addmovie), [`_mvp/cmd/listmodules`](_mvp/cmd/listmodules).

### Still out of scope
- Live indexer/downloader E2E; tagging/publishing `core`; softening the storage health probe; api-rest proxying movie RPCs.

## Wave 4 outcomes (2026-08-07)

TV library path on the MVP stack + landed identity PRs.

### PRs
- **api-rest** [#3](https://github.com/Muxcore-Media/api-rest/pull/3) — merged (forward bearer on outbound core gRPC).
- **core** [#31](https://github.com/Muxcore-Media/core/pull/31) — merged (SidecarIdentityProvider metadata → ExtractIdentity). Note: GitHub Actions runners for `core` were stuck `queued` org-wide; merge proceeded after local verification.

### media-tvshows in default MVP
- [`_mvp/run-host.sh`](_mvp/run-host.sh) starts `media-tvshows` (`:9440` / `:9450`).
- Compose: `media-tvshows` moved out of the `tv` profile into the default stack.
- [`_mvp/cmd/addtvshow`](_mvp/cmd/addtvshow) + smoke: register TV root + `AddTVShow`/`ListTVShows` (Breaking Bad / TMDB 1396).

### Smoke (**PASS**)
- health → discovery (incl. media-tvshows) → bearer modules → AddMovie → AddTVShow.

### Still out of scope
- Live acquisition E2E; media-ui operator path; jellyfin bridge smoke; tag/publish `core`.

## Wave 5 outcomes (2026-08-07)

Admin UI on the MVP stack + login smoke.

### admin-ui
- Host/compose default service on **`:8082`** (`ADMIN_UI_INSECURE=true`, auth at `:9401`).
- Fixed Go 1.22+ ServeMux panics: nested `/media/.../item/{id}` and `/formats/item/{id}` — [admin-ui#9](https://github.com/Muxcore-Media/admin-ui/pull/9) (**merged**; Actions blocked by org billing).

### auth-local
- Widened login `safeRedirect` allowlist for local admin callbacks (`127.0.0.1` / `:18180`) — [auth-local#17](https://github.com/Muxcore-Media/auth-local/pull/17) (**merged**).

### Smoke (**PASS**)
- Prior Wave 4 checks + admin-ui `/health` + password login via auth-local → session cookie → home **200**.

### Still out of scope
- Live acquisition E2E; media-ui (polluted workspace clone); live Jellyfin sync; tag/publish `core`; org Actions billing.

## Wave 6 outcomes (2026-08-07)

Jellyfin playback bridge on the MVP stack (soft/unconfigured OK).

### jellyfin
- Default in [`_mvp/run-host.sh`](_mvp/run-host.sh) + compose (`:9475` gRPC / `:8475` HTTP); data under `_mvp/data/jellyfin`.
- Helper [`_mvp/cmd/jellyfinstatus`](_mvp/cmd/jellyfinstatus) — gRPC `Status`.
- Smoke: `/healthz` 200 + `Status` (typically `configured=false` without `JELLYFIN_BASE_URL`/`JELLYFIN_API_KEY`).

### media-ui deferred
- Sibling `media-ui` is a polluted multi-repo dump (not a shippable SPA module). Operator path remains **admin-ui**.

### Smoke (**PASS**)
- Wave 5 checks + jellyfin healthz/Status.

### Still out of scope
- Live torrent/Pirate Bay E2E; live Jellyfin library sync; clean `media-ui` product; tag/publish `core`; restore GitHub Actions billing.

## Wave 7 outcomes (2026-08-07)

Fixture import path (downloads → scanner → library) without indexer/torrent/Pirate Bay.

### media-scanner
- Default host/compose: watch `_mvp/data/downloads`, library `_mvp/data/library`, `SCANNER_IMPORT_MODE=copy`, `SCANNER_MIN_VIDEO_BYTES=0`, gRPC `:9470`.
- [media-scanner#16](https://github.com/Muxcore-Media/media-scanner/pull/16) (**merged**, admin after local smoke; Actions billing still red) — on `Storage.Put` failure (e.g. Unauthenticated), fall back to local `placeFile`; auto-watch sets `library_path` from `SCANNER_LIBRARY_ROOT`.
- Helper [`_mvp/cmd/importscan`](_mvp/cmd/importscan) — AddWatchDir, write Fight Club fixture under downloads, `ImportPath`, `ListImported`, verify library file.

### Smoke (**PASS**)
- Wave 6 checks + scanner ImportPath fixture → `data/library/Movies/Fight Club (1999)/...mkv`.

### Still out of scope
- Live acquisition with real indexer/downloader (pirate testing deferred); full automation Dispatch E2E; live Jellyfin sync; clean `media-ui`; tag/publish `core`; restore GitHub Actions billing.

## Wave 8 outcomes (2026-08-07)

Automation soft path on the MVP stack (queue APIs only — no Dispatch, no indexer/torrent).

### media-automation
- Host/compose gRPC **`:9460`** published; `AUTOMATION_GRPC_ADDR` set in [`_mvp/run-host.sh`](_mvp/run-host.sh).
- Helper [`_mvp/cmd/automationqueue`](_mvp/cmd/automationqueue) — `AddToQueue` + `GetQueue` + soft-empty `SearchItem` (no indexer in default stack).
- Smoke does **not** call `Dispatch` (hard-requires downloader) and does **not** enable compose `acquisition` profile.

### Smoke (**PASS**)
- Wave 7 checks + automation queue soft.

### Still out of scope
- Live acquisition / Pirate Bay E2E; Dispatch→download→scanner; live Jellyfin sync; clean `media-ui`; tag/publish `core`; restore GitHub Actions billing.

## Wave 9 outcomes (2026-08-07)

Offline acquisition loop: automation `Dispatch` → fixture downloader → `download.completed` → scanner import (no pirate sites / no BitTorrent network).

### Fixture downloader
- [downloader-native-torrent#4](https://github.com/Muxcore-Media/downloader-native-torrent/pull/4) (**merged**) — `DOWNLOADER_ENGINE=fixture` writes parseable `.mkv` under `DOWNLOAD_DIR` and completes instantly.
- Default in host/compose (`:9461`, shared `_mvp/data/downloads`); `acquisition` profile now only adds `indexer-piratebay`.

### Event publish path
- [core#32](https://github.com/Muxcore-Media/core/pull/32) (**merged**) — allowlist `EventService/Publish`; attribute caller from `x-caller-id` / event source (not `_public`); SDK attaches `MUXCORE_MODULE_ID`.
- [publish-policy-default#17](https://github.com/Muxcore-Media/publish-policy-default/pull/17) (**merged**) — allow `downloader-native-torrent` + `media-automation` for `download.*`.
- Host: absolute `PUBLISH_POLICY_FILE` / `CALL_POLICY_FILE` (cwd-relative `policies.yaml` was failing silently).

### media-automation host fixes
- [media-automation#15](https://github.com/Muxcore-Media/media-automation/pull/15) (**merged**) — `MUXCORE_MESH_DIAL_LOCAL` + preserve explicit hosts; `AUTOMATION_EVENT_SUBSCRIBE_DELAY`.
- Helper [`_mvp/cmd/automationdispatch`](_mvp/cmd/automationdispatch) — Dispatch magnet/`dn=` + poll history until `completed`.

### Smoke (**PASS**)
- Wave 8 checks + Dispatch → history `completed` + library file present.

### Still out of scope
- Live Pirate Bay / real torrent E2E; live Jellyfin sync; clean `media-ui`; tag/publish `core`; restore GitHub Actions billing.

## Wave 10 outcomes (2026-08-07)

Harden default-stack `health-monitor` (was lowest MVP-path score ~40%).

### health-monitor
- Aggregation: expire reports as `stale` after `2 × HEALTH_MONITOR_INTERVAL` (host default `5s`).
- HTTP `GET /status` JSON (modules, degraded/stale counts, recent events); `PublishEvent` kept in an in-memory ring.
- Env: `HEALTH_MONITOR_HTTP_ADDR`, `HEALTH_MONITOR_INTERVAL`; version `0.1.1`.
- Helper [`_mvp/cmd/healthreport`](_mvp/cmd/healthreport) — `ReportHealth` + `PublishEvent` + assert `/status`.
- Host/compose publish `:9202`/`:9203`.

### media-automation (smoke reliability)
- Fixed `searchQueuedItems` holding an open SQLite cursor (`MaxOpenConns(1)`) across indexer RPCs, which starved `AddToQueue` mid-smoke with `DeadlineExceeded`. Rows are now buffered before search.

### Progress review
- Parallel subagent review of all **63** cloned repos → MVP % and completion % (see [Progress scores](#progress-scores-2026-08-07) below).

### Smoke (**PASS**)
- Wave 9 checks + health-monitor ReportHealth/`/status`.

### Still out of scope
- Live Pirate Bay / real torrent E2E; live Jellyfin sync; clean `media-ui`; tag/publish `core`; restore GitHub Actions billing; mesh event-bus fan-out from health-monitor (local ring only).

## Wave 11 outcomes (2026-08-07)

Jellyfin soft library-link + webhook path (still no live Jellyfin server).

### jellyfin
- `SyncLibrary` when unconfigured returns empty soft success with skip note in `errors` (was hard error).
- Helper [`_mvp/cmd/jellyfinlink`](_mvp/cmd/jellyfinlink) — `UpsertItemLink` → `ListItemLinks` → `Status` link count → soft `SyncLibrary` → fixture `POST /webhook` `PlaybackStart`.

### Smoke (**PASS**)
- Wave 10 checks + jellyfin soft link/webhook.

### Still out of scope
- Live Jellyfin `/Library` sync / Refresh; Pirate Bay / real torrent; clean `media-ui` SPA profile; tag/publish `core`; restore GitHub Actions billing.

## Wave 12 outcomes (2026-08-07)

Consumer `media-ui/ui` SPA on the MVP stack (ignores polluted parent dump).

### media-ui (ui/ only)
- Built Vite SPA → `media-ui/ui/dist-app`.
- Thin BFF [`_mvp/cmd/mediauiprox`](_mvp/cmd/mediauiprox) — static SPA + `/api/movies`/`/api/tv` via gRPC + image proxy; search/request soft stubs (no `request-media`).
- Host default (`MVP_ENABLE_MEDIA_UI=1`) on **`:5173`**; compose profile `media-ui` + [`_mvp/dockerfiles/media-ui.Dockerfile`](_mvp/dockerfiles/media-ui.Dockerfile).

### Smoke (**PASS**)
- Wave 11 checks + media-ui `/healthz` + SPA index + `/api/movies` (Fight Club) + `/api/tv`.

### Still out of scope
- Live Jellyfin refresh; Pirate Bay / real torrent; `request-media` search/request wiring; extract clean `media-ui` GitHub repo from dump; tag/publish `core`; Actions billing.

## Wave 13 outcomes (2026-08-08)

Wire `request-media` into the consumer path (media-ui BFF).

### request-media
- Host/compose default service (`:9380` HTTP / `:9481` gRPC); `MUXCORE_MESH_DIAL_LOCAL` for host dial of discovered modules.
- `findModuleAddr` rewritten to match media-automation host/Docker dial rules.
- Helper [`_mvp/cmd/mediarequest`](_mvp/cmd/mediarequest) — soft search + `POST /api/request` Fight Club + list requests via media-ui proxy.

### mediauiprox
- Proxies `/api/search`, `/api/request`, `/api/requests` → `request-media` (no more stubs).

### Smoke (**PASS**)
- Wave 12 checks + request soft (empty without `TMDB_API_KEY`) + request `status=added` via AddMovie fallback.

### Still out of scope
- Live TMDB search without API key; live Jellyfin refresh; extract clean `media-ui` repo; tag/publish `core`; Actions billing.

## Wave 14 outcomes (2026-08-08)

Live Apibay indexer + real BitTorrent (VPN up).

### indexer-piratebay
- Host starts when `PIRATEBAY_API_BASE` set (`.env`: `https://apibay.org`); compose `acquisition` profile wires the same.
- Live Search: 20 hits for “Fight Club 1999”.

### downloader-native-torrent
- `DOWNLOADER_ENGINE=anacrolix` (real engine) for live path; `fixture` remains default for offline smoke.
- Live Dispatch → torrent metadata → **≥64 KiB downloaded** with seeders (progress proven).

### Helper / smoke
- [`_mvp/cmd/liveacquisition`](_mvp/cmd/liveacquisition) — indexer Search → automation SearchItem → Dispatch best seeded magnet → poll progress.
- `SMOKE_LIVE_ACQUISITION=1` appends live section to [`_mvp/smoke.sh`](_mvp/smoke.sh).

### Smoke
- Fixture Dispatch auto-skipped when `DOWNLOADER_ENGINE≠fixture` (live engine cannot complete fixture magnets).
- Live helper: Apibay Search + Dispatch best-seeded magnet + **≥64 KiB** progress (**PASS**).
- Full `./smoke.sh` with `SMOKE_LIVE_ACQUISITION=1` + anacrolix: offline path soft-skips fixture Dispatch, then live section.

### Still out of scope
- Waiting for full multi-GB completes in CI; live Jellyfin refresh; extract clean `media-ui` repo; tag/publish `core`; Actions billing.

## Wave 15 outcomes (2026-08-08)

Keyless metadata smoke + spool catalog truth sync.

### metadata-tmdb
- `TMDB_FIXTURE=1` (or `TMDB_API_KEY=fixture`) serves offline Fight Club / Breaking Bad for Search/details — no network / no real key.
- Module **v0.1.1**; Health OK in fixture mode.
- Host/compose pass `TMDB_FIXTURE`; MVP `.env` enables fixture by default for local smoke.

### request-media / media-ui
- `mediarequest -require-search` (or `SMOKE_REQUIRE_TMDB_SEARCH=1`) asserts Fight Club search hit when fixture/key present.
- Smoke requires search when `TMDB_FIXTURE=1` or `TMDB_API_KEY` set.

### spool
- Catalog **2.1.1**: add `request-media` @ v0.2.0; bump `health-monitor` → v0.1.1; bump `metadata-tmdb` → v0.1.1.
- `tags/media.json` includes optional `request-media`; default/minimal pin health-monitor v0.1.1.

### Smoke (**PASS**)
- `/api/search?q=Fight+Club` → id 550 via fixture; request path unchanged.

### Still out of scope
- Real TMDB key in CI secrets; live Jellyfin refresh; extract clean `media-ui` repo; tag/publish `core`; Actions billing.

## Wave 16 outcomes (2026-08-08)

Operator visibility + scanner live-download hygiene.

### admin-ui
- Dashboard **Health Monitor** panel polls `GET /dashboard/monitor` → health-monitor `GET /status` (`ADMIN_UI_HEALTH_MONITOR_URL`, default `http://127.0.0.1:9203`).
- Shows aggregate status, reported modules, recent events.
- Smoke: authenticated `/modules` + `/dashboard/monitor` after login.

### media-scanner
- Explicit skip for incomplete downloads (`.part`, `.!qb`, `.!ut`, `.crdownload`, `.tmp`, `.download`) with debug log.
- Module **v0.1.1**.

### Smoke (**PASS**)
- Wave 15 checks + admin monitor panel + modules page.

### Still out of scope
- Live Jellyfin refresh; extract clean `media-ui` repo; tag/publish `core`; Actions billing.

## Wave 17 outcomes (2026-08-08)

Operator surfaces for automation queue + jellyfin bridge status.

### admin-ui
- **`/automation`** — `GetQueue` + recent `GetHistory` via `media.automation` capability; nav link.
- **`/jellyfin`** — soft `Status` + `ListItemLinks` via `playback.jellyfin`; shows unconfigured soft note.
- Host dial: `MUXCORE_MESH_DIAL_LOCAL=true` + `normalizeDialAddr` for `:port` discovery addrs.
- Depends on sibling `media-automation` + `jellyfin` protos (`replace` in go.mod).

### Smoke (**PASS**)
- Authenticated `/automation` (Fight Club in wanted queue) + `/jellyfin` (configured=no, item links listed).

### Still out of scope
- Live Jellyfin against a real server; extract clean `media-ui`; tag/publish `core`; Actions billing.

## Wave 18 outcomes (2026-08-08)

Restore offline complete path + operator Dispatch/Sync actions + contracts hygiene.

### Host / smoke
- Default `.env` back to **`DOWNLOADER_ENGINE=fixture`** (offline Dispatch → completed → import).
- Live path remains opt-in (`anacrolix` + `PIRATEBAY_API_BASE` + `SMOKE_LIVE_ACQUISITION=1`).
- Smoke again **PASS**es fixture Dispatch complete path.

### admin-ui
- `/automation` — **Fixture** / **Search** Dispatch buttons (`POST /automation/dispatch`).
- `/jellyfin` — **Sync library** button (`POST /jellyfin/sync`, soft when unconfigured).

### contracts-indexer / contracts-downloader
- Added `CHANGELOG.md` + `COMPATIBILITY.md` (v0.1.0).
- Spool catalog **2.1.2** lists both contracts @ v0.1.0.

### Smoke (**PASS**)
- Full offline path including Dispatch→completed→import + admin Dispatch/Sync UI affordances.

### Still out of scope
- Live Jellyfin against a real server; extract clean `media-ui`; tag/publish `core` / contracts git tags on GitHub; Actions billing.

## Wave 19 outcomes (2026-08-08)

Health-monitor mesh event fan-out + consumer search copy polish.

### health-monitor (**v0.1.2**)
- Dials core mesh; fans `module.*` / `health.*` events onto Events bus (degraded, stale, PublishEvent).
- `/status` includes `mesh_published` counter.
- Unit test for mesh stub on degraded transition.

### publish-policy-default
- Explicit rule: `health-monitor` → `module.*`, `health.*`.

### Smoke / helpers
- `_mvp/cmd/healthreport` subscribes to `module.degraded`, triggers degraded report, asserts mesh delivery.

### media-ui
- Movies/Home copy notes fixture/live TMDB search path.

### Spool
- Pins `health-monitor` **v0.1.2**.

### Smoke (**PASS**)
- Offline path + mesh fan-out assertion.

### Still out of scope
- Live Jellyfin against a real server; extract clean `media-ui` GitHub repo; tag/publish `core`; Actions billing.

## Wave 20 outcomes (2026-08-08)

Operator events UX for health mesh fan-out + media-ui packaging clarity.

### admin-ui Events
- Filter chips: All / Health / Degraded / Downloads.
- Highlights `module.degraded` / `module.stale` rows; distinct badges for `health.*` and `download.*`.
- Smoke: after healthreport, `/events?filter=health` contains `module.degraded`.

### media-ui packaging
- Root [`media-ui/README.md`](media-ui/README.md) quarantines the polluted parent dump.
- Canonical SPA docs in [`media-ui/ui/README.md`](media-ui/ui/README.md).

### Smoke (**PASS**)
- Wave 19 path + admin-ui events health filter.

### Still out of scope
- Live Jellyfin against a real server; extract `ui/` to a clean GitHub repo; tag/publish `core`; Actions billing.

## Wave 21 outcomes (2026-08-08)

Discovery polish for operator admin, notifications on the host MVP stack, and consumer/admin UX hardening from the live demo session.

### Core discovery
- `Members` refreshes the **local** node’s `Modules` / `ModuleHealth` via `moduleIDs()` (single-node MVP no longer returns an empty module list).
- Public allowlist: `Watch` (+ prior session `ListAll` / `FindByRole`) so admin Cluster SSE and ListAll work without bearer mesh identity.
- Unit test: `TestMembers_LocalModulesRefresh`.

### admin-ui
- Cluster SSE returns **500** (not a silent empty body) when Watch fails.
- `loggingResponseWriter` implements `Flush`/`Unwrap` so SSE is not blocked by request logging middleware.
- Modules HTMX self-`hx-get="/modules"` load loop removed; list uses discovery `ListAll`.
- Tailwind `assets/dist/styles.css` built/embedded (NixOS: `@tailwindcss/cli`).
- Smoke: CSS 200, `/dashboard/health` shows module ids, `/cluster/sse` `text/event-stream` + `cluster-update`.

### media-ui / media-movies / secrets
- mediauiprox auth-local login + session cookie; Fight Club stream via media-movies `GET /stream/movies/{id}` + `addmoviefile`.
- SPA **Logout** → `/logout`; dist-app rebuilt.
- Host `run-host.sh`: `SECRETS_KEY_FILE` / `ENCRYPTION_KEY_FILE` under `_mvp/data/`.

### notification-default
- Started on host MVP (`:9441`) with `WEBHOOK_URL` sink (no Discord/SMTP secrets).
- `listmodules` requires Resolve `notification-default`.

### request-media
- Added [`request-media/COMPATIBILITY.md`](request-media/COMPATIBILITY.md).

### Smoke (**PASS**)
- Prior path + CSS + dashboard health modules + cluster SSE (Flusher on logging writer) + media auth/stream + notification-default + mediarequest Netscape `#HttpOnly_` cookie jar.

### Still out of scope
- Live Jellyfin against a real server; extract clean `media-ui` GitHub repo; tag/publish `core`; Actions billing.

## Wave 22 outcomes (2026-08-08)

Post-MVP freeze + clean consumer SPA packaging (no GitHub remote create).

### MVP freeze
- Executive summary marks **P0 Media MVP met**; operators pointed at `_mvp/README.md` + `run/VIEW-ME.txt`.

### media-ui-app extract
- New sibling [`media-ui-app/`](media-ui-app/) — shippable Vite/React tree (README, LICENSE, `.gitignore`, CI stub `npm ci` / typecheck / build).
- Documents auth-local login, Logout, and `/stream` playback.
- Host + compose default dist: `media-ui-app/dist-app` ([`_mvp/run-host.sh`](_mvp/run-host.sh), [`_mvp/dockerfiles/media-ui.Dockerfile`](_mvp/dockerfiles/media-ui.Dockerfile)).
- Polluted [`media-ui/`](media-ui/) dump README updated; `media-ui/ui/` marked deprecated.

### Smoke
- Host media-ui path unchanged functionally; rebuild `media-ui-app` then `./smoke.sh`.

### Still out of scope
- Push `media-ui-app` as a GitHub repo; live Jellyfin; tag/publish `core`; Actions billing.

## Wave 24 outcomes (2026-08-08)

Core discovery MVP fixes landed on `master` and published as **`v0.5.0`**.

### Release
- Branch `release/v0.5.0-discovery` → PR [#33](https://github.com/Muxcore-Media/core/pull/33) → `master` (admin merge; Actions jobs stayed **queued** — local `go test ./internal/grpcmesh/ -count=1` PASS).
- Tag + GitHub Release: [`v0.5.0`](https://github.com/Muxcore-Media/core/releases/tag/v0.5.0) on merge commit `5f28214`.
- Module resolve: `go list -m github.com/Muxcore-Media/core@v0.5.0`.

### Discovery (in tag)
- Public allowlist: `ListAll`, `Watch`, `FindByRole` (with existing `Members` / `Resolve` / `FindByCapability`).
- Local `Members` refreshes `Modules` / `ModuleHealth` from `moduleIDs()`.
- Test: `TestMembers_LocalModulesRefresh`.

### Host MVP
- Rebuilt [`_mvp/bin/muxcored`](_mvp/bin/muxcored) from tagged tree.
- Pin docs: consumers may `go get github.com/Muxcore-Media/core@v0.5.0`; host `_mvp` may keep `replace => ../core` for convenience.

### Still out of scope
- Live Jellyfin (`SMOKE_LIVE_JELLYFIN`); drop `replace => ../core` across all sidecars; push `media-ui-app` GitHub remote; Actions billing unblock.

## Wave 25 outcomes (2026-08-08)

Publish consumer SPA, nested core module tags, pin pilot, host hygiene.

### media-ui-app
- Private repo [`Muxcore-Media/media-ui-app`](https://github.com/Muxcore-Media/media-ui-app) created via `gh` HTTPS (`git remote` = `https://github.com/Muxcore-Media/media-ui-app.git`).
- Polluted org [`media-ui`](https://github.com/Muxcore-Media/media-ui) left untouched.

### Nested core tags
- On commit `5f28214` / `v0.5.0`: `pkg/contracts/v0.5.0`, `sdk/go/module/v0.5.0`, `sdk/go/client/v0.5.0` (required for multi-module `go get`).

### Pin pilot (drop `replace => ../core*`)
| Module | PR |
|--------|-----|
| auth-local | [#18](https://github.com/Muxcore-Media/auth-local/pull/18) (+ [#19](https://github.com/Muxcore-Media/auth-local/pull/19) `:5173` redirect allowlist) |
| call-policy-default | [#17](https://github.com/Muxcore-Media/call-policy-default/pull/17) |
| publish-policy-default | [#18](https://github.com/Muxcore-Media/publish-policy-default/pull/18) |
| secrets-file | [#13](https://github.com/Muxcore-Media/secrets-file/pull/13) |
| encryption-aesgcm | [#3](https://github.com/Muxcore-Media/encryption-aesgcm/pull/3) |

- CI: `GOPRIVATE=github.com/Muxcore-Media/*` + HTTPS `MUXCORE_CI_TOKEN` (no sibling core checkout). Merged with admin where Actions stayed queued.

### Host hygiene
- Rebuilt `_mvp/bin/muxcored` from `v0.5.0` + five pinned module binaries; `./run-host.sh` restart.
- Smoke **PASS** (auth + movies + tv + admin-ui + jellyfin soft + scanner + automation + health-monitor + media-ui + request-media).

### Still out of scope
- Org-wide replace removal (media/jellyfin/api-rest/etc.); live Jellyfin; Actions billing spend.

## Wave 26 outcomes (2026-08-08)

Expand `core@v0.5.0` pin to seven more core-only MVP modules + restore host-smoke WIP that lived only in local stashes.

### Pin batch (drop `replace => ../core*`)
| Module | PR |
|--------|-----|
| api-rest | [#4](https://github.com/Muxcore-Media/api-rest/pull/4) |
| jellyfin | [#9](https://github.com/Muxcore-Media/jellyfin/pull/9) (+ [#10](https://github.com/Muxcore-Media/jellyfin/pull/10) soft SyncLibrary when unconfigured) |
| media-root-folders | [#4](https://github.com/Muxcore-Media/media-root-folders/pull/4) |
| health-monitor | [#12](https://github.com/Muxcore-Media/health-monitor/pull/12) (+ [#13](https://github.com/Muxcore-Media/health-monitor/pull/13) mesh fan-out + `/status`) |
| metadata-tmdb | [#5](https://github.com/Muxcore-Media/metadata-tmdb/pull/5) (+ [#6](https://github.com/Muxcore-Media/metadata-tmdb/pull/6)/[#7](https://github.com/Muxcore-Media/metadata-tmdb/pull/7) `TMDB_FIXTURE`) |
| database-sqlite | [#13](https://github.com/Muxcore-Media/database-sqlite/pull/13) |
| secrets-vault | [#10](https://github.com/Muxcore-Media/secrets-vault/pull/10) |

### Host hygiene
- Rebuilt seven host binaries; `MUXCORE_MESH_DIAL_LOCAL` on health-monitor in [`_mvp/run-host.sh`](_mvp/run-host.sh).
- Smoke **PASS**.

### Still out of scope
- Sibling-replace media stack (`media-movies`, `media-automation`, `admin-ui`, `request-media`, `notification-default`, …); live Jellyfin; Actions billing spend.

## Wave 27 outcomes (2026-08-08)

Native media-stack pin (no live Jellyfin, no Actions billing). Tag leaves → bottom-up pin PRs → host smoke.

### Phase A — leaf / contract tags
| Repo | Tag |
|------|-----|
| contracts-media-admin, contracts-downloader, contracts-indexer | `v0.1.0` |
| contracts-notification | `v0.1.1` |
| metadata-tmdb | `v0.1.1` |
| media-root-folders | `v0.1.1` |
| media-rename | `v0.2.1` |
| media-ffprobe | `v0.1.1` |
| media-subtitles | `v0.4.1` |
| media-custom-formats | `v0.1.1` |
| jellyfin | `v0.2.1` (dep pin only) |

### Phase B — bottom-up pin (drop all `replace`)
| Module | Pin PR | Tag |
|--------|--------|-----|
| downloader-native-torrent | [#5](https://github.com/Muxcore-Media/downloader-native-torrent/pull/5) | `v0.2.1` |
| media-movies | [#3](https://github.com/Muxcore-Media/media-movies/pull/3) (+ [#4](https://github.com/Muxcore-Media/media-movies/pull/4) `/stream/movies`) | `v0.1.0` → `v0.1.1` |
| media-tvshows | [#15](https://github.com/Muxcore-Media/media-tvshows/pull/15) | `v0.1.1` |
| media-scanner | [#17](https://github.com/Muxcore-Media/media-scanner/pull/17) | `v0.1.1` |
| media-automation | [#16](https://github.com/Muxcore-Media/media-automation/pull/16) | `v0.1.0` |
| request-media | [#4](https://github.com/Muxcore-Media/request-media/pull/4) (+ [#5](https://github.com/Muxcore-Media/request-media/pull/5) `MESH_DIAL_LOCAL`) | `v0.2.1` → `v0.2.2` |
| notification-default | [#4](https://github.com/Muxcore-Media/notification-default/pull/4) | `v0.1.0` |
| admin-ui | [#10](https://github.com/Muxcore-Media/admin-ui/pull/10) (+ [#11](https://github.com/Muxcore-Media/admin-ui/pull/11)/[#12](https://github.com/Muxcore-Media/admin-ui/pull/12) MVP surfaces; drop `node_modules`) | `v0.1.1` → `v0.1.3` |

CI: `GOPRIVATE` + HTTPS `MUXCORE_CI_TOKEN`; admin-merge when Actions stayed queued.

### Host hygiene
- Rebuilt pinned media-stack binaries under `_mvp/bin` from default branches.
- Smoke **PASS** (native/fixture path; soft jellyfin OK). Restored stash-only host surfaces that pin-from-main had dropped (admin monitor/automation/jellyfin UI, movies stream HTTP, request-media local dial).

### Still out of scope
- Live Jellyfin (`SMOKE_LIVE_JELLYFIN`); Actions billing; pinning non-MVP org modules.

## Wave 28 outcomes (2026-08-08)

Finish MVP leaf + indexer pin; pin `_mvp` smoke helpers to published tags.

### Phase A — media leaves (drop `replace`, `core@v0.5.0`)
| Module | PR | Tag |
|--------|-----|-----|
| media-rename | [#3](https://github.com/Muxcore-Media/media-rename/pull/3) | `v0.2.2` |
| media-ffprobe | [#4](https://github.com/Muxcore-Media/media-ffprobe/pull/4) | `v0.1.2` |
| media-subtitles | [#4](https://github.com/Muxcore-Media/media-subtitles/pull/4) | `v0.4.2` |
| media-custom-formats | [#4](https://github.com/Muxcore-Media/media-custom-formats/pull/4) | `v0.1.2` |

### Phase B — indexer
| Module | PR | Tag |
|--------|-----|-----|
| indexer-piratebay | [#4](https://github.com/Muxcore-Media/indexer-piratebay/pull/4) (+ [#5](https://github.com/Muxcore-Media/indexer-piratebay/pull/5) version pins) | `v0.1.1` |

### Phase C — `_mvp` smoke module
- [`_mvp/go.mod`](_mvp/go.mod): requires published tags (`core@v0.5.0`, contracts `@v0.1.0`, media stack / jellyfin Wave 27 tags); dropped sibling `replace`s; kept local `replace => ../core*`.
- Rebuilt `_mvp/bin` smoke helpers; host restart.

### Host hygiene
- Smoke **PASS** (native/fixture path; soft jellyfin OK).

### Still out of scope
- Live Jellyfin (`SMOKE_LIVE_JELLYFIN`); Actions billing; pinning non-MVP org modules.

## Wave 29 outcomes (2026-08-08)

Sibling pins for remaining complex modules + org hygiene (archive polluted consumer dump; refresh executive summary).

### Phase A — sibling / workflow pins
| Module | PR | Tag |
|--------|-----|-----|
| media-list-sync | [#3](https://github.com/Muxcore-Media/media-list-sync/pull/3) | `v0.1.1` |
| notification-apprise | [#3](https://github.com/Muxcore-Media/notification-apprise/pull/3) | `v0.1.1` |
| workflow-tapestry | [#4](https://github.com/Muxcore-Media/workflow-tapestry/pull/4) | `v0.1.0` |

Pins: `core@v0.5.0` (+ nested); list-sync also pins media stack tags; apprise pins `contracts-notification@v0.1.1`.

**Spool:** [`Muxcore-Media/spool`](https://github.com/Muxcore-Media/spool) healthy, **no `go.mod`** (JSON tag catalog: `minimal` / `media` / `acquisition`) — no pin PR.

### Phase B — hygiene
- Verified already archived: `cache-memory`, `custom-scripts`, `media-jellyfin`, `muxcorectl`.
- Archived polluted [`media-ui`](https://github.com/Muxcore-Media/media-ui) dump; canonical SPA remains [`media-ui-app`](https://github.com/Muxcore-Media/media-ui-app).
- Executive summary rewritten for Waves 24–29 reality.

### Still out of scope
- Org-wide core-only pin batch; Actions billing.

## Wave 30 outcomes (2026-08-09)

Live Pirate Bay/torrent (VPN), consumer UI verification, and core release binaries.

### Live acquisition
- Host `.env`: `PIRATEBAY_API_BASE=https://apibay.org`, `DOWNLOADER_ENGINE=anacrolix`, `SMOKE_LIVE_ACQUISITION=1`.
- Apibay Search + automation `SearchItem` + `Dispatch` **PASS** (VPN IP confirmed; indexer `configured=true`).
- ~2.4 GiB release materialized under `_mvp/data/downloads/Fight Club 1999 BDRip…`; history reached `completed`.
- [media-automation#17](https://github.com/Muxcore-Media/media-automation/pull/17) (**merged**, tag `v0.1.1`) — Dispatch no longer blocks for minutes while ImportPath copies large trees.
- `liveacquisition` prefers `-min-seeders` (smoke defaults documented in `.env.example`).

### media-ui (consumer)
- Polluted org `media-ui` stays archived; shippable SPA is **`media-ui-app`** on `:5173` via `mediauiprox`.
- Verified: unauth `/api/movies` → 401; password login → `/api/movies` + `/api/tv` + index **200**.

### core tag/publish
- Tag **`v0.5.0`** already on `master` (`5f28214`); nested module tags already published.
- Local GoReleaser → uploaded release assets: linux/darwin amd64/arm64 tarballs + checksums — [release](https://github.com/Muxcore-Media/core/releases/tag/v0.5.0).
- **Not done:** GHCR `ghcr.io/muxcore-media/muxcored:v0.5.0` (no Docker on this host; `gh` token lacks `packages` scope). Actions Release job still blocked by org billing/runners.

### Still out of scope / follow-ups
- GHCR image push; Actions billing; admin-ui `/automation` page hang (curl stalls); org-wide remaining `replace => ../core` pins; live Jellyfin.

## Progress scores (2026-08-07)

Scoring: **MVP %** = readiness for media MVP path (off-path non-blocking → 100). **Completion %** = vs stated purpose / production readiness.

| Repo | MVP % | Completion % |
|------|------:|-------------:|
| admin-ui | 98 | 90 |
| api-rest | 90 | 58 |
| auth-local | 95 | 88 |
| auth-oidc | 100 | 48 |
| backup-local | 100 | 72 |
| cache-local | 100 | 58 |
| cache-memory | 100 | 5 |
| cache-redis | 100 | 62 |
| call-policy-default | 88 | 72 |
| circuitbreaker-simple | 100 | 58 |
| config-watcher | 100 | 65 |
| contracts-downloader | 90 | 72 |
| contracts-indexer | 90 | 72 |
| contracts-media-admin | 85 | 68 |
| contracts-notification | 100 | 42 |
| contracts-reconciler | 100 | 58 |
| core | 92 | 75 |
| core.wiki | 95 | 88 |
| custom-scripts | 100 | 0 |
| database-postgres | 100 | 0 |
| database-sqlite | 90 | 68 |
| data-redaction-pattern | 100 | 58 |
| distributed-lock-sqlite | 100 | 55 |
| downloader-native-torrent | 90 | 85 |
| encryption-aesgcm | 95 | 90 |
| executor-shell | 100 | 72 |
| feature-flags-file | 100 | 55 |
| health-monitor | 95 | 78 |
| indexer-piratebay | 92 | 85 |
| input-validate-jsonschema | 100 | 72 |
| jellyfin | 92 | 90 |
| logging-file | 100 | 75 |
| media-automation | 92 | 85 |
| media-custom-formats | 100 | 72 |
| media-ffprobe | 100 | 68 |
| media-jellyfin | 100 | 0 |
| media-list-sync | 100 | 60 |
| media-movies | 93 | 82 |
| media-rename | 100 | 72 |
| media-root-folders | 95 | 85 |
| media-scanner | 92 | 80 |
| media-subtitles | 100 | 55 |
| media-transcoder | 100 | 45 |
| media-tvshows | 92 | 78 |
| media-ui | 100 | 88 |
| metadata-tmdb | 95 | 80 |
| metrics-prometheus | 100 | 60 |
| muxcorectl | 5 | 8 |
| muxcore-module-starter | 100 | 40 |
| notification-apprise | 100 | 72 |
| notification-default | 100 | 85 |
| publish-policy-default | 96 | 78 |
| ratelimit-tokenbucket | 100 | 58 |
| request-media | 96 | 86 |
| scheduler-cron | 100 | 65 |
| secrets-file | 96 | 88 |
| secrets-vault | 100 | 78 |
| serialization-safe | 100 | 72 |
| spool | 94 | 75 |
| spool-resolver-http | 100 | 48 |
| tracing-otlp | 100 | 58 |
| worker-pool-memory | 100 | 38 |
| workflow-tapestry | 100 | 72 |

Note: `health-monitor` bumped from review-time ~40/25 after Wave 10 (aggregation + `/status` + smoke). Mesh bus publish still deferred.

## Wave 1d outcomes (2026-08-07)

Closed the last Wave 1 leftovers after sibling CI landed on default branches.

### Merged
| Track | Result |
|-------|--------|
| Last CI sibling | [contracts-reconciler#2](https://github.com/Muxcore-Media/contracts-reconciler/pull/2) — switched lint to `go vet` (golangci v2 config debt deferred) |
| Optional port collisions | **8** merged after merging default into each `fix/grpc-port-collision` branch: `auth-local`, `distributed-lock-sqlite`, `metadata-tmdb`, `notification-default`, `request-media`, `secrets-file`, `secrets-vault`, `tracing-otlp` |

### Remaining (not Wave 1)
- Dependabot PRs (need re-run against new default-branch CI).
- golangci-lint v2 config migration org-wide.
- Tag/publish `core` to drop `replace` + `MUXCORE_CI_TOKEN`.

## Wave 1c outcomes (2026-08-07)

Unblocked remaining red CI via [core#26](https://github.com/Muxcore-Media/core/issues/26) (now **closed**).

### Core merges
| PR | What |
|----|------|
| [core#27](https://github.com/Muxcore-Media/core/pull/27) | Missing `proto/muxcore/*/v1` + `proto/gen`, media/download event contracts, `SettingDef.Value`, SDK `RegisterMeshHandler` / `SettingsHandler` / `MaskSecret` / `NewGRPCServer` |
| [core#28](https://github.com/Muxcore-Media/core/pull/28) | `FileImportedPayload.Year` → `int32` |
| [core#29](https://github.com/Muxcore-Media/core/pull/29) | TV fields: `EpisodeNumbers`, `AbsoluteNumber`, `AirDate`; season/episode as `int32` |

### Module CI (sibling checkout)
- Merged the bulk of remaining green CI sibling PRs after core landed (~24 in the post-#27 batch, then `media-movies` / `media-tvshows`).
- Follow-up fixes merged on the same branch where needed: `go mod tidy` (`media-list-sync`, `request-media`, `muxcore-module-starter`, `metrics-prometheus`), ratelimit `NewModule(Config{})` tests, downloader AddTorrent race under `-race`.
- **Only CI sibling PR still open/red:** [contracts-reconciler#2](https://github.com/Muxcore-Media/contracts-reconciler/pull/2) — golangci debt (errcheck/staticcheck/unused), not missing core APIs.

### Still open (not CI-sibling)
- Optional **gRPC port-collision** PRs (~8) — red until rebased onto sibling-checkout workflows (required ports already merged in Wave 1b).
- Large backlog of **Dependabot** PRs still red; should start clearing once default-branch CI is green.

### Follow-ups
- golangci-lint v2 config migration (drop `go vet` workaround).
- Tag/publish `core` to remove `replace => ../core` and `MUXCORE_CI_TOKEN`.
- Rebase or close optional port PRs; rotate CI token to a dedicated PAT/App.

## Wave 1b outcomes (2026-08-07)

Continuation after Wave 1 PR flood: merge safe work and park product blockers.

### Merged
| Track | Count / notes |
|-------|----------------|
| Quarantine READMEs | 4 — then **archived** `cache-memory`, `custom-scripts`, `media-jellyfin`, `muxcorectl` |
| Binary purge | 5 — `auth-local`, `call-policy-default`, `publish-policy-default`, `scheduler-cron`, `worker-pool-memory` |
| Required port fixes | 2 — `indexer-piratebay` `:9485`, `downloader-native-torrent` `:9461` |
| Green CI PRs | **21** merged (sibling checkout + token + go vet), including `api-rest`, `auth-local`, `admin-ui`, `auth-oidc`, `call-policy-default`, `publish-policy-default`, several media helpers, contracts (lint/test/build) |

### Closed / tracking
- [core#25](https://github.com/Muxcore-Media/core/issues/25) closed (archives done).
- [core#26](https://github.com/Muxcore-Media/core/issues/26) opened: missing `core/proto/gen` packages (`cache`, `serialization`, `database`, `healthmonitor`, `tracing`, `logging`, …) + SDK/contracts API drift block remaining red CI PRs.

### Still open (superseded by Wave 1c)
Was ~40 red CI PRs waiting on core#26. See **Wave 1c** — almost all sibling CI PRs merged; only `contracts-reconciler` + optional port/Dependabot PRs remain.

## Wave 1 outcomes (2026-08-07)

Parallel subagent execution of org-multiplier fixes. **PRs are open — not merged** (awaiting review).

### Delivered

| Track | Result |
|-------|--------|
| **CI unblock** | **54** open PRs (`fix/ci-sibling-checkout-go126`): sibling checkouts for `replace => ../…`, `MUXCORE_CI_TOKEN` for private `core`/sibling clones, lint via `go vet` (golangci-lint v2 rejects unversioned `.golangci.yml`), test timeout **10m**. Tooling in [`_wave1/`](_wave1/). |
| **Binaries** | **5** open PRs (`fix/remove-committed-binaries`): `auth-local`, `call-policy-default`, `publish-policy-default`, `scheduler-cron`, `worker-pool-memory`. |
| **Ports** | **10** open PRs: required pairs fixed (`indexer-piratebay` `:9480`→`:9485`, `downloader-native-torrent` `:9460`→`:9461`) plus additional duplicate-default cleanup. |
| **Polluted repos** | **4** quarantine README PRs + tracking issue [Muxcore-Media/core#25](https://github.com/Muxcore-Media/core/issues/25) (archive recommendation). |

### CI health (sampled ~40 CI PRs after token + vet fix)

- **Fully green** (examples): `api-rest`, `auth-local`, `admin-ui`, `worker-pool-memory`, `scheduler-cron`, `media-ffprobe`, `media-rename`, `media-root-folders`, `media-subtitles`, `media-transcoder`, `media-custom-formats`, `input-validate-jsonschema`, …
- **Still red / partial** (mostly pre-existing product gaps, not workflow wiring): multi-sibling media stack missing generated protos in `core` (e.g. `cache/v1`), `serialization-safe`, `jellyfin`/`media-automation`/`media-scanner` graph, contracts `proto-check`, golangci v1→v2 config migration deferred.

### Follow-ups (not Wave 1)

1. Migrate `.golangci.yml` to `version: "2"` and restore golangci-lint-action@v9.
2. Publish/tag `core` (and missing `proto/gen` packages) so modules can drop `replace` and lose `MUXCORE_CI_TOKEN` dependency.
3. Merge binary + port + quarantine PRs; archive the four workspace dumps.
4. Rotate `MUXCORE_CI_TOKEN` to a dedicated fine-grained PAT / GitHub App (currently set per-repo for CI).

---

## Per-repository status

## Platform / Core

### `admin-ui`
- **Status:** Active development — Wave 16–17 operator surfaces: health-monitor panel, `/automation` queue, `/jellyfin` status
- **Maturity:** Strong for a UI sidecar: Go 1.26, tests, multi-job CI, Dockerfile; no `muxcore.json` / COMPATIBILITY; no release tags
- **Last activity:** 2026-08-08 — Wave 17 automation + jellyfin pages + smoke
- **Critical tasks:**
  - Add `muxcore.json` (+ COMPATIBILITY); cut tagged `v0.x` release
  - Expand handler coverage; keep CI green when Actions billing restored

### `api-rest`
- **Status:** Active development — thin but real REST gateway (`rest.api` / `auth.delegate`) with auth/CORS hardening on 2026-07-22.
- **Maturity:** v0.1.0 in `muxcore.json`, CI (lint/test/build), 3 test files, Dockerfile/Makefile; no CHANGELOG/COMPATIBILITY; CI still on older actions (`checkout@v4`, `setup-go@v5`, golangci-lint-action v6).
- **Last activity:** 2026-07-22 — auth/CORS middleware and module hardening; 2026-07-21 README slim; 2026-06-24 initial commit.
- **Critical tasks:**
  - Add CHANGELOG + COMPATIBILITY (and bump CI actions to match sibling modules) before treating as shippable.
  - Broaden tests past middleware/module smoke — `handler.go` exposes modules/storage/audit/events/spools/cluster with little route-level coverage.
  - Document and enforce auth delegation end-to-end against `auth-local`/`auth-oidc` (capability claims `auth.delegate`).
  - Decide public surface completeness vs README “modules, storage, audit, events, …” — verify each handler path against core APIs and gap-fill.

### `core`
- **Status:** Active development — platform “loom” marked **pre-1.0 beta** in README; largest codebase (~33k LoC Go, 67 `*_test.go`), but **master code idle since 2026-06-12** while modules advanced through July.
- **Maturity:** Strong: CHANGELOG through v1.0.0-rc.1 notes, COMPATIBILITY.md, multi-workflow CI (self-hosted), fuzz/security work, wiki-oriented docs; **no GitHub Releases/tags** on the public repo; open PRs are mostly stale Dependabot bumps; Dependabot CI often cancelled (~24h) or failed.
- **Last activity:** 2026-06-12 — CI pipeline fix commit; Dependabot graph/update noise through 2026-08-03 without product merges.
- **Critical tasks:**
  - Unblock self-hosted CI for Dependabot/PRs (cancelled 24h runs) and merge or close long-open dependency PRs (#11–#24).
  - Ship missing module protos modules already import (`circuitbreaker/v1`, `configwatcher/v1`) or document that those live outside core — current tree has neither under `proto/gen`.
  - Cut and publish real version tags/releases matching CHANGELOG (rc.1 / 0.4.0) so modules can drop `replace ../core`.
  - Clear remaining production-readiness gaps tracked in sibling notes (`module/mgr` coverage, `main()` decomposition) before calling anything production-ready.

### `muxcore-module-starter`
- **Status:** Early/scaffold — intentional cookiecutter module; README/ROADMAP still placeholders (`Your Module`, “Feature X/Y/Z”); “Core contract implementation” unchecked
- **Maturity:** Minimal scaffold (~163 Go LOC), unit + integration test stubs, CI/Dockerfile/docs template present; CI triggers only on `main`; same `../core` replace pattern as other modules without sibling checkout
- **Last activity:** 2026-07-22 — refresh starter docs/bootstrap; then Dependabot-only
- **Critical tasks:**
  - Make CI green for the template (checkout `core` or remove replaces) so copies inherit a working pipeline
  - Replace placeholder README badges/text (`yourorg/your-module`) and flesh out `ROADMAP.md` with real starter guidance instead of “Feature X”
  - Decide default branch (`main` vs org-wide `master`) so Dependabot/CI don’t diverge from sibling modules
  - Document the intended copy/rename flow (`muxcore.json`, binary name, capability IDs) in one short “create a module” section

### `muxcorectl`
- **Status:** Early/scaffold — MVP operator CLI exists (`modules list`, `cluster status`, `events tail`, `storage ls`, `audit query`), but lifecycle/spool admin is explicitly deferred; repo is also a broken multi-module workspace
- **Maturity:** ~505 LOC CLI (`cmd/` + `internal/cli`); one format unit test; README only; **no `.github` CI**; `go.mod` pins `core v0.1.0` with `replace ../core` while modules elsewhere use `v0.4.0`; **22 gitlinks and no `.gitmodules`**
- **Last activity:** 2026-07-22 — `feat: add muxcorectl CLI scaffold and module internals` (prior June work was media-ui/VPN/core submodule)
- **Critical tasks:**
  - Add `.gitmodules` (or stop tracking gitlinks) and document how nested module checkouts are meant to work — `git submodule status` already fails (`no submodule mapping for path 'admin-ui'`)
  - Add CI that builds/tests `./...` with a checked-out `core` sibling; today there is no workflow at all
  - Align `core` version (`v0.1.0` vs module ecosystem `v0.4.0`) and remove reliance on bare `../core` for consumers
  - Implement deferred RPCs called out in README (module stop/spawn, spool deploy) or demote the README so operators know admin control is unavailable

### `spool`
- **Status:** Active development — official catalog/tag pack (not a Go module); discovery surface for the org
- **Maturity:** README + SECURITY; `catalog.json` v2.1.0 with ~37 modules; tags `default`/`minimal`/`media`/`acquisition`; **no CI**, no Go code/tests
- **Last activity:** 2026-07-22 — jellyfin catalog bump to v0.2.0; acquisition tag pack + catalog docs refresh
- **Critical tasks:**
  - Close catalog gaps vs org repos still missing (e.g. `secrets-vault`, `media-ui`, `media-transcoder`, `media-subtitles`, `request-media`, `notification-apprise`, many contract/helper modules) so spool stays the source of truth
  - Add CI validating `catalog.json` / `tags/*.json` schema, unique names, and that referenced GitHub repos/tags exist
  - Audit pinned module versions in tags (many still `v0.1.0`) against real releases so `--tag default|media|…` installs coherent sets
  - Keep SECURITY third-party-spool warnings aligned as more community spools appear

### `spool-resolver-http`
- **Status:** Early/scaffold — thin HTTPS tag resolver implementing `SpoolResolver`; CI red on main
- **Maturity:** v0.1.0; single unit test file; Makefile/Docker/CI; no CHANGELOG/COMPAT/SECURITY; issues disabled
- **Last activity:** 2026-07-22 — CI/LICENSE bootstrap (after 2026-07-03 initial commit)
- **Critical tasks:**
  - Same CI blockers as `serialization-safe`: golangci Go 1.24 vs module Go 1.26.4, and `replace => ../core` without checking out `core`
  - Harden `ResolveTag`: builds `fmt.Sprintf("%s/%s", spoolUrl, tagName)` and fetches with no scheme/host allowlist — SSRF risk if mesh callers can supply arbitrary URLs; restrict to `https` + allowlisted hosts (e.g. GitHub raw/spool patterns)
  - Add CHANGELOG/COMPAT and a release workflow; enable issues or point to org support
  - Confirm tag URL shape matches official `spool` layout (`tags/<name>.json`) and document expected `spoolUrl` format in README

### `worker-pool-memory`
- **Status:** Active development — Phase 1 in-memory queue + HTTP API done; distributed dispatch/failover still open per ROADMAP; CI broken
- **Maturity:** v0.1.0; queue/server/integration tests; CI + release; ROADMAP marks Phase 2–3 unchecked; COMPATIBILITY incorrectly requires “MuxCore v1.0.0”; **~18MB binaries `module` and `worker-pool-memory` are committed to git**
- **Last activity:** 2026-06-12 — template alignment; earlier metrics/health/integration work; 7 open Dependabot PRs, no product commits since mid-June
- **Critical tasks:**
  - Remove committed binaries from git history/tree and extend `.gitignore` (only `/module` is ignored today; root `worker-pool-memory` binary is tracked) — bloated clone and supply-chain smell
  - Fix CI: last master failure used absolute `replace` to `/home/enderk/claude/core`; current `go.mod` uses `../core` but workflow still does **not** check out `core` (unlike `secrets-vault`/`tracing-otlp`)
  - Implement ROADMAP Phase 2 (executor discovery via registry + gRPC mesh dispatch + result collection) — without it the module is an HTTP queue, not the distributed worker pool README describes
  - Correct COMPATIBILITY (core ≥ 0.4.0, not v1.0.0) and finish Phase 3 reassignment/drain/metrics claims that README partially oversells

### `workflow-tapestry`
- **Status:** Active development — DAG engine with seeded media definitions and mesh dispatch; master CI does not build
- **Maturity:** v0.1.0; module + dispatch tests; seeded defs under `internal/definitions/`; CI with core checkout; CHANGELOG rich; no COMPATIBILITY; README says MIT while `LICENSE` is GPL-3.0; `go.mod` requires `core v0.1.0` while `muxcore.json` declares `minCoreVersion` 0.4.0
- **Last activity:** 2026-07-22 — workflow dispatch/seed definitions and module alignment
- **Critical tasks:**
  - Fix CI compile error: `no required module provides package github.com/Muxcore-Media/core/proto/gen/muxcore/workflow/v1` — align replace/checkout with a core revision that ships workflow protos
  - Align dependency metadata: bump `go.mod` core require to ≥0.4.0 to match `MinCoreVersion` / contract declaration
  - Resolve LICENSE mismatch (README “MIT” vs GPL-3.0 file) before release
  - Persistence: runs/definitions are in-memory only — document durability limits and plan a durable store if tapestry is required for production media pipelines

## Auth & Policy

### `auth-local`
- **Status:** Active development — full local AuthProvider/Authorizer/IdentityProvider with WebAuthn/TOTP/RBAC; not production-hardened while binaries ship in-repo and docs disagree on core version.
- **Maturity:** v0.1.0, CHANGELOG/COMPATIBILITY/SECURITY/ROADMAP, 7 test files, CI + release.yml, Dockerfile; `go.mod` uses `replace` → `../core`; COMPATIBILITY claims Core **v1.0.0+** while deps are **core v0.4.0**.
- **Last activity:** 2026-07-22/23 — Dependabot merges for checkout/setup-go/docker/golangci/gh-release; open PRs #10–#12 (actions + go-deps).
- **Critical tasks:**
  - Stop tracking / remove committed binaries `auth-local` and `authctl` (~27MB/~16MB blobs; listed in `.gitignore` but still in git) — history bloat and supply-chain risk.
  - Fix COMPATIBILITY vs `go.mod`/`replace` (document real core floor as v0.4.0+ or bump core; make CI build without sibling `../core` checkout).
  - Land or triage open Dependabot PRs (#10–#12).
  - Publish first tagged release (release.yml exists; no GH releases listed).

### `auth-oidc`
- **Status:** Early/scaffold — single initial commit (2026-07-23) with working OIDC login parity for admin-ui; docs/CI/metadata incomplete vs `auth-local`.
- **Maturity:** v0.1.0 `muxcore.json`, README, 5 test files, Makefile/Dockerfile/CI; no CHANGELOG, COMPATIBILITY, SECURITY, `.golangci.yml`, or release workflow; `replace` directives force CI to checkout `core` beside the module.
- **Last activity:** 2026-07-23 — “Initial commit: auth-oidc module”.
- **Critical tasks:**
  - Add CHANGELOG/COMPATIBILITY/SECURITY + golangci config and a release pipeline to match `auth-local`.
  - Replace `../core` local replaces with versioned module deps (or keep dual-checkout documented as temporary) so consumers can `go build` standalone.
  - Add IdP integration tests (Authentik/Keycloak) beyond unit maps/session — README documents Authentik but only local unit coverage exists.
  - Explicitly document intentional `Unimplemented` AuthService methods (user CRUD/TOTP/WebAuthn/API tokens) in COMPATIBILITY so callers do not assume auth-local parity.

### `call-policy-default`
- **Status:** Active development — call-policy enforcement works; publish-policy intentionally denies; ROADMAP advanced items open; committed binaries present.
- **Maturity:** v0.1.0 metadata, CI+release.yml, 4 test files, policies.yaml, SECURITY/COMPATIBILITY; CHANGELOG still “Initial scaffold” only; ROADMAP unchecked for dynamic/rate/time/group policies.
- **Last activity:** 2026-07-23 — Dependabot merges; open PRs #11–#13 (grpc + actions).
- **Critical tasks:**
  - Remove tracked binaries `call-policy-default` and `module` (~18MB/~16MB; gitignored yet committed) same as auth-local.
  - Refresh CHANGELOG to match implemented AllowCall path + metrics/health (scaffold-only notes are stale).
  - Keep publish denial explicit in docs/COMPATIBILITY and point operators to `publish-policy-default` (`AllowPublish` hard-denies with that reason today).
  - Prioritize ROADMAP “dynamic policy via event bus” only if mesh needs runtime grants; otherwise close or defer unchecked advanced items to avoid false readiness.

### `publish-policy-default`
- **Status:** Active development — YAML publish ACL is implemented with hot-reload, health, metrics, and solid tests; ROADMAP still lists payload checks, rate limits, dynamic capability matching
- **Maturity:** Policy + server + module tests + integration scaffold; docs/ROADMAP/SECURITY; CI correctly checks out sibling `core` (better than peers); **~35MB compiled binaries `module` and `publish-policy-default` are tracked in git** despite `.gitignore`
- **Last activity:** 2026-07-23 — Dependabot merges for Actions; no recent feature commits
- **Critical tasks:**
  - Remove committed binaries from git history/index and enforce ignore (they are already listed in `.gitignore` but still tracked)
  - Land ROADMAP P1 items that matter for production buses: payload-level `ResourcePublishPolicyProvider` checks and per-caller publish rate limiting
  - Keep Dependabot PRs unblocked — prior go-deps CI failures should be re-checked now that core checkout exists
  - Ship a tagged `v0.1.0` and verify `policies.yaml` defaults are safe for non-dev spools (avoid accidental `caller: "*"` / `"*"` rules)

## Contracts

### `contracts-downloader`
- **Status:** Active development — proto + generated Go stubs for `DownloaderService`; not a runnable module; CI red on `proto-check`.
- **Maturity:** Contract-only `0.1.0`; README lists RPCs and `downloader-native-torrent` implementer; Makefile + proto-check workflow; no unit tests (stubs only); issues disabled.
- **Last activity:** 2026-07-22 — docs/LICENSE refresh (initial 2026-07-03).
- **Critical tasks:**
  - Fix CI `proto-check`: `apt-get` runs without sudo → “Permission denied”, then `protoc: No such file or directory` (use `sudo apt-get` or a protoc action/image).
  - Add a smoke/`go test` that compiles the generated package (or buf lint) so “test” job is meaningful.
  - Tag a stable contract version and keep `contracts-reconciler` canonical entry (`Downloader` @ v1.0.0) aligned with published tags.
  - Confirm generated stubs stay in sync with implementers (`downloader-native-torrent`).

### `contracts-indexer`
- **Status:** Active development — proto contract with recent `absolute` episode field; CI failing same proto-check apt/protoc issue.
- **Maturity:** `0.1.0` stubs + solid README (season-pack/`absolute` semantics, single-site vs aggregator); Makefile/CI; no tests; notes planned `indexer-prowlarr` not published.
- **Last activity:** 2026-07-22 — docs/LICENSE; 2026-07-21 — `feat: add absolute episode number to indexer SearchRequest`.
- **Critical tasks:**
  - Same CI fix as other contracts repos (`sudo`/protoc installer) — latest CI **failure** on proto-check.
  - Publish or stub `indexer-prowlarr` so aggregator story in README is not aspirational-only.
  - Version/tag after `absolute` field and notify implementers (`indexer-piratebay`, media-automation fan-out).
  - Add buf/breaking-change check before more SearchRequest fields land.

### `contracts-media-admin`
- **Status:** Active development — richest media admin proto (history/missing/tags/collections/calendar); CI currently **green**.
- **Maturity:** Proto + `gen/` stubs, README documents TV/movies Unimplemented asymmetries; **no Makefile**, no `proto-check` job (unlike sibling contracts); `0.1.0`; no tests.
- **Last activity:** 2026-07-22 — CI/LICENSE/README; 2026-07-21 — history/missing/tags/collections/calendar RPCs + `delete_files` flag.
- **Critical tasks:**
  - Add Makefile + `proto-check` (parity with other contracts) so generated `gen/` cannot drift silently.
  - Drive implementers (`media-movies` / `media-tvshows`) to finish Unimplemented surfaces (TV collections, movie calendar) called out in README.
  - Tag contract versions when admin UI depends on new RPCs to avoid silent stub skew.
  - Consider a tiny compile/compat test importing `gen/muxcore/media/admin/v1`.

### `contracts-notification`
- **Status:** Early/scaffold — brand-new contract repo (single commit); CI already failing on proto-check.
- **Maturity:** Proto + generated stubs, Makefile, README with channel enum and known implementers; `0.1.0`; no tests; newest of the contracts set.
- **Last activity:** 2026-07-23 — `Initial commit` (CI failure same day).
- **Critical tasks:**
  - Fix proto-check CI (`sudo apt-get install protobuf-compiler` or non-apt protoc) — run failed 2026-07-23.
  - Verify implementers (`notification-default`, `notification-apprise`) import this module path and match `Channel` enum.
  - Register/update `NotificationProvider` in `contracts-reconciler` canonical map if third-party substitutes are expected.
  - Add proto-check/Makefile discipline and a first tagged release.

### `contracts-reconciler`
- **Status:** Active development — library used by `core` (`go.mod` require); CI **passing**; not a runnable module.
- **Maturity:** Substantial tests (many `Test*` in `reconciler_test.go`), README/SECURITY, muxcore.json `type: contracts`; **go 1.23** while org peers use **1.26.4**; canonical registry still references unpublished/legacy paths (`contracts-media`, `contracts-playback`, etc.).
- **Last activity:** 2026-07-23 — merge PR #1 workspace sync / CI + muxcore.json (engine initial 2026-05-26).
- **Critical tasks:**
  - Refresh `canonicalRegistry` to real Muxcore-Media contract repos (`contracts-downloader`, `contracts-indexer`, `contracts-media-admin`, `contracts-notification`, …) and drop stale names.
  - Bump `go` directive to 1.26.x for org consistency; re-run tests under that toolchain.
  - Add integration test that resolves against a checked-out real contracts-* repo (not only synthetic fixtures).
  - Publish tagged versions so `core` stops pinning a floating pseudo-version commit.

## Data & Secrets

### `data-redaction-pattern`
- **Status:** Active development — real `data.redaction` provider (field/path/regex rules + defaults) with substantial behavioral tests (~13 cases), but greenfield docs/release and broken CI
- **Maturity:** Strong unit coverage for redaction logic; CI present; **no** CHANGELOG/COMPATIBILITY/SECURITY/release workflow/tags; Issues disabled; only 2 commits
- **Last activity:** 2026-07-22 “add CI/LICENSE and align module bootstrap”; CI on that push **failed** (`../core` replace missing)
- **Critical tasks:**
  - Repair CI to checkout private `Muxcore-Media/core` alongside the module (same pattern as encryption’s multi-checkout intent)
  - Add CHANGELOG + COMPATIBILITY + first release tag `v0.1.0` once CI passes
  - Re-enable Issues (or track work elsewhere) for rule/API feedback
  - Confirm standalone `go build` without local monorepo replaces for consumers

### `database-postgres`
- **Status:** Empty — remote repo exists (`Muxcore-Media/database-postgres`) with description “MuxCore PostgreSQL database module” but zero commits / no default branch / working tree has no files
- **Maturity:** None (no code, docs, tests, CI, tags, or `muxcore.json`)
- **Last activity:** Created 2026-07-23; no commits since
- **Critical tasks:**
  - Bootstrap from `database-sqlite` / `muxcore-module-starter` (module layout, CI with private `core` checkout, Dockerfile, deploy)
  - Implement `database` / `database.postgres` capability (Exec/Query/Transaction/Migrate) against Postgres, mirroring the sqlite gRPC surface
  - Add unit + integration tests and a first `v0.1.0` tag once CI is green
  - Document env config (`DATABASE_URL` / pool settings) and `COMPATIBILITY.md` vs core `0.4.0+`

### `database-sqlite`
- **Status:** Active development — working SQLite sidecar (`modernc.org/sqlite`, WAL, gRPC DatabaseService) at `0.1.0`, tagged, but CI is red
- **Maturity:** Good local depth (db unit tests ~296 LOC, module lifecycle + integration scaffold, CI + release workflows, COMPATIBILITY/CHANGELOG/SECURITY); `muxcore.json` still has `"contracts": []`; Makefile ships `ghcr.io/yourorg/...`
- **Last activity:** 2026-07-22 module/CI alignment; 2026-07-23 Dependabot merges; open Dependabot PRs (#8–#10); last CI on those PRs **failed**
- **Critical tasks:**
  - Fix CI/`go.mod` `replace => ../core*` (private core): checkout sibling `core` in workflows or drop replaces for published module paths — current failure mode is `replacement directory ../core does not exist`
  - Populate `muxcore.json` contracts to match `DatabaseProvider` / gRPC API already documented in COMPATIBILITY
  - Replace `ghcr.io/yourorg` in Makefile with `ghcr.io/muxcore-media` and verify release workflow publishes
  - Merge/reconcile open Dependabot bumps once CI checkout is fixed

### `distributed-lock-sqlite`
- **Status:** Active development — TTL locks, sweeper, renew/unlock tokens, solid unit tests (~12), but CI broken and “distributed” is single-node SQLite unless storage is shared
- **Maturity:** Good functional tests; CI/LICENSE present; no CHANGELOG/COMPATIBILITY/release/tags; Issues disabled; 2 commits
- **Last activity:** 2026-07-22 bootstrap; CI **failed** on replace dirs
- **Critical tasks:**
  - Fix CI core sibling checkout / replace strategy (same failure as redaction: `../core does not exist`)
  - Clarify docs: this is process-local/shared-volume locking, not multi-host without networked SQLite — or plan a Redis/etcd backend
  - Add CHANGELOG + release tag after CI green
  - Add concurrency/stress tests for contending Acquire/Renew under sweeper load

### `encryption-aesgcm`
- **Status:** Active development — closest to production-ready in batch: AES-256-GCM, versioned keyring, RotateKey, SECURITY.md, strong encrypt/decrypt tests; `0.2.0` tagged
- **Maturity:** High for pre-1.0 (CHANGELOG, deploy assets, CI that checks out `core`); last CI push still **failed** (2026-07-22); Issues empty
- **Last activity:** 2026-07-22 docs/CI bootstrap refresh
- **Critical tasks:**
  - Diagnose/fix remaining CI failure on the core multi-checkout layout (last CI run failed despite core checkout steps)
  - Threat-model review: keyring file perms, master-key env handling, rotation/rollback runbooks (SECURITY.md is policy-only)
  - Add integration test covering legacy blob decrypt + multi-key ring after RotateKey across process restart
  - Align Dependabot/action versions with working checkout pattern used by sibling modules

### `secrets-file`
- **Status:** Broken/incomplete — feature-complete AES-GCM secrets sidecar, but default-branch CI is red
- **Maturity:** v0.1.0; unit + vault + integration tests; CI + release workflows; README/CHANGELOG/COMPAT/SECURITY present; Go 1.26.4
- **Last activity:** 2026-07-22/23 — Dependabot Action bumps merged (`setup-go`, `checkout`, `golangci-lint-action`, release action)
- **Critical tasks:**
  - Repair CI: `.golangci.yml` has no `version:` so `golangci-lint-action@v9` fails with `unsupported version of the configuration: ""`; also `go.mod` `replace => ../core` but workflow never checks out `Muxcore-Media/core`, so build/test fail with `replacement directory ../core does not exist`
  - Reconcile open Dependabot PRs (#7–#9) only after lint/config + core-checkout (or published core modules) are fixed — PR CI currently fails the same way
  - Drop or gate local `replace` directives for release builds so GHCR/release jobs can compile without a sibling checkout
  - Pre-1.0: COMPATIBILITY already warns interfaces may change — pin a core contract version and cut a green `v0.1.0` tag once CI passes

### `secrets-vault`
- **Status:** Active development — multi-provider SecretsService (Vault/OpenBao, Infisical, AWS, GCP, Azure) landed; master CI still failing
- **Maturity:** v0.1.0; strong test surface (9 `*_test.go` incl. per-backend + integration); CI checks out `core`; solid README; thin CHANGELOG/ROADMAP; **not** listed in official `spool` catalog/tags
- **Last activity:** 2026-07-23 — single commit: “Initial secrets-vault module with multi-provider backends”; open Dependabot PRs #1–#6
- **Critical tasks:**
  - Fix CI checkout of `Muxcore-Media/core` — initial master run fails with `Not Found` on the core repo checkout (token/visibility/permissions); without that, `replace => ../core` cannot resolve
  - Add `secrets-vault` to `spool` `catalog.json` (and optional tag entries) so operators can discover it alongside `secrets-file` (README already documents mutual exclusivity of the `secrets` capability)
  - Migrate `.golangci.yml` to a versioned config before merging Action major bumps (same empty-version class of failures seen on sibling modules)
  - Expand backend tests beyond mocks where feasible (live Infisical/Vault smoke in CI with secrets) — current coverage is mostly unit-level

### `serialization-safe`
- **Status:** Early/scaffold — working JSON↔msgpack `Convert`/`SupportedTypes` gRPC module, but CI never green and packaging docs incomplete
- **Maturity:** v0.1.0; one unit test file; CI present; no CHANGELOG/COMPATIBILITY/SECURITY/release workflow; issues disabled
- **Last activity:** 2026-07-22 — “chore: add CI/LICENSE and align module bootstrap” (after 2026-07-03 initial commit)
- **Critical tasks:**
  - Unblock CI: `golangci-lint-action@v6` built with Go 1.24 cannot lint Go 1.26.4 (`can't load config: ... lower than the targeted Go version`); upgrade lint action / golangci binary
  - Add core checkout (or remove `replace => ../core`) — workflow only checks out this repo, so CI cannot resolve MuxCore deps the same way as today
  - Add CHANGELOG + COMPATIBILITY + release workflow to match mature modules (`secrets-file` pattern)
  - Issues are disabled — enable tracker or document support path for a public capability module

## Cache & Infra

### `backup-local`
- **Status:** Active development — real `.tar.gz` create/restore with checksums and path-traversal rejection; still Unreleased/v0.1.0.
- **Maturity:** CHANGELOG/COMPATIBILITY (core v0.4.0+), CI+release.yml, Dockerfile, module + integration tests (~310 test LOC); open Dependabot PRs #7–#9.
- **Last activity:** Recent merges via Dependabot (through ~2026-07-23); open PR #9 bumps grpc (2026-08-03).
- **Critical tasks:**
  - Add dedicated archive round-trip / traversal / empty-archive tests at `internal/` (CHANGELOG claims these fixes; coverage is mostly module wiring + thin integration).
  - Exercise Backupable `ExportState`/`ImportState` peer path in CI (README marks it as first-class create/restore content).
  - Merge Dependabot PRs (#7–#9) and cut first `v0.1.0` tag (release.yml present, no releases).
  - Confirm proto packaging (`proto/muxcore/backup/v1`) is the canonical shared contract vs vendored generated code drift.

### `cache-local`
- **Status:** Early/scaffold — one commit (2026-07-23) delivering in-memory `CacheLayerService` for `cache.local` (+ advertises `cache.memory`).
- **Maturity:** v0.1.0 docs (README/CHANGELOG/COMPATIBILITY/SECURITY), 4 test files, CI+release.yml, Makefile/Dockerfile; 6 open Dependabot PRs (#1–#6) including grpc and actions.
- **Last activity:** 2026-07-23 — “Initial cache-local module.”
- **Critical tasks:**
  - Clarify capability split: `muxcore.json` lists both `cache.local` and `cache.memory` while README/COMPATIBILITY emphasize `cache.local` only — align naming with the misnamed `cache-memory` workspace repo.
  - Merge Dependabot backlog (#1–#6); CI still on older action majors in open PRs.
  - Add persistence/eviction/concurrency stress tests for the sweeper + TTL map (process-local loss-on-restart is documented; race coverage matters).
  - Tag `v0.1.0` once CI is green on current actions.

### `cache-memory`
- **Status:** Broken/incomplete — not an in-memory cache module; private multi-module agent/dev workspace (`AGENTS.md`, `docker-compose.yml`, `notes/`, many module dirs) with broken gitlinks (`no submodule mapping found in .gitmodules for path 'admin-ui'`).
- **Maturity:** No README/go.mod/muxcore.json/Makefile/CI for a cache product; ~225 tracked paths spanning core + many modules; issues disabled; empty GH description; ~105MB disk.
- **Last activity:** 2026-07-22 — “docs: update AGENTS guidance”; earlier June commits touch VPN/kill-switch and media-ui design-sync inside this workspace.
- **Critical tasks:**
  - Rename/repurpose or archive: either become the real `cache.memory` module or stop colliding with `cache-local`’s `cache.memory` capability.
  - Fix or remove broken submodule/gitlink entries (`.gitmodules` missing while tree has `admin-ui`, `core`, etc.).
  - Extract durable planning docs (`notes/*`) to a proper docs/ops repo; strip secrets/hooks if this was a personal Claude working dir.
  - Do not treat as a deployable MuxCore module until a single `go.mod` + `muxcore.json` + CI exist.

### `cache-redis`
- **Status:** Active development — Redis-backed cache sidecar with broad gRPC surface; CHANGELOG still “initial scaffold” despite implementation.
- **Maturity:** v0.1.0, README/CHANGELOG/COMPATIBILITY/SECURITY, 3 test files, CI+release.yml, Dockerfile; open Dependabot PRs #8–#10 (actions + grpc).
- **Last activity:** 2026-07-22/23 — Dependabot merges; open PR #10 (grpc 1.83.0, 2026-08-03).
- **Critical tasks:**
  - Update CHANGELOG to describe real RPCs (Get/Set/Delete/Exists/Incr/CAS/Lock/PubSub) rather than starter scaffold only.
  - Add Redis integration tests in CI (testcontainers or service container) — current tests likely miss live Redis Lock/PubSub behavior.
  - Merge Dependabot PRs and publish first release tag.
  - Document HA/key-prefix/auth TLS expectations for production Redis (SECURITY.md exists but ops knobs are minimal in README).

### `circuitbreaker-simple`
- **Status:** Broken/incomplete — module code and tests exist (v0.1.0), but CI cannot build: `go.mod` `replace` points at `../core` without checking out core, and it imports `core/proto/gen/muxcore/circuitbreaker/v1` which is absent from current `core` (only `pkg/contracts/circuitbreaker.go` exists).
- **Maturity:** Early module: README + Makefile/Dockerfile + golangci + unit tests (`module_test.go` covers open/half-open paths); no CHANGELOG/COMPATIBILITY; muxcore.json `0.1.0`; latest CI push **failed** (2026-07-23).
- **Last activity:** 2026-07-22 — `chore: add CI/LICENSE and align module bootstrap` (prior: 2026-07-03 initial commit).
- **Critical tasks:**
  - Fix CI to sibling-checkout `Muxcore-Media/core` (same pattern as `config-watcher`) or drop local `replace` and consume a published core module/proto path.
  - Add missing `circuitbreaker/v1` proto to `core` (or move the service proto into this module) so the import resolves.
  - Publish a tagged core dependency instead of workspace-only `replace` so standalone clones work.
  - Add CHANGELOG and document TLS/prod config beyond `MUXCORE_INSECURE_DISABLE_TLS`.

### `config-watcher`
- **Status:** Broken/incomplete — richest module packaging in this batch (deploy compose/systemd, CHANGELOG, SECURITY/CONTRIBUTING), but builds fail against current core: imports `core/proto/gen/muxcore/configwatcher/v1` which is not in `core/proto/gen`.
- **Maturity:** Active feature work with tests (info, watch/report, multi-watcher, lifecycle); CI checkouts core as sibling but still fails; version `0.1.0`; CHANGELOG still entirely under `[Unreleased]`.
- **Last activity:** 2026-07-22 — `chore: add LICENSE and refresh docs/CI` (feat initial 2026-06-13).
- **Critical tasks:**
  - Land `configwatcher/v1` generated protos in `core` (or stop importing from core and use only the local `proto/.../admin.proto` + generated stubs).
  - Repair CI: latest run failed with “no required module provides package …/configwatcher/v1”.
  - Align admin vs watcher proto split (local `adminv1` vs core package) and document the contract ownership.
  - Cut a first CHANGELOG release once CI is green; keep deploy docs in sync with working builds.

### `executor-shell`
- **Status:** Active development — shell executor with fail-closed allowlist (`EXECUTOR_ALLOWED_COMMANDS`), local proto, meaningful CanHandle/Execute tests; no release yet
- **Maturity:** Moderate (CHANGELOG, Dockerfile/deploy, CI); **no** SECURITY.md despite RCE-adjacent surface; no tags; Issues disabled; CI **failed** on 2026-07-22
- **Last activity:** 2026-07-22 proto contracts + bootstrap
- **Critical tasks:**
  - Fix CI core replace/checkout; get `go test`/`go build` green on push
  - Add SECURITY.md: allowlist guidance, forbid `*` in prod, logging of rejected payloads, sandboxing notes
  - Expand tests: prefix matching edge cases, timeout/cancel, large stdout/stderr handling
  - Cut `v0.1.0` release once CI passes; enable Issues for security reports or use advisories

### `feature-flags-file`
- **Status:** Active development — YAML flags + SIGHUP reload + gRPC `IsEnabled`/`GetVariant` implemented, but tests are scaffold-only and CI failing on Dependabot PRs
- **Maturity:** Docs/COMPATIBILITY/release workflow present; unit tests only Info/Lifecycle (~32 LOC); `muxcore.json` `"contracts": []`; Makefile still `ghcr.io/yourorg`; **no git tags** despite release.yml
- **Last activity:** 2026-07-22 starter alignment; Dependabot Jul 23 / Aug 3 graph updates; CI on Dependabot PRs **failed**
- **Critical tasks:**
  - Fix CI private-core checkout vs `replace => ../core*`
  - Add behavioral tests: load YAML, IsEnabled/GetVariant defaults, SIGHUP reload, missing file behavior
  - Declare `FeatureFlagProvider` in `muxcore.json` contracts (COMPATIBILITY already lists it)
  - Fix GHCR org in Makefile and publish first `v0.1.0` via release workflow

### `health-monitor`
- **Status:** Active development — Wave 10 status/stale + Wave 19 mesh fan-out of `module.*` / `health.*` (v0.1.2)
- **Maturity:** Unit tests for degrade/stale/status; tagged lineage `v0.1.0` locally at `0.1.1`; MVP smoke via `_mvp/cmd/healthreport`
- **Last activity:** 2026-08-07 — Wave 10 aggregation/`/status`
- **Critical tasks:**
  - Wire `PublishEvent` / degrade transitions onto core event bus (or document local-only ring)
  - Optionally add GetSummary gRPC to `healthmonitor` proto (today summary is HTTP-only)
  - Tag/publish `v0.1.1` once CI billing restored
  - Have other MVP modules optionally `ReportHealth` on a timer (today smoke injects reports)

### `input-validate-jsonschema`
- **Status:** Active development — real JSON Schema/regex `InputValidator` with path-traversal guards; CI red; docs/release thin
- **Maturity:** `muxcore.json` **v0.1.0** / `minCoreVersion` 0.4.0; solid unit suite (~13 tests incl. symlink/traversal); Makefile/Docker/CI; **no** CHANGELOG/COMPATIBILITY/tag/Release; issues disabled
- **Last activity:** 2026-07-22 — proto contracts, CI/LICENSE, bootstrap alignment (initial 2026-07-03)
- **Critical tasks:**
  - Fix CI `replace => ../core` checkout (and golangci go1.26 mismatch) — last push CI failed build/test/lint
  - Add CHANGELOG + COMPATIBILITY and tag/publish `v0.1.0` (catalog already lists the repo)
  - Align proto layout (`muxcore/inputvalidate` vs `proto/`) with other modules to avoid consumer confusion
  - Enable issues or document support channel (GitHub issues disabled)

### `logging-file`
- **Status:** Active development — usable JSONL logger with rotation/redaction/`SetLevel`; CI red; release hygiene missing
- **Maturity:** `muxcore.json` **v0.1.0**; strong unit coverage (~398 test LOC: levels, rotation, redaction, stdout); Makefile/Docker/CI; **no** CHANGELOG/COMPATIBILITY/tag; issues disabled
- **Last activity:** 2026-07-22 — CI/LICENSE + bootstrap alignment (initial 2026-07-03)
- **Critical tasks:**
  - Fix CI sibling `core` checkout + golangci go1.26 — last CI failed on missing `../core` and linter version
  - Expose rotation knobs via env/settings — README admits `MaxSizeMB`/`MaxBackups` are code-only defaults
  - Add CHANGELOG/COMPATIBILITY and tag `v0.1.0` (already in spool catalog)
  - Re-enable issues or point operators to org support

### `metrics-prometheus`
- **Status:** Broken/incomplete — module code looks usable (v0.1.0 Prometheus gRPC + `/metrics`), but every recent `CI` run on `master` fails because `go.mod` `replace`s point at `../core` and the workflow never checks out `core`
- **Maturity:** Unit + integration tests; full docs (`README`, `CHANGELOG`, `COMPATIBILITY`, `SECURITY`); Dockerfile/Makefile; no releases/tags; Dependabot PRs open
- **Last activity:** 2026-07-22–27 — template/CI alignment, then Dependabot action/Go bumps (no feature commits since)
- **Critical tasks:**
  - Fix CI like `publish-policy-default`: checkout `Muxcore-Media/core` beside the module (or drop `replace` and consume published modules) so `go test`/`go build` stop failing on missing `../core`
  - Merge or close stale Dependabot PRs (`actions/checkout`/`setup-go`, go-deps) after CI is green
  - Add a tagged `v0.1.0` release once CI passes (changelog already claims 2026-07-21)
  - Expand beyond smoke tests: exercise register/increment/histogram paths and scrape output under race (current `module_test.go` is only ~32 LOC)

### `ratelimit-tokenbucket`
- **Status:** Broken/incomplete — token-bucket gRPC module present (v0.1.0 docs/tests), but `master` CI consistently fails on `replace ../core` without checking out `core`
- **Maturity:** Unit + integration tests; CHANGELOG/COMPATIBILITY/SECURITY; limiting **off until `RATELIMIT_ENABLED`**; no releases; open Dependabot PRs
- **Last activity:** 2026-07-22–27 — deps/CI bumps only after template alignment
- **Critical tasks:**
  - Fix CI core checkout (mirror `publish-policy-default`) so lint/test/build pass
  - Add tests for enabled/disabled paths, burst exhaustion, and multi-key isolation (current coverage is thin relative to the capability’s blast radius)
  - Document/default-safe behavior for production: empty/missing `RATELIMIT_ENABLED` means unrestricted traffic
  - Publish `v0.1.0` after CI is green; clear Dependabot queue

### `scheduler-cron`
- **Status:** Broken/incomplete — HTTP scheduler + cronstore are implemented with good unit tests, but CI still fails (`replace ../core/...` missing), **binaries `module`/`scheduler-cron` are committed**, and fired tasks only log (ROADMAP: no event publish, no persistence, UTC-only)
- **Maturity:** cronstore/server tests + integration scaffold; README/ROADMAP/CHANGELOG; `COMPATIBILITY.md` claims Core **v1.0.0+** while `go.mod` uses `core v0.4.0` — docs drift
- **Last activity:** 2026-07-22–08-03 — Dependabot only; open PR CI still failing on replace dirs
- **Critical tasks:**
  - Fix CI to checkout `core` (or drop replaces); unblock Dependabot grpc bump
  - Untrack committed binaries (`module`, `scheduler-cron`) — same ignore/track bug as publish-policy
  - Implement ROADMAP P0: publish `scheduler.task.*` events and enforce task timeouts (handler today is log-only)
  - Add persistence / missed-run catch-up (in-memory store loses schedules on restart) and correct COMPATIBILITY to match actual core `v0.4.0`

### `tracing-otlp`
- **Status:** Broken/incomplete — OTLP export + slog fallback implemented, but master CI fails to compile
- **Maturity:** v0.1.0; unit tests; CI with intended `core` sibling checkout; CHANGELOG/SECURITY; no `.golangci.yml`/COMPATIBILITY; `muxcore.json` `roles` empty
- **Last activity:** 2026-07-22 — LICENSE + docs/CI/deps refresh
- **Critical tasks:**
  - Fix build: CI error `no required module provides package github.com/Muxcore-Media/core/proto/gen/muxcore/tracing/v1` despite `replace => ../core` — ensure published/checked-out `core` actually exposes generated protos (or vendor/require the proto path correctly)
  - Add COMPATIBILITY + fill `roles` in `muxcore.json` (other infra modules use `infrastructure`)
  - Add `.golangci.yml` (versioned) or stop implying lint parity with starter template
  - Verify OTLP path vs slog fallback with an integration test against a local collector when `OTEL_EXPORTER_OTLP_ENDPOINT` is set

## Media Pipeline

### `downloader-native-torrent`
- **Status:** Active development — richest module in this batch (`v0.2.0`): anacrolix engine, VPN/NAT-PMP, contracts-downloader adapter, settings, events; ROADMAP still open
- **Maturity:** Broad surface + injectable `fakeEngine` tests (~27 cases); CHANGELOG/ROADMAP/CI; tagged `v0.2.0`; CI last push **failed**; default gRPC `:9460` collides with `media-automation` (documented)
- **Last activity:** 2026-07-22 engine + contracts/settings/CI commits
- **Critical tasks:**
  - Unblock CI (`replace` for `../core` and `../contracts-downloader` — checkout both in workflow)
  - Finish ROADMAP: file selection priorities; DHT/PEX tuning via settings
  - Default or document non-colliding `DOWNLOADER_GRPC_ADDR` for multi-module hosts
  - Add release workflow + smoke test against real magnet in a controlled env (unit tests intentionally avoid public trackers)

### `indexer-piratebay`
- **Status:** Active development — working Apibay `IndexerService` with settings/rate-limit, but CI red on `main`
- **Maturity:** `muxcore.json`/`CHANGELOG`/`COMPATIBILITY`/`ROADMAP` at **v0.1.0** (tagged); ~940 Go LOC + 248-LOC fixture/`httptest` tests; CI/Docker/Makefile; no GitHub Release; no open issues/PRs
- **Last activity:** 2026-07-22 — settings support + module/CI hardening (after initial commit same day)
- **Critical tasks:**
  - Fix CI: checkout sibling `core` + `contracts-indexer` (or drop `replace`s) — last CI failed with `../core` / `../contracts-indexer` missing
  - Upgrade `golangci-lint-action` past v6 — lint fails: linter built with go1.24 vs module go1.26.4
  - Change default gRPC `:9480` — same default as `media-ffprobe` (`internal/module.go`), will collide on multi-module hosts
  - Finish ROADMAP optional torrent-detail enrichment; publish a GitHub Release for tagged `v0.1.0`

### `jellyfin`
- **Status:** Active development — richest playback bridge (**v0.2.0**): webhooks, item-link sync, sessions poll; Wave 11 soft SyncLibrary + MVP link/webhook smoke
- **Maturity:** ~3.3k Go LOC; webhook/sync/health tests; full docs; deploy compose/systemd; release workflow; tagged `v0.2.0` but no GitHub Release; Dependabot CI still blocked by org billing/toolchain
- **Last activity:** 2026-08-07 — Wave 11 soft SyncLibrary + `_mvp/cmd/jellyfinlink` smoke
- **Critical tasks:**
  - Unblock CI / Dependabot when Actions billing restored
  - Publish GitHub Release for `v0.2.0`; keep spool catalog pin in sync
  - ROADMAP: admin-ui/media-ui surfaces for status/refresh/play links
  - Optional live Jellyfin integration smoke (when a real server is available)

### `media-automation`
- **Status:** Active development — main acquisition engine (fan-out, scoring, upgrades, import-on-complete); featureful but unreleased and CI red
- **Maturity:** ~5.1k Go LOC + ~1k test LOC + integration scaffold; README/CHANGELOG/COMPATIBILITY/ROADMAP/SECURITY; CI + release workflows; **no tags/releases**; CHANGELOG still entirely under **Unreleased**; 2 open Dependabot PRs (CI failing)
- **Last activity:** 2026-07-23 — Dependabot action/Go bumps; last product work 2026-07-21–22 (download-event delay/settings, indexer fanout tests, library event sync)
- **Critical tasks:**
  - Fix CI checkouts for `../core`, `../contracts-indexer`, `../contracts-downloader`, `../media-custom-formats`, etc. — Dependabot CI still fails Aug 4 on missing replaces
  - Cut `v0.1.0` (or later): move CHANGELOG off Unreleased and run `release.yml`
  - Resolve default port clash with `downloader-native-torrent` both on `:9460` (documented elsewhere; still operational landmine)
  - ROADMAP remaining: per-series override of delay/release groups

### `media-custom-formats`
- **Status:** Active development — FormatService scoring/profiles/seeds used by automation; CI red; docs incomplete vs tag
- **Maturity:** tagged **v0.1.0**; ~1.2k internal LOC + tests; README/COMPATIBILITY/SECURITY/CONTRIBUTING; Makefile/Docker/CI; **no CHANGELOG**; COMPATIBILITY contracts table still “Planned”
- **Last activity:** 2026-07-22 — release-group seed data + module bootstrap/CI (initial 2026-06-15)
- **Critical tasks:**
  - Fix CI (`../core` replace + golangci go1.26) — last push CI failed
  - Write CHANGELOG matching the `v0.1.0` tag and publish a GitHub Release
  - Update COMPATIBILITY: document local `formatsv1` proto (and soft dep from `media-automation` `ScoreRelease`) instead of “Planned”
  - Add scoring golden tests for seeded Remux/HDR/x265/Proper/CAM + release-group prefer/must-not paths

### `media-ffprobe`
- **Status:** Active development — ffprobe analyze/cache/quality classify at v0.1.0; CI red; live analyze lightly tested
- **Maturity:** tagged **v0.1.0**; README/CHANGELOG/COMPATIBILITY/SECURITY; Docker installs `ffmpeg`; unit tests for parsers/HDR/cache; Analyze path logs “expected without ffprobe”; CI present
- **Last activity:** 2026-07-22 — add CI + refresh docs/deploy (initial 2026-06-15)
- **Critical tasks:**
  - Fix CI `../core` checkout + golangci go1.26 mismatch — last push failed
  - Change default gRPC `:9480` — collides with `indexer-piratebay`
  - Add CI/fixture test that runs real `ffprobe` (image already has ffmpeg) so Analyze/cache aren’t best-effort only
  - COMPATIBILITY still says contracts “Planned” while module-local `ffprobev1` is shipping — document the real surface

### `media-jellyfin`
- **Status:** Broken/incomplete — **polluted workspace**, not a Jellyfin module: no root `go.mod`/`muxcore.json`/`README`/CI; real bridge lives in sibling repo `jellyfin`
- **Maturity:** Monorepo dump: `docker-compose.yml`, `AGENTS.md`, nested module trees, **22 gitlinks** without `.gitmodules` (`git submodule status` fails), many empty submodule dirs locally; only Dependabot graph updates; issues disabled
- **Last activity:** Local HEAD 2026-06-19 media-ui design-sync; remote last push 2026-07-03; graph updates 2026-07-23
- **Critical tasks:**
  - Decide fate: archive/rename vs convert to a real app repo — name collides with functional `Muxcore-Media/jellyfin`
  - Add `.gitmodules` or stop tracking gitlinks (`admin-ui`, `core`, …) so clones aren’t empty stubs
  - Either extract a real `media-jellyfin` module (`cmd`/`internal`/`muxcore.json`) or delete the misleading repo and keep `jellyfin` as canonical
  - Strip nested copies of unrelated modules (`logging-file`, `media-movies`, VPN notes, etc.) that duplicate org repos

### `media-list-sync`
- **Status:** Active development — Trakt/IMDb/Plex/Jellyfin/*arr fetch + library sync implemented; packaging/CI lag feature work
- **Maturity:** tagged **v0.1.0**; ~4.1k Go LOC + fetch/rootpath/module tests; thin README; CI only; **no** Makefile/Dockerfile/CHANGELOG/COMPATIBILITY; many `go.mod` replaces to sibling media modules
- **Last activity:** 2026-07-22 — README/LICENSE/CI + proto refresh; 2026-07-21 root-path resolve + multi-source fetch/sync
- **Critical tasks:**
  - Fix CI: checkout `core` plus `media-automation`/`media-movies`/`media-tvshows`/`metadata-tmdb`/`media-root-folders` (or publish and drop replaces) — last CI failed on all of those missing
  - Add Makefile/Dockerfile to match sibling modules; flesh README (auth fields for Trakt OAuth, IMDb export URL, root-folder rules)
  - Add httptest coverage for `fetchTrakt`/`fetchIMDb` HTTP paths (tests today mostly parse/CRUD, not live client errors)
  - Write CHANGELOG/COMPATIBILITY for post-tag Jul 21–22 features and publish a Release

### `media-movies`
- **Status:** Active development — feature-rich movie library (`MediaAdminService`, tags/collections/history) at v0.1.0, but CI is red on master
- **Maturity:** ~7.3k LOC non-test Go; 5 test files (incl. large `module_test.go`); README + Makefile + Dockerfile + `muxcore.json`; no CHANGELOG/COMPATIBILITY; no git tags; issues disabled
- **Last activity:** 2026-07-22 — README/LICENSE/CI bootstrap; prior week added admin adapter, history, titles, root paths, tags/collections/artwork
- **Critical tasks:**
  - Fix CI: `go.mod` `replace` paths expect sibling checkouts (`../core`, `../contracts-media-admin`, `../metadata-tmdb`, `../media-root-folders`) that Actions does not fetch — build/test fail immediately
  - Upgrade golangci-lint action (v6 binary built with go1.24 cannot lint go1.26.4)
  - Publish tagged SDK/module versions (or CI multi-checkout) so modules build outside a monorepo workspace
  - Add CHANGELOG/COMPATIBILITY and a v0.1.0 tag to match `muxcore.json`

### `media-rename`
- **Status:** Active development — multi-template renamer with hardlink/copy/move import modes at tagged v0.2.0; CI broken
- **Maturity:** ~3.9k LOC; single large `module_test.go` (~543 lines) + hardlink fixtures; README/LICENSE/CI/`muxcore.json`; no Makefile/Dockerfile; tag `v0.2.0`
- **Last activity:** 2026-07-22 — README/CI/muxcore.json + hardlink fixture; 2026-07-21 — named templates, episode tokens, multi-episode import modes
- **Critical tasks:**
  - Unblock CI: same sibling `replace ../core` failure plus golangci-lint go1.24 vs go1.26 mismatch
  - Add Dockerfile/Makefile for deploy parity with other media modules
  - Expand tests beyond module-level happy paths (edge cases for tokens, multi-episode, import modes)
  - Document contract/capability consumers (`media-scanner` depends on this via replace)

### `media-root-folders`
- **Status:** Active development — focused `media.roots` catalog with free-space probes; roadmap items marked done; CI red
- **Maturity:** ~2.3k LOC; 1 test file; README + COMPATIBILITY + ROADMAP + Makefile; tag `v0.1.0`; default branch `main`
- **Last activity:** 2026-07-22 — initial module + README/CI/roadmap refresh (2 commits total)
- **Critical tasks:**
  - Fix CI sibling-replace + golangci-lint Go version (identical failure pattern)
  - Add Dockerfile for compose/deploy stacks that expect container images
  - Harden free-space/accessibility probe edge cases (permissions, missing mounts) with dedicated tests
  - Align indirect core pin (`v0.1.0` in go.mod require) with stated minCore `0.4.0`

### `media-scanner`
- **Status:** Active development — Wave 16 skips incomplete downloads (`*.part` etc.); ImportPath/watch/import mature; CI still blocked by Actions billing
- **Maturity:** ~4.2k LOC; unit + integration tests; full docs; tag `v0.1.0` (code at **0.1.1**); Dependabot active
- **Last activity:** 2026-08-08 — Wave 16 incomplete-download skip
- **Critical tasks:**
  - Make CI workspace-aware (sibling checkouts) when Actions billing restored
  - Tag `v0.1.1`; migrate golangci config
  - Keep CHANGELOG/ROADMAP aligned

### `media-subtitles`
- **Status:** Active development — OpenSubtitles search/download + sidecar registration at tagged v0.4.0; CI broken
- **Maturity:** ~2.8k LOC; 1 test file; README/`muxcore.json`/CI; no Makefile/Dockerfile/CHANGELOG; tag `v0.4.0`
- **Last activity:** 2026-07-22 — README/LICENSE/CI/proto refresh; 2026-07-21 — `RegisterSidecar`
- **Critical tasks:**
  - Fix CI (`replace ../core` + golangci-lint go1.26 incompatibility)
  - Add Dockerfile and config docs for `OPENSUBTITLES_API_KEY` (search/download hard-fail without it)
  - Add HTTP-client tests/mocks for OpenSubtitles paths (auth errors, rate limits, download URL flow)
  - Version alignment: muxcore.json says `0.3.0` while git tag is `v0.4.0`

### `media-transcoder`
- **Status:** Early/scaffold toward usable — FFmpeg queue/profiles/GPU detect exist, but thin docs surface and CI red; little post-June feature velocity
- **Maturity:** ~2.7k LOC; 1 test file (~258 lines); solid README (grpcurl examples); CI/`muxcore.json`; tag `v0.1.0`; no Makefile/Dockerfile
- **Last activity:** 2026-07-22 — README/LICENSE/CI/muxcore.json only; feature code dated 2026-06-15
- **Critical tasks:**
  - Fix CI replace/lint toolchain blockers
  - Add Dockerfile with FFmpeg (+ optional NVENC/VAAPI notes); document host GPU device requirements
  - Expand job lifecycle tests (cancel, progress parse, failure paths) beyond profile CRUD/NVENC preset checks
  - Ship CHANGELOG and confirm GPU profile seeding works when hardware is absent

### `media-tvshows`
- **Status:** Active development — largest library module (series/season/episode, MediaAdmin + TvManagement); docs strong; CI red on master and Dependabot PRs
- **Maturity:** ~8.8k LOC; 6 test files incl. integration; README/CHANGELOG/COMPATIBILITY/SECURITY/CONTRIBUTING; Dockerfile/Makefile/release.yml; no git tags despite v0.1.0 docs; open Dependabot PRs (#11 grpc, #12 sqlite)
- **Last activity:** 2026-07-23 — Dependabot action bumps; feature baseline from June; PRs still open as of 2026-08-04
- **Critical tasks:**
  - Fix CI multi-repo checkout for replaces (`core`, `contracts-media-admin`, `media-root-folders`, `metadata-tmdb`) — same root cause as media-movies
  - Merge/fix Dependabot PRs after CI is workspace-capable; bump golangci config for v9 if needed
  - Tag `v0.1.0` and keep ROADMAP item (indexer/download ownership) clearly out-of-scope vs automation
  - Mirror media-movies feature parity gaps (collections/quality profiles) only if admin UI contract requires them

### `media-ui` / `media-ui-app`
- **Status:** Active development — Wave 22 clean extract in sibling [`media-ui-app/`](media-ui-app/); parent dump quarantined
- **Maturity:** Vite SPA + auth/logout/stream on MVP BFF; CI stub in extract; no GitHub remote yet; limited frontend unit tests
- **Last activity:** 2026-08-08 — Wave 22 `media-ui-app` packaging
- **Critical tasks:**
  - Push `media-ui-app/` as a clean GitHub repository; archive parent dump
  - Add frontend unit tests
  - Keep `_mvp` `MEDIA_UI_DIST` pointed at extract `dist-app`

### `metadata-tmdb`
- **Status:** Active development — TMDB provider + Wave 15 `TMDB_FIXTURE` offline mode (v0.1.1); CI still red on Actions billing
- **Maturity:** ~6.1k LOC; large `module_test.go`; fixture Fight Club / Breaking Bad for keyless smoke; no CHANGELOG/COMPATIBILITY; no tags; default branch `main`
- **Last activity:** 2026-08-08 — Wave 15 fixture mode + MVP smoke search
- **Critical tasks:**
  - Fix CI: checkout `../core` (and any contract packages) or publish replace-free modules
  - Resolve `contracts-metadata` reference in `muxcore.json` — publish contract or retarget to `core/pkg/contracts`
  - Tag `v0.1.1`; document `TMDB_API_KEY` vs `TMDB_FIXTURE=1`

### `request-media`
- **Status:** Active development — Wave 13 on MVP stack (HTTP/gRPC intake + AddMovie fallback); host dial fix for discovery
- **Maturity:** Module + `reqstore` tests; README + CHANGELOG; local `proto/`; no COMPATIBILITY; search needs `TMDB_API_KEY` via metadata-tmdb
- **Last activity:** 2026-08-08 — Wave 13 MVP wiring + `MUXCORE_MESH_DIAL_LOCAL`
- **Critical tasks:**
  - Repair CI sibling checkouts when Actions billing restored
  - Add COMPATIBILITY; harden TV path tests
  - Document `TMDB_API_KEY` for live search in MVP `.env`
  - Decide whether `proto/requestmedia` stays local or moves under contracts

## Notifications & Misc

### `custom-scripts`
- **Status:** Active development — private/org workspace for local stack, agent rules, notes, and nested module checkouts — not a product module.
- **Maturity:** No root README/CI; `AGENTS.md` + large `docker-compose.yml`; many `notes/` audits/plans; mixed nested copies (some populated module trees, many **empty** dirs like `core/`, `admin-ui/`); `git submodule status` errors (“no submodule mapping … admin-ui”); last touch media-ui design-sync notes.
- **Last activity:** 2026-07-22 — `chore: refresh media-ui design-sync notes` (VPN/docker fixes mid-June).
- **Critical tasks:**
  - Repair gitlinks/submodules: add `.gitmodules` or remove empty nested dirs so the workspace clones cleanly.
  - Add a root README describing purpose (dev/orchestration workspace vs publishable module) and which nested trees are source-of-truth vs mirrors.
  - Reconcile nested module copies with standalone repos to avoid dual-maintenance drift (e.g. embedded `circuitbreaker-simple` vs org repo).
  - Execute or close dated `notes/` items (`wiki-changes-pending.md` mTLS docs, `v1-production-readiness-plan.md` P0/P1) so planning debt does not silently age.

### `notification-apprise`
- **Status:** Broken/incomplete — solid Apprise/Discord/Slack/webhook sidecar (~1.1k LOC + tests) but last CI on `main` failed: missing `../core` + `../contracts-notification` replaces, and golangci built with Go 1.24 vs module Go 1.26.4
- **Maturity:** Meaningful unit tests; README + Makefile/Dockerfile; missing `CHANGELOG`/`COMPATIBILITY`/`SECURITY`; CI still on `actions/checkout@v4` / golangci-lint-action@v6; no releases
- **Last activity:** 2026-07-22 — add CI/LICENSE and drop local notify proto (initial commit 2026-07-03)
- **Critical tasks:**
  - Repair CI: checkout `core` and `contracts-notification` (or publish and drop replaces); bump lint action so Go 1.26.4 modules lint
  - Add CHANGELOG/COMPATIBILITY and align with `contracts-notification` version expectations after proto drop
  - Upgrade workflow actions to the org’s current pin set (checkout/setup-go/golangci) to match healthier modules
  - Add integration coverage against a real/mock Apprise API (today tests are in-process only)

### `notification-default`
- **Status:** Broken/incomplete — feature-complete default notifier (Discord/Slack/webhook/SMTP + media event bridge, ~1.4k LOC + tests) but CI failed on the LICENSE/CI commit for the same `../core` / `../contracts-notification` replace gap
- **Maturity:** Strong unit tests; README documents SMTP/runtime `Configure`; no CHANGELOG/COMPATIBILITY; issues disabled on GitHub; older CI action versions
- **Last activity:** 2026-07-22 — README/CI/LICENSE + drop local notify proto (prior: 2026-06-16 media event subscription)
- **Critical tasks:**
  - Fix CI sibling checkouts (or published module deps) so `go test`/`go build` work in Actions
  - Add CHANGELOG/COMPATIBILITY and a release tag once green — this is the default notification path operators will load
  - Re-enable issues or track SMTP TLS/`EMAIL_*` edge cases elsewhere; runtime configure surface needs explicit test matrix for STARTTLS vs 465
  - Bump CI actions off Node-20-era `checkout@v4` / `setup-go@v5` like other modules

---

## Notes

- Analysis produced by parallel read-only agents (git history, README/CHANGELOG/COMPATIBILITY, `go.mod`, `muxcore.json`, tests/CI, `gh` issues/PRs where available).
- Status labels are judgment calls based on docs maturity, tests, CI health, and implementation depth — not formal release gates.
- `claude-working-directory` was intentionally excluded from this clone set and report.
