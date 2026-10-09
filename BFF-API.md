# mediauiprox BFF JSON contracts

Consumer SPA (`media-ui-app`) talks to `mvp/cmd/mediauiprox` on `:5173`.

The complete, test-enforced list of every registered route is the [Route inventory](#route-inventory) at the end of this file; the sections below describe request/response contracts for the main groups.

## List endpoints

### `GET /api/movies?page=&page_size=&library=`

Optional `library=musicvideos|homevideos` filters the movie catalog for companion pages:

- Path prefixes from `MEDIA_UI_LIBRARY_PATHS_FILE` / `-library-paths-file` (JSON `{ "musicvideos": ["/…"], "homevideos": ["/…"] }`)
- Root-folder name / genre tags `musicvideo` / `homevideo` when present
- Title/genre heuristic only when the config prefixes for that library are empty (`filter_mode: "heuristic"`)

Success includes `library` and `filter_mode` (`config` | `heuristic`). Movie rows may include `root_folder_path` and `library_type`.

### `GET /api/tv?page=&page_size=`

Success (`200`):

```json
{
  "items": [ /* Movie | TVShow */ ],
  "total": 0,
  "page": 1,
  "page_size": 48
}
```

`items` is always an array (never omitted). An empty library is `{ "items": [], "total": 0, ... }` — not an HTTP error.

### Movie item fields

| Field | Type | Notes |
|-------|------|--------|
| `id` | string | |
| `tmdb_id` | number | |
| `title` | string | |
| `year` | number | |
| `overview` | string | |
| `runtime` | number | |
| `vote_average` | number | |
| `genres` | string[] | always present |
| `poster_url` | string | absolute URL or `/images/movies/…` |
| `backdrop_url` | string | same |
| `has_file` | bool | |
| `status` | string | |
| `tagline` | string | |
| `created_at` | string | |
| `stream_url` | string | `/stream/movies/{id}` |
| `root_folder_path` | string | library root when set |
| `library_type` | string | set when `?library=` filtered |

### TV show item fields

Same poster/backdrop/stream conventions under `/images/tv/` and `/stream/tv/{episode_id}`. Includes nested `seasons[].episodes[]` with `has_file` / `stream_url`.

## Detail

- `GET /api/movies/{id}` → `{ "movie": {…} }`
- `GET /api/tv/{id}` → `{ "show": {…} }`

Missing entity → `404` with `{ "error": "…", "code": "movies.not_found" | "tv.not_found" }`.

## Live TV (file-backed companion)

### `GET /api/livetv`

Reads durable JSON (`MEDIA_UI_LIVETV_FILE`, shared with admin `ADMIN_UI_LIVETV_FILE`):

```json
{
  "channels": [{ "id": "ch1", "name": "…", "number": "1", "now_playing": { "title": "…", "start": "…", "end": "…" } }],
  "guide": [{ "channel_id": "ch1", "title": "…", "start": "…", "end": "…" }],
  "recordings": [],
  "timers": [],
  "available": true,
  "source": "MEDIA_UI_LIVETV_FILE"
}
```

`now_playing` is derived from in-window `guide` rows. Physical tuners / EPG grabbers are out of scope (decision B waiver).

### `POST /api/livetv/timers`

Appends a timer to the same JSON file. Admin/manager only (DVR scheduling uses household storage).

## Errors

Backend/gRPC failures return JSON (not silent empty lists):

```json
{ "error": "human message", "code": "movies.list_failed" }
```

Typical HTTP status: `400` / `401` / `404` / `502` / `503` from gRPC code mapping.

Operator routes add `403 operator.forbidden` (no admin/manager role), `403 operator.admin_required` (`root_folder_path` without admin) and `413 operator.body_too_large` (see [Operator route roles](#operator-route-roles)).

Parental enforcement adds `401 parental.session_invalid`, `403 parental.blocked` / `parental.restricted_route` / `parental.policy_unconfigured` / `parental.policy_unverifiable` and `503 parental.policy_unavailable` / `parental.classification_unavailable` (see [Parental enforcement](#parental-enforcement-adr-0031)).

## Jellyfin play deep-link

### `GET /api/jellyfin/play?mux_id=`

Resolves a MuxCore library id through jellyfin item links → `{ "url": "https://…/web/index.html#!/details?id=…" }`.

`404` when unlinked or Jellyfin base URL unset; `503` when the jellyfin bridge is unreachable.

### `GET /api/plex/play?rating_key=`

Resolves a Plex rating key through plex `PlayURL` → `{ "url": "https://…/web/index.html#!/server/{machine}/details?key=/library/metadata/{rating_key}" }`.

`404` when Plex base URL or machine id is unset; `503` when the plex bridge is unreachable. PlayURL failure falls back to `Status` the same way Jellyfin play does.

## Playback resolve (native player)

### `GET /api/playback/resolve?src=`

Reads shared admin playback policy (`ADMIN_UI_PLAYBACK_FILE`) and returns the stream URL the SPA should use:

```json
{
  "stream_url": "/stream/movies/{id}",
  "mode": "direct",
  "resume_enabled": true,
  "transcoder_enabled": true,
  "prefer_direct_play": true,
  "max_bitrate_mbps": "80",
  "trickplay_enabled": false,
  "transcoder_available": false
}
```

When transcoding is enabled and direct play is not preferred, `mode` is `transcode` and `stream_url` is `/stream/hls?src=…` (BFF proxies a seekable HLS playlist from `media-transcoder`). `/stream/transcode` remains the piped fMP4 fallback.

Resolve is a C-PLAY route (see [Parental enforcement](#parental-enforcement-adr-0031)). Only `src` is read; the former `tags`, `parental_rating` and `unrated` query parameters and the blob `prefs.parental` check are removed and ignored. A parental denial returns `403 { "code": "playback.parental_blocked", "parental_code": "parental.blocked" }`.

## Optional library-plus sections

Proxied from module health HTTP (defaults `:9641` music, `:9651` books, `:9661` comics, `:9671` audiobooks). Soft when the module is down — **HTTP 200** with an honest payload (SPA shows “Coming soon” after calling the API):

### `GET /api/music` → media-music `GET /api/artists`
### `GET /api/books` → media-books `GET /api/authors`
### `GET /api/comics` → media-comics `GET /api/series`
### `GET /api/audiobooks` → media-audiobooks `GET /api/audiobooks`

```json
{
  "items": [],
  "total": 0,
  "available": false,
  "coming_soon": true,
  "library": "music",
  "code": "music.unavailable",
  "message": "Coming soon — enable the library-plus spool tag…"
}
```

When the module responds, `available: true` and `items` are the upstream JSON array rows.

`PATCH /api/books/{id}` (author) and `PATCH /api/audiobooks/{id}` accept `{ "monitored": true|false, "root_folder_path": "/data/books" }` — at least one field is required. Author path is the existing library-plus `path` field (Readarr-style root). Audiobook root assignment looks up the author and patches `/api/authors/{id}`. Book works, comic series/issues stay monitor-only (`id` + `monitored` required). Comics have no root-folders kind.

---

## Capabilities & discover

### `GET /api/capabilities`

Session required. Returns BFF feature flags and optional peer availability (transcoder, debrid, list-sync, library-plus modules, Jellyfin bridge).

### `GET /api/discover/{movies|tv|people}?q=`

TMDB-backed search/detail proxy when `metadata-tmdb` is reachable. Detail routes: `/api/discover/movies/{id}`, `/api/discover/tv/{id}`, `/api/discover/people/{id}`, cast/crew subpaths.

### `GET /api/graph/related?id=tmdb:{movie|tv}:{n}`

Related-titles rail for Movie/TV detail (`media-ui-app`). Proxies media-graph admin JSON `GET /api/graph/related?external_id=` on `GRAPH_HTTP_URL` (default `http://127.0.0.1:9731`, flag `-graph-http`). Optional `limit`, `rel`, and `depth` are forwarded.

The SPA `id` query is a graph **external id** (`tmdb:movie:550` / `tmdb:tv:1396`), not a `gn_*` node id.

Success (`200`) when the graph module answers:

```json
{
  "items": [
    {
      "id": 807,
      "title": "Se7en",
      "year": 1995,
      "overview": "",
      "poster": "",
      "vote_avg": 0,
      "media_type": "movie",
      "relation": "same_franchise",
      "content_rating": "R"
    }
  ],
  "available": true
}
```

`items` is always an array. Graph node attrs (`year`, `tmdb_id`, `overview`, `poster`, `vote_average`, `content_rating`) are mapped when present; ingest today typically has title/year/tmdb id only.

When media-graph is unset, unreachable, or returns a non-404 error: **HTTP 200** `{ "items": [], "available": false }` — the BFF never 5xx this route. HTTP 404 from graph (title not in the store) is `{ "items": [], "available": true }`.

Optional `GRAPH_MODULE_TOKEN` / `GRAPH_HTTP_TOKEN` / `-graph-token` is sent as `Authorization: Bearer …` when graph admin JSON requires a token (loopback clients skip auth). Enable the module with `MVP_ENABLE_MEDIA_GRAPH=1`.

`GET /api/capabilities` includes `features.graph` when `GRAPH_HTTP_URL/healthz` is live.

## Userdata (session)

### `GET|POST|DELETE /api/userdata`

Progress, favorites, watched state, and preferences stored under `MEDIA_UI_USERDATA_DIR` / userdata-local when configured. JSON bodies mirror admin playback policy fields where shared.

With `USERDATA_LOCAL_URL` set (and `USERDATA_PREFER_MESH` not `0`), reads and writes go to userdata-local's canonical `GET|PUT /api/userdata` route through the checked provider client (ADR-0033; see [Userdata provider transport](#userdata-provider-transport)) with the session's bearer and `X-MuxCore-User-Id`, no query string. If that call fails (transport, module admission or a non-200 status) the BFF serves its local `MEDIA_UI_USERDATA_DIR` store instead and logs the outage once; this blob fallback is never used for parental policy and is not reported as a provider success. Sessions without an auth-local bearer use the local store only.

GET/PUT responses include `user_id` (household id used as the parental PIN salt: `SHA-256(userID+":"+pin)`) and echo `X-MuxCore-User-Id` with the same value. Scope is the session principal; admin/manager may override via `?user_id=` or `X-MuxCore-User-Id` (same header as admin-ui userdata sync).

## Acquisition status

### `GET /api/acquisition`

Probes configured indexer/downloader health HTTP (Pirate Bay, native torrent, qBittorrent, SABnzbd, native usenet, debrid) and `indexer-torznab` gRPC `ListIndexers` (Prowlarr/Jackett children). Peers that are unset or down are listed as `live: false`. `hasIndexer` is true when Pirate Bay health is live **or** at least one Torznab/Prowlarr indexer is `configured`. Response also includes `indexers: [{ id, name, protocol, language, configured }]` and `indexers_available`.

```json
{
  "ready": false,
  "hasIndexer": true,
  "hasDownloader": false,
  "message": "An indexer is up, but no downloader is connected.",
  "peers": [
    { "id": "indexer-piratebay", "kind": "indexer", "label": "Pirate Bay indexer", "live": true },
    { "id": "downloader-native-torrent", "kind": "downloader", "label": "Native torrent", "live": false }
  ]
}
```

`ready` is true only when at least one indexer **and** one downloader are live. HTTP peers answer `< 500` on `/healthz` or `/`. Torznab/Prowlarr counts when `ListIndexers` returns a configured child. Also reports `live_grab_allowed`, `downloader_mode` (`fixture` unless `DOWNLOADER_ENGINE` is live), `indexer_mode`, and `vpn.{configured,conf_present}` from `WG_CONF`. Live grab stays opt-in: fixture engines are always allowed; live torrent needs a readable `WG_CONF`. Defaults: `INDEXER_PIRATEBAY_HTTP_URL`, `INDEXER_TORZNAB_GRPC_CLIENT_ADDR` (`:9486`), `DOWNLOADER_TORRENT_HTTP_URL` (`:9464`), `DOWNLOADER_QBIT_HTTP_URL`, `DOWNLOADER_SAB_HTTP_URL`, `DOWNLOADER_USENET_HTTP_URL`, `DEBRID_HTTP_URL`.

### `GET /api/indexers`

Household Prowlarr/Jackett catalog from `indexer-torznab` `ListIndexers`. `{ available, indexers: [{ id, name, protocol, language, configured }] }`. Module unset/down → `{ available: false, indexers: [] }`. Not a feature key — Settings → Acquisition shows the catalog.

### `POST /api/indexers` / `PATCH /api/indexers/{id}` / `DELETE /api/indexers/{id}`

Admin only. Proxies Prowlarr Torznab/Newznab CRUD (`name`, `base_url`, `api_key`, `implementation`, `enable`). `api_key` is write-only; responses use `has_api_key`. Direct Torznab/Jackett (no `PROWLARR_URL`) returns `indexers.unsupported`.

`GET /api/capabilities` includes `features.acquisition` when `ready` is true.

## Watch Together (household SyncPlay)

### `POST /api/watch-together`

Creates a room. Body: `{ mediaId, src, title, positionSeconds, playing }`. `src` is required. Response includes `id`, `hostToken` (store in the host tab only), and `youAreHost: true`.

### `GET /api/watch-together/{id}`

Public room clock for guests. Never returns `hostToken`.

### `POST /api/watch-together/{id}`

Host sync. Body: `{ positionSeconds, playing }`. Requires `X-Watch-Together-Host` matching the create token (or the signed-in host user). Guests receive HTTP 403.

Rooms persist under `MEDIA_UI_USERDATA_DIR/watch-together.json` and expire after 24 hours. SPA join URL: `/player?src=…&together={id}`.

`GET /api/capabilities` includes `features.watchTogether` and `features.offline`.
`GET /api/capabilities` includes `features.formats` when media-custom-formats gRPC is connected.

## Quality / TRaSH packs

### `GET /api/formats`

Lists custom formats and quality profiles from `media-custom-formats`. `{ available, formats: [{ id, name, score, ruleCount, rules: [{ field, op, value, negate }] }], profiles[], release_profiles[] }`.

### `POST /api/formats/sync-trash`

Imports Recyclarr-compatible TRaSH packs (`SyncTrashGuides`). Body: `{ scoreSet, importProfiles, services, official }`. Default uses the bundled household fixture (or the module's `FORMATS_TRASH_GUIDES_PATH`). `{ official: true }` downloads the hardcoded TRaSH-Guides GitHub archive. The whole route is admin/manager only, bundled pack included (T-M5-12). Client `guidesPath` is ignored except the `official` sentinel; arbitrary URLs/paths are not accepted.

### `POST /api/formats/score`

Preview a release name: `{ title, profileId? }` → total/format scores and matching formats.

### `POST /api/formats/profiles` · `PATCH|DELETE /api/formats/profiles/{id}`

Household quality-profile CRUD (admin-ui `/formats/profiles`). Admin/manager only. POST/PATCH body `{ name, min_score, cutoff_score, upgrade_allowed, upgrade_delay_minutes, format_scores }`. GET `/api/formats` stays available to any signed-in session so movie/TV pickers work. Not a new feature key — Settings Quality is already gated by `features.formats`.

### `POST /api/formats` · `PATCH|DELETE /api/formats/{id}`

Household custom-format CRUD (admin-ui `/formats`). Admin/manager only. POST/PATCH body `{ name, default_score|score, rules: [{ field, op, value, negate }] }`. `field` is typically `title`/`size`/`seeders`; `op` is `matches`/`contains`/`gt`/`lt`/`gte`/`lte`. Same `features.formats` tab; writes use admin/manager.

### `GET|POST /api/formats/release-profiles` · `PATCH|PUT|DELETE /api/formats/release-profiles/{id}`

Household Radarr/Sonarr release restrictions (admin-ui `/formats/release-profiles`). GET `{ available, profiles: [{ id, name, preferred, must_contain, must_not_contain, preferred_score, enabled }] }` — any signed-in session; module down → `{ available: false, profiles: [] }`. POST/PATCH `{ name, preferred, must_contain, must_not_contain, preferred_score, enabled }` upserts (`preferred_text` / `must_contain_text` / `must_not_contain_text` also accepted as CSV). Writes admin/manager. Not a new feature key — Settings Quality already gated by `features.formats`.

## Monitor / unmonitor

### `PATCH /api/movies/{id}` · `PATCH /api/tv/{id}` · `PATCH /api/tv/seasons/{id}` · `PATCH /api/episodes/{id}`

Body `{ "monitored": true|false, "quality_profile_id": "qp_uhd", "root_folder_path": "/data/movies" }`. At least one field is required on movie/series PATCH. Forwards to media-movies `UpdateMovie` or media-tvshows `UpdateTVShow` / `UpdateSeasonMonitored` / `UpdateEpisodeMonitored`. Season monitor updates cascade to that season's episodes. Admin/manager only; a body that names `root_folder_path` (any spelling `encoding/json` matches, empty value included) needs admin, as root-folder changes do (RULE-AUTH-3). The same holds for `PATCH /api/music/{id}`, `/api/books/{id}`, `/api/comics/{id}` and `/api/audiobooks/{id}`; album, work, issue, season and episode PATCHes take only `monitored`. After a successful movie/series/episode PATCH the BFF also calls automation `UpdateQueueItem` so Search now / grab scoring uses the new profile immediately (`queue_id` matches wanted id, library item id, or TV series id). Automation down does not fail the library PATCH.

### `GET /api/roots`

Household library roots from `media-root-folders` (`?kind=movies|tv|…`). `{ available, roots: [{ id, path, name, media_kind, accessible, free_bytes, total_bytes, is_default }] }`. Module unset/down → `{ available: false, roots: [] }`. List is available to any signed-in household session (movie/TV root picker).

### `GET /api/roots/browse` · `POST /api/roots` · `DELETE /api/roots/{id}`

Arr-style library roots for admin/manager. Browse (`?path=`) returns `{ available, path, parent, entries: [{ name, path, is_dir }] }` (module prefixes still apply). POST body `{ path, name, media_kind, is_default }` creates a root. DELETE removes the catalog row (not files on disk). Module unset/down → browse `{ available: false, entries: [] }`; create/delete HTTP 503. Not a feature key — Settings hides Libraries when the session is not admin/manager.

### `GET|POST /api/scan`

Household library scan (admin-ui `/library-scan`). Admin/manager only. GET `{ available, status, last_scan_at, watch_dirs, total_imported, last_found, last_imported, last_skipped, last_error }` — scanner unset/down → `{ available: false, … }`. POST `{ type: watch|library_roots, path?, media_type? }` runs `Scan` or `ScanLibraryRoots` and returns `{ type, files_found, files_imported, files_skipped, message }`. Not a feature key — Settings → Libraries hides scan actions when the scanner is unavailable.

### `GET|POST /api/scan/watch-dirs` · `PATCH|PUT /api/scan/watch-dirs/{id}` · `DELETE /api/scan/watch-dirs/{id}`

Household Arr-style watch/download folders. Admin/manager only. GET `{ available, dirs: [{ id, path, media_type, library_path, tv_library_path, music_library_path, enabled, created_at }] }`. POST `{ path, media_type, library_path }` calls `AddWatchDir`. PATCH/PUT `{ enabled?, path?, media_type?, library_path?, tv_library_path?, music_library_path? }` pauses (`SetWatchDirEnabled`) or retargets (`UpdateWatchDir`) without delete-and-re-add. DELETE removes the watch. Scanner unset/down → GET `{ available: false, dirs: [] }`. Same Libraries privilege gate.

### `GET /api/rename/preview` · `POST /api/rename`

Radarr/Sonarr Preview Rename. `GET ?movie_id=` or `?tv_id=` (`&episode_id=` optional) returns `{ available, items: [{ file_id, episode_id, title, current_path, new_path, new_filename, changed, quality }] }`. `POST` (admin/manager only) body `{ movie_id }` or `{ tv_id, episode_id? }` applies changed rows (`media-rename` Execute) and updates `movie_files` / `episode_files`. Module unset/down → GET `{ available: false, items: [] }`. Not a feature key — the SPA hides the card when unavailable.

### `POST /api/rename/organize`

Household Organize (admin-ui `/rename/organize` → `BatchRename`). Admin/manager only. Body `{ directory, media_type, dry_run, import_mode }`. `directory` must be a configured library root or watch-dir path (or a child of one). Relative paths, `/`, and anything outside those prefixes return `organize.path_forbidden`. No roots and no watch folders → `organize.root_required`. Default `dry_run` is true. Response `{ available, directory, media_type, dry_run, total, renamed, errors, items: [{ original, renamed_to, success, error }] }`. Not a feature key — Settings Naming is already admin/manager.

### `GET|POST /api/rename/templates` · `PATCH|DELETE /api/rename/templates/{id}`

Household naming-template CRUD (admin-ui `/rename/templates`). Admin/manager only. GET `?media_type=` optional returns `{ available, templates: [{ id, name, media_type, pattern, is_default, updated_at }] }`. POST `{ name, media_type, pattern, is_default }` creates. PATCH `{ name, pattern, is_default }` updates. DELETE removes (module refuses the last template for a media type). Module unset/down → GET `{ available: false, templates: [] }`; writes HTTP 503. Not a feature key — Settings hides Naming when the session is not admin/manager.

## Series overrides

### `GET /api/tv/{id}/override` · `PUT /api/tv/{id}/override` · `DELETE /api/tv/{id}/override`

Per-show grab delay and preferred/ignored release groups (`ListSeriesOverrides` / `UpsertSeriesOverride` / `DeleteSeriesOverride`). PUT body `{ delay_minutes, preferred_groups, ignored_groups }`. `{ available, found, override }`. PUT/POST/DELETE are admin/manager only; GET stays available to any signed-in session. Automation unset/down → GET `{ available: false, found: false }`.

## Remove / refresh

Every route in this section is admin/manager only (T-M5-12), including `?delete_files=1`, which matches `DELETE /api/movies/{id}/files/{fileId}`.

### `DELETE /api/movies/{id}` · `DELETE /api/tv/{id}`

Remove a title from the library. `?delete_files=1` also deletes files on disk (Radarr/Sonarr-style). `{ "removed": true, "delete_files": bool }`.

### `DELETE /api/movies/{id}/file`

Remove media files for a movie but keep the title (`ListFiles` + `RemoveFile`). `?delete_files=1` deletes files on disk. `{ "removed": bool, "delete_files": bool, "files": n }`.

### `DELETE /api/episodes/{id}/file`

Remove the episode's media file (`RemoveEpisodeFile`). `?delete_files=1` deletes the file on disk.

### `POST /api/movies/{id}/refresh` · `POST /api/tv/{id}/refresh`

Refresh TMDB metadata for the title. `{ "refreshed": true }`.

## Release blocklist

### `GET /api/blocklist`

Lists automation `release_blacklist` rows (`ListBlocklist`). `{ items, total, available }`. Rows include `title`, `guid`, `wanted_item_id`, `reason`, `loop`. Automation unset/down → HTTP 200 `{ available: false, items: [] }`.

### `POST /api/blocklist/clear`

Unblock one release `{ wanted_item_id, guid? }` or `{ clear_all: true }`. Forwards to `ClearBlocklist`. Admin/manager only, like `POST /api/releases/block`. Interactive Search still uses `POST /api/releases/block` to add entries.

## Delay profiles

### `GET /api/delay-profiles`

Lists automation per-protocol grab delays (`ListDelayProfiles`). `{ available, profiles: [{ protocol, wait_minutes }] }`. Seeded defaults are torrent=15, usenet=0. Automation unset/down → HTTP 200 `{ available: false, profiles: [] }`.

### `PUT /api/delay-profiles` · `POST /api/delay-profiles`

Upsert one protocol delay. Body `{ protocol, wait_minutes }` (`wait_minutes` 0–10080). Forwards to `UpsertDelayProfile`. Admin/manager only (GET stays open). The grab loop waits this long after first seeing a release before dispatching.

## Manual import

### `GET /api/import/candidates`

Lists unimported files in scanner watch/download folders (`media-scanner` `ListImportCandidates`). `{ items, total, available }`. Rows include `path`, `title`, `media_type`, `year`, `size`. When the scanner is unset or down: `{ available: false, items: [] }`.

### `POST /api/import`

Imports one candidate via `ImportPath`. Body: `{ path, title?, media_type?, year?, tmdb_id?, season_number?, episode_number? }`. `path` is required. Response: `{ imported, skipped, found, message }`. Admin/manager only, like watch folders and `POST /api/scan`.

## Saved offline (browser cache)

Household browsers save a title via Cache API (`media-ui-app` `/offline`). Playback prefers the cached blob when `src` matches. This is device-local — not a server download queue (`/activity`).

## Request policy (Seerr-style quotas)

Proxied to request-media `GET|PUT /api/request-policy`. The BFF forwards the signed-in user's auth-local bearer and `X-Caller-Id` (session user id; compatibility, one release). Client identity headers are stripped (ADR-0019). `PUT` requires an approve role. HTTP 429 `{ "code": "request.quota" }` when a household member exceeds pending or weekly limits.

## Watchlist & collections

### `GET|POST|DELETE /api/watchlist`

Requires `media-list-sync` when adding external list sources; local watchlist rows stored in userdata.

### `GET|POST /api/lists` · `POST /api/lists/sync` · `PATCH|PUT /api/lists/{id}` · `DELETE /api/lists/{id}`

Household Arr/Seerr import lists (admin-ui `/list-sync`). Admin/manager only. GET `{ available, sources: [{ id, name, type, enabled, list_url, quality_profile_id, root_folder_path, has_api_key, … }] }` — API keys are never returned. POST `{ name, type, list_url, username, api_key, quality_profile_id, root_folder_path, … }` creates a Trakt/IMDb/Plex/Jellyfin/Radarr/Sonarr source. PATCH/PUT `{ enabled?, name?, list_url?, quality_profile_id?, root_folder_path?, … }` pauses or edits without delete-and-re-add. POST `/api/lists/sync` runs `SyncNow` for every enabled source. POST `/api/lists/{id}/sync` syncs that list only (`SyncNow.source_id`). POST `/api/lists/{id}/test` fetches the list without importing (`TestSource`) → `{ ok, message, items_found }`. Module unset/down → GET `{ available: false, sources: [] }`. Not a feature key — Settings hides Lists when the session is not admin/manager.

### `GET /api/lists/history` · `GET /api/lists/items`

Household import-list ops (admin-ui `/list-sync/history` and `/list-sync/items`). Admin/manager only. History `{ available, entries: [{ id, source_id, source_name, status, items_found, items_new, error, started_at, completed_at }], total }`. Items `{ available, items: [{ id, source_id, title, year, media_type, status, action, tmdb_id, imdb_id, matched_item_id, … }], total }` with optional `?source_id=` `?media_type=` `?matched_only=true`. Module unset/down → `{ available: false, entries|items: [] }`. Not a feature key — Settings Lists already admin/manager.

### `POST /api/migrate`

Household Arr library import (admin-ui `/migrate`). Admin/manager only. Body `{ service: radarr|sonarr|lidarr, base_url, api_key, dry_run, remap_from, remap_to }`. `dry_run` defaults true. Returns `{ dry_run, fetched, imported, skipped, errors, preview, scan_note }`. Arr API keys are never echoed. Live import uses movies/TV/music gRPC (`AddMovie`/`AddTVShow`/`AddArtist`) then `ScanLibraryRoots` when media-scanner is connected. Lidarr needs `MUSIC_GRPC_CLIENT_ADDR`. Not a feature key — Settings hides Import when the session is not admin/manager.

### `GET /api/tags` · `POST /api/tags` · `DELETE /api/tags/{id}`

Household Arr tags (movies + TV + music catalogs). GET `?media=movie|tv|music|artist|all` returns `{ available, tags: [{ id, label, media, created_at }] }` to any signed-in session. POST `{ label, media: movie|tv|music|artist }` and DELETE `?media=` are admin/manager. Module unset/down → GET `{ available: false, tags: [] }`. Not a feature key — Settings hides Tags when the session is not admin/manager.

### `GET|PUT /api/movies/{id}/tags` · `GET|PUT /api/tv/{id}/tags` · `GET|PUT /api/music/{id}/tags`

Per-title tag assignment (music id is the artist). GET `{ available, tags }`. PUT `{ tag_ids: [] }` replaces the item’s tags (admin/manager).

### `GET|POST /api/movies/{id}/titles` · `DELETE /api/movies/{id}/titles/{titleId}` · `GET|POST /api/tv/{id}/titles` · `DELETE /api/tv/{id}/titles/{titleId}`

Household Arr alternate titles used for grab/import matching. GET `{ available, titles: [{ id, title, clean_title, source, user }] }` — any signed-in session; module unset/down → `{ available: false, titles: [] }`. POST `{ title }` adds a user title (admin/manager). DELETE removes a user-sourced title only (the module refuses primary/TMDB rows). Not a feature key — movie/TV detail shows the card when the catalog is available.

### `GET /api/movies/{id}/history` · `GET /api/tv/{id}/history` · `GET /api/music/{id}/history`

Household Arr title history (grab / import / delete file / delete item) from movies, TV, and music `ListHistory`. Music id is the artist. Any signed-in session. Optional `?event=grab|import|delete_file|delete_item`. Response `{ available, items: [{ id, event_type, item_id, title, source_title, quality, indexer, file_path, download_id, created_at }], total }`. Module unset/down → `{ available: false, items: [] }`. Not a feature key — movie/TV/artist detail hides the card when unavailable.

### `GET /api/notifications` · `PUT|POST /api/notifications` · `POST /api/notifications/test`

Household Arr Connect (notification-default). Admin/manager only. GET `{ available, channels: [{ id, enabled, description, last_error, last_success_at }] }` — webhook URLs and SMTP passwords are never returned. Module unset/down → `{ available: false, channels: [] }`. PUT `{ channel: discord|slack|webhook|email, webhook_url?, smtp_host?, smtp_port?, smtp_user?, smtp_pass?, smtp_from?, to?, enabled? }`. POST `/api/notifications/test` `{ channel }` sends a fixture ping via `Notify`. Not a feature key — Settings → Notifications shows Connect fields only for admin/manager.

### `GET /api/backups` · `POST /api/backups` · `DELETE /api/backups/{id}` · `POST /api/backups/{id}/restore`

Household backups (`backup-local`). Admin/manager only. GET `{ available, restore_dir, backups: [{ id, created_at, size_bytes, modules, checksum }] }`. Module unset/down → `{ available: false, backups: [] }`. POST create uses configured `BACKUP_SOURCE_DIRS` (no client paths). Restore always extracts into `BACKUP_RESTORE_DIR` on the backup-local host — the SPA cannot send a target path. Restore is 400 `backups.restore_dir_required` when that env is empty. Not a feature key — Settings hides Backups when the session is not admin/manager.

### `GET /api/collections` · `GET /api/collections/{id}` · `PATCH /api/collections/{id}` · `POST /api/collections/{id}/sync`

Box sets from `media-movies`. GET list `{ available, items: [{ id, name, movie_count, monitored }] }`. GET detail includes movies plus `monitored`, `search_on_add`, `quality_profile_id`, `root_folder_path`. PATCH `{ monitored, search_on_add?, quality_profile_id?, root_folder_path? }` and POST sync `{ add_missing? }` are admin/manager. Sync adds missing TMDB collection parts. Not a feature key — writes use `canManageLibrary`.

## Invites & onboarding

### `GET /api/invite/peek?token=`

Public. Returns invite metadata before redemption.

### `POST /api/invite/redeem`

Public. Body: `{ "token", "username", "password" }` — creates auth-local user via auth HTTP.

### `GET /api/invites` · `POST /api/invites` · `DELETE /api/invites/{id}`

Household Wizarr-style invite admin. Requires BFF session role `admin` or `manager`, and a linked auth-local token from login. GET `{ available, invites: [{ id, prefix, role, max_uses, use_count, expires_at, revoked, join_url? }] }`. Auth unset/unlinked → GET `{ available: false, invites: [] }`. POST body `{ role, max_uses, ttl_hours }` returns `{ invite }` with a one-time `join_url` (`/invite/{token}`).

### `GET /api/users` · `PATCH /api/users/{id}` · `DELETE /api/users/{id}`

Household user admin (Wizarr companion to invites). Same admin/manager + linked auth-local token gate. GET `{ available, users: [{ id, username, roles, totp_enabled, created_at }] }`. PATCH body `{ role }` or `{ roles }` updates RBAC. DELETE removes the account (BFF and auth-local both reject self-delete; auth-local also rejects last-admin delete/demote). Auth unset/unlinked → GET `{ available: false, users: [] }`. Not a feature key.

### `GET /api/keys` · `POST /api/keys` · `DELETE /api/keys/{id}` · `POST /api/keys/{id}/rotate`

Household API keys (auth-local tokens). Same admin/manager + linked auth-local token gate. GET `{ available, keys: [{ id, name, prefix, user_id, username, scopes, created_at, last_used }] }` — raw secrets are never listed. POST `{ name, user_id?, scopes? }` and rotate return `{ token, secret }` once. Auth unset/unlinked → GET `{ available: false, keys: [] }`. Not a feature key — Settings hides API keys when the session is not admin/manager.

## Quick Connect & TV login

### `GET|POST /api/quickconnect`

Jellyfin-style quick connect code flow (file-backed store under userdata dir).

### `GET|POST /api/tv/login` · `POST /api/tv/login/totp`

Device/TV pairing: returns short code or completes TOTP step; redirects into session cookie on success.

## Mobile auth handoff

### `GET /api/mobile/auth/login` · `GET /api/mobile/auth/done` · `POST /api/mobile/session`

Deep-link friendly mobile login: poll `done` after browser auth, exchange for bearer/session.

## Password reset (consumer)

### `POST /api/password-reset` · `GET /api/password-reset` · `POST /api/password-reset/{id}/dismiss` · `POST /api/password-reset/{id}/password`

Public POST queues a reset request (`MEDIA_UI_PASSWORD_RESET_FILE`, shared with admin-ui). GET lists pending `{ available, count, requests: [{ id, username, note, created_at, user_id, user }] }` for admin/manager (user_id is filled when auth-local has a matching account). Dismiss marks the row closed. Set-password `{ password }` (min 8) proxies `POST /api/users/{id}/password` on auth-local, then marks matching usernames resolved. Never echoes the new password. Not a feature key — Settings → Users shows the queue.

## Debrid (optional)

When `downloader-debrid` HTTP is up:

- `POST /api/debrid/add` — enqueue magnet/hoster (admin/manager only)
- `GET /api/debrid/vfs` — virtual file listing
- `GET /api/debrid/stream` — proxied playback URL

## Playback helpers (native player)

Session required unless noted.

| Route | Purpose |
|-------|---------|
| `GET /api/playback/subtitles?src=` | List subtitle tracks (`media-subtitles`) |
| `GET /api/playback/subtitles/{id}?src=` | Serve subtitle bytes. Restricted principals: only a track id the same session was shown by the list route (see Parental enforcement) |
| `GET /api/subtitles/search` | Household player search (`title`, `language`, `year`, `imdb_id`, `season`, `episode`) → `{ available, results: [{ id, provider, title, language, format, release, downloads }] }`. Module unset/down or missing OpenSubtitles key → `{ available: false, results: [] }`. |
| `POST /api/subtitles/download` | Body `{ id, provider, language?, media_file_id? }` downloads via `DownloadSubtitle` and returns `{ track_url: /api/playback/subtitles/{id}, language, label }`. |
| `GET /api/subtitles/wanted` · `POST /api/subtitles/wanted` · `DELETE /api/subtitles/wanted/{id}` · `POST /api/subtitles/wanted/search` | Household Bazarr wanted list. Admin/manager. GET `{ available, wanted, total }`. Module unset/down → `{ available: false, wanted: [] }`. POST `{ title, language?, media_type?, season?, episode?, imdb_id?, tmdb_id? }`. Search-now `{ media_ids?, limit? }` → `{ searched, downloaded }`. |
| `GET /api/subtitles/providers` · `PUT /api/subtitles/providers/{id}` | Provider catalog (`opensubtitles`, `fixture`). PUT `{ enabled }`. Soft-fail GET `{ available: false, providers: [] }`. |
| `GET /api/subtitles/history` · `POST /api/subtitles/history/clear` | Download history. Soft-fail GET `{ available: false, history: [] }`. |
| `GET /api/subtitles/blacklist` · `DELETE /api/subtitles/blacklist/{id}` | Bazarr subtitle blacklist. Admin/manager. GET `{ available, entries: [{ id, title, provider, language, reason, file_id, created_at }], total }`. Module unset/down → `{ available: false, entries: [] }`. DELETE removes one entry so that release can be downloaded again. Not a feature key — Settings → Subtitles ops already gated by admin/manager. |
| `GET /api/subtitles/profiles` · `PUT /api/subtitles/profiles` | Language profiles. PUT `{ name, languages: "eng,spa+hi", is_default? }`. Not a feature key — Settings → Subtitles shows ops only for admin/manager. |
| `GET /api/playback/segments?src=` | Intro/outro/credits skip segments (`media-intro-outro`) |
| `GET /api/playback/chapters?src=` | Chapter markers (`media-ffprobe`) |
| `GET /api/playback/analysis?src=` | Container/codec summary for OSD |
| `POST /api/playback/session` | Native player session ingest → playback-monitor `POST /ingest` |
| `GET /api/sessions` | Active household streams → playback-monitor `GET /sessions/active` |
| `POST /api/sessions/{id}/stop` | Stop a live stream (Jellyfin dashboard-style). Marks the monitor session kicked; Jellyfin/Emby sessions also call jellyfin `TerminateSession`, and Plex sessions call plex `TerminateSession` when `-plex-grpc` / `PLEX_GRPC_CLIENT_ADDR` is reachable. Native player progress after a kick returns `{ stopped: true }` so the tab pauses. |
| `GET /api/history` | Stopped household plays → playback-monitor `GET /history` (Jellyfin handoff + native). `{ items, total, available }` with `watched` when position is ≥90% or within 60s of the end. Monitor unset/down → HTTP 200 `{ available: false, items: [] }`. |
| `GET /stream/hls?src=` | Seekable HLS transcode (`media-transcoder`). Forwards `max_height`, `audio_index`, `subtitle_index`, `start` (remount ffmpeg at that second). Redirects to `/stream/hls/{key}/index.m3u8`. |
| `GET /stream/hls/{key}/{file}` | HLS playlist (`index.m3u8`) or MPEG-TS segment. |
| `GET /stream/transcode?src=` | Piped fMP4 fallback. Forwards `start`, `max_height`, `audio_index`, `subtitle_index` (PGS/VobSub burn-in). |
| `GET /stream/trickplay?src=` | Trickplay sprite sheet |

`POST /api/playback/session` accepts `{ event_type, session_id, media_id, title, media_type, position_seconds, duration_seconds, is_paused, is_transcode }`. `event_type` is `started` / `progress` / `stopped` (or `playback.*`). The BFF fills household user + `server_type=native` and forwards to `PLAYBACK_MONITOR_HTTP_URL` (`-playback-monitor-http`, default `http://127.0.0.1:8560`) with `PLAYBACK_MONITOR_HTTP_TOKEN`. When the monitor is unset or down the route still returns **HTTP 202** `{ "accepted": true, "forwarded": false }` so playback never fails.

Session ownership (T-M5-13): the client's `session_id` is not forwarded as is. The BFF sends playback-monitor `ExternalSessionID = native:<url.QueryEscape(tenant)>:<url.QueryEscape(user id)>:<session_id>`, built from the verified BFF session, with `ServerID=muxcore-native` and `UserID` from the session. playback-monitor keys sessions by `(server_id, external_session_id)`, so every member's ids live in that member's own namespace: another member who sends the same `session_id` reaches a different monitor session and cannot rewrite `user_id`, inject progress/title, stop, or see the kick of someone else's session. Jellyfin/Plex/Emby rows use their own `server_id` and cannot be reached either. An omitted or blank `session_id` becomes `media:<media_id>` inside the caller's namespace. A non-blank `session_id` must be at most 256 bytes of printable text after trimming (`unicode.IsPrint`: no control, bidi, zero-width or separator characters other than ASCII space); otherwise **400** `playback.session_id_invalid` and nothing is forwarded. The binding is derived, not stored: the BFF keeps no per-session table, so memory does not grow with session ids, and after a BFF restart the same user's events still reach the same monitor session. Unknown ids from a verified user are new sessions owned by that user (playback-monitor already starts a row on `progress`/`stopped` for an unknown key). The response `session_id` is playback-monitor's row id, as before. The principal must resolve from one valid BFF session credential. When auth is required (the default, `MEDIA_UI_REQUIRE_AUTH`), a request with no resolvable principal or a blank user id gets **401** `api.unauthorized` before the body is read and nothing is forwarded. This includes a stale `session` cookie sent together with a valid bearer (`Authorization: Bearer` or `X-MuxCore-Session`): the session gate and downstream principal both choose the nonempty cookie first, so such a request is refused before a handler runs. A client that hits this clears the stale cookie. The `anonymous` identity exists only with `MEDIA_UI_REQUIRE_AUTH=0` (dev) and a request without a session. Upgrade note: the key changes, so a native session that is active while the BFF is upgraded continues under a new monitor row, and an operator stop (kick) issued before the upgrade may not reach that player once, because it was recorded on the old key. The old row is not left active indefinitely: playback-monitor expires stale active rows when sessions are listed, after `PLAYBACK_MONITOR_ACTIVE_TIMEOUT` (default 15 minutes; the expiry can be disabled, in which case old rows stay active until stopped). An operator stop of the old row is optional, not the only cleanup.

`GET /api/sessions` returns `{ items, total, available }` with snake/camel-normalized rows (`title`, `user`, `href`, `paused`, `transcode`, `serverType`, progress). Listing uses the same operator token as ingest (`PLAYBACK_MONITOR_HTTP_TOKEN`). When the monitor is unset or down the route still returns **HTTP 200** `{ "available": false, "items": [] }`. `POST /api/sessions/{id}/stop` returns `{ stopped, id, serverType, jellyfinStopped }`. It is admin/manager only: it can stop any household member's stream, including Jellyfin and Plex sessions, and the BFF cannot prove that a session belongs to the caller (see [Operator route roles](#operator-route-roles)).

`GET /api/capabilities` includes `features.playbackMonitor` when monitor `/healthz` is live.

All `/api/playback/*` routes return JSON errors `{ "error", "code" }` on upstream failure (no silent empty success for mutating paths). Invalid session payloads are `400`; monitor outages are `202`, not `5xx`.

## Auth session

- `GET /login` — redirect to `AUTH_HTTP_URL/login?redirect=…`
- `GET /auth/callback?code=` — exchange OAuth-style code for `session` cookie
- `GET /logout` — confirm page only; it does **not** end the session (a cross-site link cannot log users out)
- `POST /logout` — revoke the session (cookie and/or bearer), clear the cookie, `303` to `/login` (`{"logged_out":true}` with `Accept: application/json`)
- `GET /api/session` · `GET /api/me` — current household identity (`user_id`, `username`, `roles`, optional `tenant_id`). Session required. Also sets `X-MuxCore-User-Id`. Used by media-ui `refreshCurrentUserId` for PIN salt.

Set `MEDIA_UI_PUBLIC_URL` and `MEDIA_UI_TRUSTED_PROXIES` (dawn/dusk `/128` CIDRs) when behind edge nginx so Secure cookies and callback origins match `https://mux.zem.systems`.

Sessions persist across BFF restarts (NFR-REL-003) in `MEDIA_UI_USERDATA_DIR/sessions.json` (0600). Entries are keyed by SHA-256 of the session token; the user's auth-local token is AES-256-GCM sealed with `MEDIA_UI_SESSION_KEY` or a generated `session.key` (0600) in the same dir. Without `MEDIA_UI_USERDATA_DIR`, sessions are memory-only.

T-M5-11 / ADR-0026: before every protected request, a session carrying an upstream bearer is revalidated through the auth provider's `Validate` RPC. This applies to browser cookies and native BFF session tokens (`Authorization: Bearer` / `X-MuxCore-Session`), also when `MEDIA_UI_REQUIRE_AUTH=0`. A nonempty cookie takes precedence over native headers. Configure `AUTH_GRPC_CLIENT_ADDR` (or `-auth-grpc`): host default `127.0.0.1:9403`, Compose/Helm/Kustomize `auth-local:9403`. It is a provider endpoint, separate from `AUTH_HTTP_URL` / `AUTH_HTTP_INTERNAL_URL` and core discovery; the existing mesh TLS settings apply. Other identity providers must implement compatible `Validate` semantics and use their own endpoint.

Validation has an eight-second bound (or the shorter request deadline), sends only the stored bearer as the credential, and does not cache success or errors. Valid responses refresh canonical username and roles, including empty values, without extending local expiry. User ID and tenant must match the local binding exactly; a blank/whitespace user ID or a changed/omitted tenant is rejected. The request uses one snapshot for roles, parental policy, userdata scope and forwarded identity/bearer. Logout, expiry or replacement while validation runs cannot resurrect a session or apply an old result to a replacement.

- Invalid/expired/revoked bearer, `Unauthenticated`, or principal mismatch: delete only the matching local binding and clear its cookie; APIs return **401 `api.unauthorized`**, streams return 401, browser pages redirect to login.
- Provider `PermissionDenied`: **403 `auth.forbidden`**, retaining the session.
- Missing/unavailable/unimplemented provider, cancellation, timeout or unusable response: **503 `auth.unavailable`** with `Retry-After: 1`, retaining the session for retry. Validation responses use `Cache-Control: no-store`.

Login, logout and other public recovery routes remain available during a provider outage. Quick Connect approval revalidates a linked approver, while anonymous registration/polling and the resulting local-only device session retain their existing behavior. Bearerless legacy/Quick Connect sessions cannot reflect provider revocation; parental routes still reject them with `parental.policy_unverifiable`. This slice does not add provider device grants, revoke API keys, or terminate already established streams; each new protected request (including HLS assets) is checked. Full all-device revocation/live propagation acceptance remains pending.

### Browser security (NFR-SEC-004, NFR-SEC-006)

- **CSRF:** `POST`/`PUT`/`PATCH`/`DELETE` must send an `Origin` (fallback `Referer`) equal to `MEDIA_UI_PUBLIC_URL` or an entry of `MEDIA_UI_ALLOWED_ORIGINS` (comma-separated); when neither is set, the request's own origin (Host, or `X-Forwarded-Host` from a trusted proxy) is used. A cookie-authenticated request with neither header is rejected. Non-browser clients that authenticate with `Authorization: Bearer <session>` (or `X-MuxCore-Session`) and send **no** `session` cookie are exempt. Rejections are `403 {"code":"csrf.rejected"}`. The `session` cookie is `HttpOnly`, `SameSite=Lax`, and `Secure` on https origins.
- **Headers:** every response sets `Content-Security-Policy` (`script-src 'self'`; TMDB/remote https images, YouTube trailer frames, same-origin reader iframes; override with `MEDIA_UI_CSP`), `X-Frame-Options: SAMEORIGIN` (the book/comic readers iframe `/stream/`), `X-Content-Type-Options: nosniff`, `Referrer-Policy: strict-origin-when-cross-origin`.
- **Timeouts:** `ReadHeaderTimeout` 10s, `ReadTimeout`/`WriteTimeout` 2m, `IdleTimeout` 2m. `/stream/*`, `/api/sessions/events` and `/api/debrid/stream` clear the read/write deadlines per request.
- **Identity to modules (ADR-0019):** proxied requests never carry the client's `Cookie`, `Authorization`, `X-Caller-Id`, `X-MuxCore-*`, `X-Tenant-ID`, `X-Auth-Claims-Tenant`, `X-User-ID` or `X-Auth-Token`. request-media gets `Authorization: Bearer <auth-local token>` and, for one release, `X-Caller-Id=<user id>`; userdata-local and the Jellyfin push get the bearer plus `X-MuxCore-User-Id`.

## Parental enforcement (ADR-0031)

The BFF is the single server-side parental enforcement point (FR-PLAY-007, ADR-0031). For each gated request it reads the signed-in principal's policy from userdata-local `GET {USERDATA_LOCAL_URL}/api/parental-policy` (ADR-0030) with exactly one `Authorization: Bearer <session auth-local token>` and `X-MuxCore-User-Id: <session user>`, no query string and no client-supplied identity or tenant. The response envelope is validated strictly: `state` is `configured` (revision > 0, policy decoded by userdata-local `parental.DecodePolicy`) or `unconfigured` (revision 0, `policy: null`); unknown or duplicate fields are rejected; `user_id` and `tenant_id` must equal the session's (an empty session tenant is the household scope and is never rebound to `TENANT_MODE`'s `"default"`). Validated documents are cached for at most 30 s per SHA-256(bearer) + user + tenant; errors are never cached and a provider `401` evicts the entry. Items are evaluated with userdata-local `parental.Evaluate`.

### Userdata provider transport

ADR-0033 (T-M4-01 S9b). The policy read above and the userdata blob proxy share one client from userdata-local's published `httpclient` package, built once at startup **after** mesh enrollment (`meshid.Ensure` exports `MUXCORE_TLS_CERT`/`KEY`/`CA`):

- `USERDATA_LOCAL_URL` must be a bare origin (`scheme://host[:port]`; trailing `/` is trimmed; no path, query, fragment or credentials; not an unspecified address). Household/staging (or no insecure flag) requires `https://`, the BFF's own enrolled certificate (CN = `MUXCORE_MODULE_ID`, default `media-ui`) and an explicit core CA; the provider must present CN **and** SAN `userdata-local` regardless of the dialed host (container name or loopback). System roots, `InsecureSkipVerify`, proxy environment variables and redirects (including same-origin) are never used. Explicit insecure dev (`MUXCORE_INSECURE_DISABLE_TLS=true`) uses `http://` only, with no module identity.
- A configuration error is logged at startup and leaves the BFF without a provider (parental policy → `503 parental.policy_unavailable`; userdata → local store). There is no plaintext retry.
- Status mapping is unchanged from ADR-0031: `200` validated document; provider `401` → `401 parental.session_invalid` and cache eviction; every other status, transport/TLS failure, refused redirect or module-admission denial (`403 {"code":"userdata.module_forbidden"}`, `httpclient.ErrUnavailable` reason `module_forbidden`) → `503 parental.policy_unavailable`, never cached, never a session revocation. An application `403 policy.forbidden` is also `503`.
- The provider's admission table lets `media-ui` GET the policy and GET/PUT `/api/userdata` only; the BFF can never write a parental policy.

Inputs that never influence a decision: query `tags`, `parental_rating`, `unrated`, `user_id`, `tenant_id`; headers `X-MuxCore-User-Id`, `X-Tenant-ID`, `X-Caller-Id`; the userdata blob `prefs.parental`; the local userdata store. There is no switch that disables enforcement. With `MEDIA_UI_REQUIRE_AUTH=0` (dev only) a request **without** a session is not gated and the BFF logs a startup warning; a request with a session is always gated. Without `USERDATA_LOCAL_URL` every gated request from a session returns `503 parental.policy_unavailable`.

| Condition | C-LIST / C-ITEM / C-PLAY / C-DENY | C-EXEMPT |
|---|---|---|
| Configured `unrestricted` | Existing behavior, without added classification lookups or narrowing filters | Unchanged |
| Configured `restricted` | C-LIST filters visible items; C-ITEM and C-PLAY deny disallowed items with **403 `parental.blocked`** (resolve retains `playback.parental_blocked` with `parental_code: "parental.blocked"`). C-DENY returns **403 `parental.restricted_route`** | Unchanged |
| `unconfigured` | **403 `parental.policy_unconfigured`** | Unchanged |
| Session without an auth-local bearer (Quick Connect, legacy) | **403 `parental.policy_unverifiable`** | Unchanged |
| No session while auth is required | **401 `parental.session_invalid`** | Existing authentication applies |
| Provider `401` | **401 `parental.session_invalid`** (cache entry evicted) | Unchanged |
| Provider `403`/`404`/`409`/`413`/`5xx`, redirect, timeout, connection error, bad or oversized JSON, envelope or scope mismatch, unknown state | **503 `parental.policy_unavailable`** — no local/blob fallback, never cached | Unchanged |
| Media classification lookup fails or returns an invalid item identity | **503 `parental.classification_unavailable`** | n/a |

Gate errors carry `Cache-Control: no-store` and only `{ "error", "code" }` (plus `parental_code` on resolve); never item metadata.

**Classification:** media-movies and media-tvshows v0.1.23 supply `content_rating`, `content_rating_source` and `tag_labels`, exposed on movie/series JSON. Only `operator` classification is accepted in this slice. Empty/unknown ratings and missing/unsupported sources remain unavailable even with `allow_unrated`; explicit `NR`/`UR` follows that setting. Tags are exact normalized labels and blocked tags win. Episodes inherit their series through `GetEpisode`; wrong or missing response identities fail closed. Per-item reads use fresh classifications, and detail/PATCH responses are checked before their fields are returned. Genuine gRPC `NotFound` is blocked; legacy movie/series providers return `Unknown` for missing items, which remains a generic lookup-failure 503 rather than relying on error-string matching.

**Lists and totals:** restricted movie/TV requests send the policy's narrowing filter to the owning module (kids mode defaults to PG unless an explicit ceiling is set), then re-evaluate every returned row. Under [ADR-0032](../docs/adr/0032-fail-closed-paginated-classification.md), an inconsistent already-paginated response fails with generic no-store 503 and returns neither rows nor total. Consistent responses retain the provider's visible total. Collection summaries count visible members and omit wholly hidden collections; a wholly hidden collection detail returns an empty movie list/zero total without its name or preferences. Companion movie libraries enumerate the narrowed catalogue completely before filtering/counting/local pagination, sort by item ID, and fail with the same 503 if pages are incomplete, duplicate IDs or changing totals prevent completeness, or the 100,000-item / 15-second bound is exceeded. Unrestricted catalogue behavior retains the prior queries.

**Playback cache:** trusted classifications are cached only for C-PLAY, keyed by kind and ID, for at most 30 seconds from lookup start. An episode mapping and its series classification share one expiry. Lookup errors and unavailable classifications are not cached, and every request evaluates its current policy against the classification. Bare `media_id` on `/api/playback/segments` does not establish an owning movie/episode and remains denied to restricted principals (intro/outro segments, S9). An opaque `/api/playback/subtitles/{id}` is served only through the session/item track binding under "Subtitle ownership". Subtitle path confinement for unrestricted callers is separate T-M3-10 work. This fixture-backed wiring does not establish authenticated provider transport, live rollout, profiles/PIN, public-image protection or native-client acceptance; FR-PLAY-007 remains partial.

**Subtitle ownership (restricted principals):** discovery and mapping of subtitles to the authorized item use only links the owning data proves. `GET /api/playback/subtitles?src=` pins the item to the gate's grant and lists a sidecar file only when the video provably owns it: the sidecar stem is the video stem plus language/flag tag tokens (`.en`, `.pt-BR`, `.forced`, `.sdh`), and no other video in the folder fits at least as specifically (so `Alien.mkv` does not list `Alien.Resurrection.en.srt`; two containers with one base name are ambiguous and grant nothing). A media-subtitles row is listed only when it names the file asked about. `GET /api/movies/{id}/subtitles` and `GET /api/tv/{id}/subtitles` drop `ListSubtitles` rows that name another file and `ListMedia(series_id)` rows that name another series, and never use the `GetMedia` fallbacks (media-subtitles `GetMedia` takes its own `media.id`, not a movie or series id; the restricted answer is an empty list). Unrestricted callers keep the lenient listing and the fallbacks. Every track returned to a restricted principal is bound to its BFF session and item (sliding 4 h; 512 per session, 16384 overall, least recently used evicted; a track already claimed by 8 items is not advertised). `GET /api/playback/subtitles/{id}` is served only for an id bound to the same session, with every bound item re-evaluated on each fetch; a missing, expired, evicted, other-session or post-restart binding is **403 `parental.blocked`**. `POST /api/subtitles/download` stays C-DENY. The fetch is authorized by the verified, provider-revalidated BFF session and the current policy and classification of each bound item on every request; the binding alone is never a credential (no session, a revoked or mismatched provider session, or an outage is 401/503 before any track is served). It is the C-PLAY binding ADR-0031 §1.2 already specifies, not a new decision, and is separate from T-M3-10 (unrestricted path confinement). Not covered: `GET /api/playback/segments` stays denied for restricted principals, live/provider-transport rollout and remaining S9 items. Residuals: a sidecar is proved owned when the track is listed and the binding then lasts up to the sliding 4 h, so a file replaced in that window is served under the old proof (ownership is not re-proved per fetch); `name` in `GET /api/collections` (list summary) and `GET /api/collections/{id}` (`GetCollectionMovies`) comes from media-movies as free text. The BFF neither classifies it nor checks that the two answers agree, so a restricted principal sees the module's name for any collection that still has a visible movie.

**HLS keys:** `media-transcoder` derives `/stream/hls/{key}/…` deterministically from the source, so a key proves nothing. When a restricted principal's authorized `GET /stream/hls` is proxied, the BFF binds the key from the transcoder's playlist redirect to that BFF session and item (sliding 4 h; at most 256 bindings per session and 8192 overall, least recently used evicted first, so one session cannot push other users out). `GET /stream/hls/{key}/{file}` from a restricted principal is served only for a key bound to the same session, and the bound item is re-evaluated on each request. Unrestricted principals are unchanged.

## Parental route classes

Every pattern registered by `registerRoutes` has a class in `cmd/mediauiprox/parental_routes.go` (`parentalRouteClasses`, keyed by the exact registered pattern). Registration panics on an unclassified pattern, and `routes_inventory_test.go` fails when a registered pattern has no class or when this table differs from the code. Routes not listed below are **C-EXEMPT**: no catalogue content in the response (session, auth, health, SPA, `/api/userdata`, playback telemetry, item mutations that return only status), operator routes that reject non-admin/manager sessions in the handler or through the [operator role gate](#operator-route-roles) (proved per route by `TestParentalOperatorExemptionsAreRoleGated`), and `/images/*` (public; out of scope per ADR-0031 §6). Some C-DENY and C-ITEM routes are also operator routes; for them the role gate runs first and the parental class still applies to admin/manager principals with a restricted policy (ADR-0031 §7).

| Route | Class |
|-------|-------|
| `ANY /stream/movies/` | C-PLAY |
| `ANY /stream/tv/` | C-PLAY |
| `GET /api/playback/analysis` | C-PLAY |
| `GET /api/playback/chapters` | C-PLAY |
| `GET /api/playback/resolve` | C-PLAY |
| `GET /api/playback/segments` | C-PLAY |
| `GET /api/playback/subtitles` | C-PLAY |
| `GET /api/playback/subtitles/{id}` | C-PLAY |
| `GET /stream/hls` | C-PLAY |
| `GET /stream/hls/{key}/{file}` | C-PLAY |
| `GET /stream/transcode` | C-PLAY |
| `GET /stream/trickplay` | C-PLAY |
| `ANY /api/discover/` | C-DENY |
| `ANY /api/jellyfin/play` | C-DENY |
| `ANY /api/media-issues` | C-DENY |
| `ANY /api/plex/play` | C-DENY |
| `ANY /api/request` | C-DENY |
| `ANY /api/request-policy` | C-DENY |
| `ANY /api/requests` | C-DENY |
| `ANY /api/requests/` | C-DENY |
| `ANY /api/search` | C-DENY |
| `ANY /api/watch-together` | C-DENY |
| `ANY /api/watch-together/` | C-DENY |
| `ANY /api/watchlist` | C-DENY |
| `GET /api/activity` | C-DENY |
| `GET /api/audiobooks` | C-DENY |
| `GET /api/audiobooks/` | C-DENY |
| `GET /api/audiobooks/{id}/artwork` | C-DENY |
| `GET /api/audiobooks/{id}/history` | C-DENY |
| `GET /api/blocklist` | C-DENY |
| `GET /api/books` | C-DENY |
| `GET /api/books/` | C-DENY |
| `GET /api/books/{id}/artwork` | C-DENY |
| `GET /api/books/{id}/history` | C-DENY |
| `GET /api/books/{id}/tags` | C-DENY |
| `GET /api/calendar` | C-DENY |
| `GET /api/comics` | C-DENY |
| `GET /api/comics/` | C-DENY |
| `GET /api/comics/{id}/artwork` | C-DENY |
| `GET /api/comics/{id}/history` | C-DENY |
| `GET /api/debrid/stream` | C-DENY |
| `GET /api/debrid/vfs` | C-DENY |
| `GET /api/graph/related` | C-DENY |
| `GET /api/history` | C-DENY |
| `GET /api/import/candidates` | C-DENY |
| `GET /api/jellyfin/link` | C-DENY |
| `GET /api/livetv` | C-DENY |
| `GET /api/missing` | C-DENY |
| `GET /api/music` | C-DENY |
| `GET /api/music/` | C-DENY |
| `GET /api/music/tracks/{id}/lyrics` | C-DENY |
| `GET /api/music/{id}/artwork` | C-DENY |
| `GET /api/music/{id}/files` | C-DENY |
| `GET /api/music/{id}/history` | C-DENY |
| `GET /api/music/{id}/tags` | C-DENY |
| `GET /api/playback/segments/media` | C-DENY |
| `GET /api/plex/sync-lists` | C-DENY |
| `GET /api/releases/search` | C-DENY |
| `GET /api/releases/upgrades` | C-DENY |
| `GET /api/rename/preview` | C-DENY |
| `GET /api/sessions` | C-DENY |
| `GET /api/sessions/events` | C-DENY |
| `GET /api/subtitles/search` | C-DENY |
| `GET /api/wanted` | C-DENY |
| `GET /api/watch-stats` | C-DENY |
| `GET /api/watch-stats/duplicates` | C-DENY |
| `GET /api/watch-stats/item` | C-DENY |
| `GET /api/watch-stats/stale` | C-DENY |
| `GET /stream/audiobooks/` | C-DENY |
| `GET /stream/books/` | C-DENY |
| `GET /stream/comics/` | C-DENY |
| `GET /stream/music/` | C-DENY |
| `POST /api/debrid/add` | C-DENY |
| `POST /api/livetv/timers` | C-DENY |
| `POST /api/releases/grab` | C-DENY |
| `POST /api/rename` | C-DENY |
| `POST /api/subtitles/download` | C-DENY |
| `POST /api/wanted` | C-DENY |
| `ANY /api/movies` | C-LIST |
| `ANY /api/tv` | C-LIST |
| `GET /api/collections` | C-LIST |
| `GET /api/collections/` | C-LIST |
| `GET /api/collections/{id}` | C-LIST |
| `ANY /api/movies/` | C-ITEM |
| `ANY /api/tv/` | C-ITEM |
| `GET /api/episodes/{id}/file` | C-ITEM |
| `GET /api/movies/{id}/artwork` | C-ITEM |
| `GET /api/movies/{id}/files` | C-ITEM |
| `GET /api/movies/{id}/history` | C-ITEM |
| `GET /api/movies/{id}/subtitles` | C-ITEM |
| `GET /api/movies/{id}/tags` | C-ITEM |
| `GET /api/movies/{id}/titles` | C-ITEM |
| `GET /api/tv/{id}/artwork` | C-ITEM |
| `GET /api/tv/{id}/history` | C-ITEM |
| `GET /api/tv/{id}/subtitles` | C-ITEM |
| `GET /api/tv/{id}/tags` | C-ITEM |
| `GET /api/tv/{id}/titles` | C-ITEM |
| `PATCH /api/movies/{id}` | C-ITEM |
| `PATCH /api/tv/{id}` | C-ITEM |

## Operator route roles

T-M5-12 (C-30): these state-changing routes had no role check, so any signed-in `user` or `viewer` could call them. Their row in `parentalRouteClasses` (`cmd/mediauiprox/parental_routes.go`) sets `requirePrivileged`, and `operator_routes.go` enforces it for every one before the parental gate of the route's class and before the handler. The check uses only the BFF session's roles (`sessionHasPrivilegedRole`: `admin` or `manager`, the same predicate as the 170 handler-gated operator routes); client headers, query parameters and body fields are never consulted for it. A session without the role gets **403** `{ "error", "code": "operator.forbidden" }`. No parental policy lookup and no module call happen before that, so the response says nothing about the item or the caller's policy. Without a session (auth disabled for local dev) the result is also 403, like the handler-gated operator routes.

Rows marked "admin for `root_folder_path`" also require `admin` (`sessionHasAdminRole`) when the JSON body names `root_folder_path`. Root-folder create/update is already admin-only (RULE-AUTH-3), and these PATCHes forward the path to the module as the item's new root. The gate reads at most 1 MiB of the body, decodes it with the same decoder as the handler (`decodeLibraryPatch`, so `Root_Folder_Path` and an empty value count), and restores it for the handler. A manager gets **403** `operator.admin_required`, or **413** `operator.body_too_large` for a larger body. Admins skip the read.

`TestOperatorRoutesMatchBFFAPIDoc` keeps this table equal to the code. `operator_routes_test.go` carries an independent list of the 43 routes and proves each one by request: `user`, `viewer`, no-role, `approver` and lookalike-role sessions get 403 with no module, HTTP or policy call; `manager` and `admin` reach the module; a manager is refused `root_folder_path` on all six root-path routes.

Role decisions:
- **Removal and file deletion** (including `?delete_files=1`): admin/manager. This matches the existing `DELETE /api/movies/{id}/files/{fileId}` and `POST /api/maintainer/act`, which also delete from disk.
- **Monitoring, quality profile and series overrides**: admin/manager. Managers manage libraries (FRD §1). Only `root_folder_path` needs admin.
- **Acquisition** (grab, search-now, block, wanted add/remove, import retry, blocklist clear, delay profiles, debrid add, TRaSH sync in both modes, rename, manual import, subtitle download): admin/manager. `user` may only request through `/api/request*`, which goes through approval (RULE-AUTH-4). `POST /api/wanted` and `POST /api/releases/grab` would bypass that approval. `POST /api/import` takes a server path; it gets the same role as watch folders and `POST /api/scan`.
- **`POST /api/sessions/{id}/stop`**: admin/manager, with no own-session exception. The BFF cannot prove ownership from trusted data. playback-monitor has no per-session read, and Jellyfin/Plex records carry media-server user ids that have no trusted mapping to MuxCore users. Native records take their `user_id` from the BFF session. Before T-M5-13 any member could pick another member's `session_id` in `POST /api/playback/session` and a `started` event then overwrote the record's `user_id`; the BFF now namespaces native session keys by the verified user (see the playback session notes above), but the route keeps its role gate because Jellyfin/Plex ownership is still unprovable.
- **`POST /api/livetv/timers`**: admin/manager (schedules recordings to household storage).
- Not gated, by design: `POST /api/formats/score` and `/parse` only compute. `GET /api/delay-profiles` and `GET /api/tv/{id}/override` are reads.

| Route | Role |
|-------|------|
| `DELETE /api/audiobooks/{id}` | admin/manager |
| `DELETE /api/books/works/{id}` | admin/manager |
| `DELETE /api/books/{id}` | admin/manager |
| `DELETE /api/comics/issues/{id}` | admin/manager |
| `DELETE /api/comics/{id}` | admin/manager |
| `DELETE /api/episodes/{id}/file` | admin/manager |
| `DELETE /api/movies/{id}` | admin/manager |
| `DELETE /api/movies/{id}/file` | admin/manager |
| `DELETE /api/music/{id}` | admin/manager |
| `DELETE /api/tv/{id}` | admin/manager |
| `DELETE /api/tv/{id}/override` | admin/manager |
| `PATCH /api/audiobooks/{id}` | admin/manager; admin for `root_folder_path` |
| `PATCH /api/books/works/{id}` | admin/manager |
| `PATCH /api/books/{id}` | admin/manager; admin for `root_folder_path` |
| `PATCH /api/comics/issues/{id}` | admin/manager |
| `PATCH /api/comics/{id}` | admin/manager; admin for `root_folder_path` |
| `PATCH /api/episodes/{id}` | admin/manager |
| `PATCH /api/movies/{id}` | admin/manager; admin for `root_folder_path` |
| `PATCH /api/music/albums/{id}` | admin/manager |
| `PATCH /api/music/{id}` | admin/manager; admin for `root_folder_path` |
| `PATCH /api/tv/seasons/{id}` | admin/manager |
| `PATCH /api/tv/{id}` | admin/manager; admin for `root_folder_path` |
| `POST /api/activity/retry` | admin/manager |
| `POST /api/blocklist/clear` | admin/manager |
| `POST /api/debrid/add` | admin/manager |
| `POST /api/delay-profiles` | admin/manager |
| `POST /api/formats/sync-trash` | admin/manager |
| `POST /api/import` | admin/manager |
| `POST /api/livetv/timers` | admin/manager |
| `POST /api/movies/{id}/refresh` | admin/manager |
| `POST /api/music/{id}/refresh` | admin/manager |
| `POST /api/releases/block` | admin/manager |
| `POST /api/releases/grab` | admin/manager |
| `POST /api/releases/search-now` | admin/manager |
| `POST /api/rename` | admin/manager |
| `POST /api/sessions/{id}/stop` | admin/manager |
| `POST /api/subtitles/download` | admin/manager |
| `POST /api/tv/{id}/override` | admin/manager |
| `POST /api/tv/{id}/refresh` | admin/manager |
| `POST /api/wanted` | admin/manager |
| `POST /api/wanted/remove` | admin/manager |
| `PUT /api/delay-profiles` | admin/manager |
| `PUT /api/tv/{id}/override` | admin/manager |

## Route inventory

Every route registered by `registerRoutes` in `cmd/mediauiprox/main.go` (plus `registerLibraryRoutes` and `registerRequestMediaRoutes`). `routes_inventory_test.go` fails when this table and the registered patterns differ. Format: `METHOD /pattern` (`ANY` = registered without a method, so every method reaches the handler; a trailing `/` matches the subtree). `{x}` are path parameters.

### Service, auth, static and streams

| Route | Purpose |
|-------|---------|
| `ANY /` | SPA static files and client-side routing fallback |
| `ANY /auth/callback` | OAuth callback from auth-local |
| `ANY /healthz` | Liveness probe (`{"status":"ok"}`) |
| `ANY /images/audiobooks/` | Reverse proxy to audiobook images |
| `ANY /images/books/` | Reverse proxy to book images |
| `ANY /images/comics/` | Reverse proxy to comic images |
| `ANY /images/movies/` | Reverse proxy to movie images (media-movies) |
| `ANY /images/music/` | Reverse proxy to music images |
| `ANY /images/tv/` | Reverse proxy to TV images (media-tvshows) |
| `ANY /login` | Start login (redirect to auth-local) |
| `GET /logout` | Sign-out confirm page (form that POSTs to `/logout`) |
| `POST /logout` | End session (CSRF-checked) |
| `GET /stream/audiobooks/` | Audiobook stream |
| `GET /stream/books/` | Book stream |
| `GET /stream/comics/` | Comic issue stream |
| `GET /stream/hls` | HLS playlist index |
| `GET /stream/hls/{key}/{file}` | HLS segment/asset |
| `ANY /stream/movies/` | Reverse proxy to media-movies file stream |
| `GET /stream/music/` | Music stream |
| `GET /stream/transcode` | Transcode stream |
| `GET /stream/trickplay` | Trickplay sprite |
| `ANY /stream/tv/` | Reverse proxy to media-tvshows file stream |

### Capabilities & discover

| Route | Purpose |
|-------|---------|
| `GET /api/capabilities` | Which optional modules/features are available to the SPA |
| `ANY /api/discover/` | Discover movies/TV/people/music/book browse and detail (subtree) |
| `GET /api/graph/related` | Related titles from media-graph |
| `ANY /api/search` | Proxy to request-media search |

### Movies & TV library

| Route | Purpose |
|-------|---------|
| `GET /api/calendar` | Upcoming releases calendar |
| `GET /api/collections` | List collections |
| `GET /api/collections/` | Collection detail (subtree) |
| `GET /api/collections/{id}` | Collection detail (subtree) |
| `PATCH /api/collections/{id}` | Set collection monitored |
| `PUT /api/collections/{id}` | Set collection monitored |
| `POST /api/collections/{id}/sync` | Sync collection |
| `PATCH /api/episodes/{id}` | Patch episode |
| `DELETE /api/episodes/{id}/file` | Delete episode file |
| `GET /api/episodes/{id}/file` | Get episode file |
| `GET /api/missing` | Missing/wanted library items |
| `ANY /api/movies` | List movies (paged); POST adds a movie |
| `ANY /api/movies/` | Movie detail subtree (`/api/movies/{id}`) |
| `DELETE /api/movies/{id}` | Delete movie |
| `PATCH /api/movies/{id}` | Patch movie |
| `GET /api/movies/{id}/artwork` | List movie artwork |
| `POST /api/movies/{id}/artwork` | Replace movie artwork |
| `DELETE /api/movies/{id}/file` | Delete movie file |
| `GET /api/movies/{id}/files` | List movie files |
| `DELETE /api/movies/{id}/files/{fileId}` | Delete movie file by ID |
| `GET /api/movies/{id}/history` | List movie history |
| `POST /api/movies/{id}/refresh` | Refresh movie |
| `GET /api/movies/{id}/subtitles` | List movie subtitles |
| `POST /api/movies/{id}/subtitles` | Upload movie subtitles |
| `GET /api/movies/{id}/tags` | Get movie tags |
| `POST /api/movies/{id}/tags` | Set movie tags |
| `PUT /api/movies/{id}/tags` | Set movie tags |
| `GET /api/movies/{id}/titles` | List movie titles |
| `POST /api/movies/{id}/titles` | Add movie title |
| `DELETE /api/movies/{id}/titles/{titleId}` | Delete movie title |
| `GET /api/tags` | List tags |
| `POST /api/tags` | Create tag |
| `DELETE /api/tags/{id}` | Delete tag |
| `ANY /api/tv` | List TV shows (paged); POST adds a show |
| `ANY /api/tv/` | TV show detail subtree (`/api/tv/{id}`, seasons, episodes) |
| `POST /api/tv/login` | TV login with username/password (device code flow, POST) |
| `ANY /api/tv/login/totp` | TV login second step: verify TOTP code (POST) |
| `PATCH /api/tv/seasons/{id}` | Patch TV season |
| `DELETE /api/tv/{id}` | Delete TV |
| `PATCH /api/tv/{id}` | Patch TV |
| `GET /api/tv/{id}/artwork` | List TV artwork |
| `POST /api/tv/{id}/artwork` | Replace TV artwork |
| `GET /api/tv/{id}/history` | List TV history |
| `DELETE /api/tv/{id}/override` | Delete series override |
| `GET /api/tv/{id}/override` | Get series override |
| `POST /api/tv/{id}/override` | Put series override |
| `PUT /api/tv/{id}/override` | Put series override |
| `POST /api/tv/{id}/refresh` | Refresh TV |
| `GET /api/tv/{id}/subtitles` | List TV subtitles |
| `POST /api/tv/{id}/subtitles` | Upload TV subtitles |
| `GET /api/tv/{id}/tags` | Get TV tags |
| `POST /api/tv/{id}/tags` | Set TV tags |
| `PUT /api/tv/{id}/tags` | Set TV tags |
| `GET /api/tv/{id}/titles` | List TV titles |
| `POST /api/tv/{id}/titles` | Add TV title |
| `DELETE /api/tv/{id}/titles/{titleId}` | Delete TV title |

### Library-plus (music, books, comics, audiobooks)

| Route | Purpose |
|-------|---------|
| `GET /api/audiobooks` | List audiobooks (media-audiobooks) |
| `POST /api/audiobooks` | Add audiobook |
| `GET /api/audiobooks/` | Audiobook by ID |
| `DELETE /api/audiobooks/{id}` | Delete audiobook |
| `PATCH /api/audiobooks/{id}` | Patch audiobook |
| `GET /api/audiobooks/{id}/artwork` | List audiobook artwork |
| `POST /api/audiobooks/{id}/artwork` | Replace audiobook artwork |
| `GET /api/audiobooks/{id}/history` | List audiobook history |
| `POST /api/audiobooks/{id}/import` | Import audiobook |
| `GET /api/books` | List book authors (media-books) |
| `POST /api/books` | Add book author |
| `GET /api/books/` | Book author by ID |
| `DELETE /api/books/works/{id}` | Delete book |
| `PATCH /api/books/works/{id}` | Patch book |
| `POST /api/books/works/{id}/import` | Import book |
| `DELETE /api/books/{id}` | Delete book author |
| `PATCH /api/books/{id}` | Patch book author |
| `GET /api/books/{id}/artwork` | List book artwork |
| `POST /api/books/{id}/artwork` | Replace book artwork |
| `POST /api/books/{id}/books` | Add book |
| `GET /api/books/{id}/history` | List book history |
| `GET /api/books/{id}/tags` | Get book tags |
| `POST /api/books/{id}/tags` | Set book tags |
| `PUT /api/books/{id}/tags` | Set book tags |
| `GET /api/comics` | List comic series (media-comics) |
| `POST /api/comics` | Add comic series |
| `GET /api/comics/` | Comic series by ID |
| `DELETE /api/comics/issues/{id}` | Delete comic issue |
| `PATCH /api/comics/issues/{id}` | Patch comic issue |
| `POST /api/comics/issues/{id}/import` | Import comic issue |
| `DELETE /api/comics/{id}` | Delete comic series |
| `PATCH /api/comics/{id}` | Patch comic series |
| `GET /api/comics/{id}/artwork` | List comic artwork |
| `POST /api/comics/{id}/artwork` | Replace comic artwork |
| `GET /api/comics/{id}/history` | List comic history |
| `POST /api/comics/{id}/issues` | Add comic issue |
| `GET /api/music` | List music artists (media-music) |
| `POST /api/music` | Add music artist |
| `GET /api/music/` | Music artist by ID |
| `PATCH /api/music/albums/{id}` | Patch music album |
| `POST /api/music/albums/{id}/import` | Import music album |
| `GET /api/music/tracks/{id}/lyrics` | Track lyrics |
| `DELETE /api/music/{id}` | Delete music artist |
| `PATCH /api/music/{id}` | Patch music artist |
| `POST /api/music/{id}/albums` | Add music album |
| `GET /api/music/{id}/artwork` | List music artwork |
| `POST /api/music/{id}/artwork` | Replace music artwork |
| `GET /api/music/{id}/files` | List music track files |
| `DELETE /api/music/{id}/files/{fileId}` | Delete music track file |
| `GET /api/music/{id}/history` | List music history |
| `POST /api/music/{id}/refresh` | Refresh music artist |
| `GET /api/music/{id}/tags` | Get music tags |
| `POST /api/music/{id}/tags` | Set music tags |
| `PUT /api/music/{id}/tags` | Set music tags |

### Roots, scan, rename & import

| Route | Purpose |
|-------|---------|
| `POST /api/import` | Import path |
| `GET /api/import/candidates` | Import candidates |
| `POST /api/rename` | Rename execute |
| `POST /api/rename/organize` | Organize library |
| `GET /api/rename/preview` | Rename preview |
| `GET /api/rename/templates` | List rename templates |
| `POST /api/rename/templates` | Create rename template |
| `DELETE /api/rename/templates/{id}` | Delete rename template |
| `PATCH /api/rename/templates/{id}` | Patch rename template |
| `GET /api/roots` | List roots |
| `POST /api/roots` | Create root |
| `GET /api/roots/browse` | Browse roots |
| `GET /api/roots/pick` | Pick root |
| `POST /api/roots/probe` | Probe root |
| `DELETE /api/roots/{id}` | Delete root |
| `PATCH /api/roots/{id}` | Patch root |
| `GET /api/scan` | Library scan status |
| `POST /api/scan` | Library scan |
| `GET /api/scan/watch-dirs` | List watch dirs |
| `POST /api/scan/watch-dirs` | Create watch dir |
| `DELETE /api/scan/watch-dirs/{id}` | Delete watch dir |
| `PATCH /api/scan/watch-dirs/{id}` | Update watch dir |
| `PUT /api/scan/watch-dirs/{id}` | Update watch dir |

### Quality, formats & releases

| Route | Purpose |
|-------|---------|
| `GET /api/blocklist` | List blocklist |
| `POST /api/blocklist/clear` | Clear blocklist |
| `GET /api/delay-profiles` | List delay profiles |
| `POST /api/delay-profiles` | Upsert delay profile |
| `PUT /api/delay-profiles` | Upsert delay profile |
| `GET /api/formats` | List custom formats and quality profiles |
| `POST /api/formats` | Create custom format |
| `POST /api/formats/parse` | Parse a release title into format attributes |
| `POST /api/formats/profiles` | Create quality profile |
| `DELETE /api/formats/profiles/{id}` | Delete quality profile |
| `PATCH /api/formats/profiles/{id}` | Patch quality profile |
| `GET /api/formats/release-profiles` | List release profiles |
| `POST /api/formats/release-profiles` | Upsert release profile |
| `DELETE /api/formats/release-profiles/{id}` | Delete release profile |
| `PATCH /api/formats/release-profiles/{id}` | Upsert release profile |
| `PUT /api/formats/release-profiles/{id}` | Upsert release profile |
| `POST /api/formats/score` | Score a release against profiles |
| `POST /api/formats/sync-trash` | Sync TRaSH Guides packs |
| `DELETE /api/formats/{id}` | Delete custom format |
| `PATCH /api/formats/{id}` | Patch custom format |
| `POST /api/releases/block` | Block a release (adds to blocklist) |
| `POST /api/releases/grab` | Grab a release from interactive search |
| `GET /api/releases/search` | Interactive release search |
| `POST /api/releases/search-now` | Search now |
| `GET /api/releases/upgrades` | Items below their quality cutoff (upgrade candidates) |

### Acquisition, indexers & activity

| Route | Purpose |
|-------|---------|
| `GET /api/acquisition` | Acquisition stack health/status |
| `GET /api/activity` | Download and import activity queue |
| `POST /api/activity/retry` | Retry a failed activity item |
| `POST /api/debrid/add` | Add a magnet/link to the debrid service |
| `GET /api/debrid/stream` | Stream a debrid file |
| `GET /api/debrid/vfs` | Browse the debrid virtual filesystem |
| `GET /api/indexers` | List indexers |
| `POST /api/indexers` | Create indexer |
| `DELETE /api/indexers/{id}` | Delete indexer |
| `PATCH /api/indexers/{id}` | Update indexer |
| `GET /api/wanted` | List wanted (monitored, missing) items |
| `POST /api/wanted` | Add a wanted item |
| `POST /api/wanted/remove` | Remove a wanted item |

### Requests, lists & watchlist

| Route | Purpose |
|-------|---------|
| `GET /api/lists` | List sources |
| `POST /api/lists` | Create list source |
| `GET /api/lists/history` | List sync history |
| `GET /api/lists/items` | List sync items |
| `POST /api/lists/sync` | Sync list sources |
| `DELETE /api/lists/{id}` | Delete list source |
| `PATCH /api/lists/{id}` | Update list source |
| `PUT /api/lists/{id}` | Update list source |
| `POST /api/lists/{id}/sync` | Sync list source |
| `POST /api/lists/{id}/test` | Test list source |
| `ANY /api/media-issues` | Media issue reports (GET list, POST create, PATCH resolve) |
| `POST /api/migrate` | Arr migration import (Radarr/Sonarr/Lidarr) |
| `ANY /api/request` | Proxy to request-media create request |
| `ANY /api/request-policy` | Proxy to request-media request policy (quotas) |
| `ANY /api/requests` | Proxy to request-media request list |
| `ANY /api/requests/` | Proxy to request-media request detail/actions |
| `ANY /api/watchlist` | Per-user watchlist (GET|POST|DELETE) |

### Playback, userdata & live TV

| Route | Purpose |
|-------|---------|
| `DELETE /api/jellyfin/link` | Unlink the household Jellyfin account |
| `GET /api/jellyfin/link` | Jellyfin link status |
| `POST /api/jellyfin/match` | Match a Mux item to a Jellyfin item |
| `ANY /api/jellyfin/play` | Jellyfin play deep-link resolve |
| `POST /api/jellyfin/refresh` | Trigger Jellyfin library refresh |
| `GET /api/jellyfin/status` | Jellyfin status |
| `POST /api/jellyfin/sync` | Sync watched state with Jellyfin |
| `GET /api/livetv` | Live TV |
| `POST /api/livetv/timers` | Create live TV recording timer |
| `GET /api/playback/analysis` | Media analysis (streams, codecs) for playback |
| `GET /api/playback/chapters` | Chapter markers (ffprobe) |
| `GET /api/playback/resolve` | Playback resolve |
| `DELETE /api/playback/segments` | Delete playback segments |
| `GET /api/playback/segments` | Intro/outro/credits skip segments |
| `PUT /api/playback/segments` | Put playback segments |
| `GET /api/playback/segments/media` | Media with detected segments (admin list) |
| `POST /api/playback/session` | Report native-player playback session to playback-monitor |
| `GET /api/playback/subtitles` | List subtitles for a playback item |
| `GET /api/playback/subtitles/{id}` | Serve a subtitle file |
| `ANY /api/plex/play` | Plex play deep-link resolve |
| `GET /api/plex/sync-lists` | Plex watchlist/list sync sources |
| `ANY /api/userdata` | Session userdata: GET reads, PUT/POST writes progress/favorites/prefs |
| `ANY /api/watch-together` | Create a Watch Together room (POST) |
| `ANY /api/watch-together/` | Watch Together room state/actions (subtree) |

### Users, auth & household admin

| Route | Purpose |
|-------|---------|
| `GET /api/backups` | List backups |
| `POST /api/backups` | Create backup |
| `DELETE /api/backups/{id}` | Delete backup |
| `POST /api/backups/{id}/restore` | Restore backup |
| `GET /api/guard` | Playback-guard status |
| `GET /api/guard/rules` | List playback-guard rules |
| `POST /api/guard/rules` | Upsert guard rule |
| `PUT /api/guard/rules` | Upsert guard rule |
| `DELETE /api/guard/rules/{id}` | Delete guard rule |
| `POST /api/guard/trust/reset` | Reset guard trust |
| `POST /api/guard/users/merge` | Merge guard users |
| `POST /api/guard/violations/ack` | Ack guard violations |
| `GET /api/history` | Household playback history |
| `GET /api/invite/peek` | Invite peek |
| `POST /api/invite/redeem` | Invite redeem |
| `GET /api/invites` | List invites |
| `POST /api/invites` | Create invite |
| `DELETE /api/invites/{id}` | Revoke invite |
| `GET /api/keys` | List API keys |
| `POST /api/keys` | Create API key |
| `DELETE /api/keys/{id}` | Delete API key |
| `POST /api/keys/{id}/rotate` | Rotate API key |
| `GET /api/maintainer` | Library maintainer state (rules, candidates, exclusions) |
| `POST /api/maintainer/act` | Act on maintainer candidates in bulk |
| `POST /api/maintainer/candidates/{id}/{action}` | Maintainer candidate action |
| `POST /api/maintainer/collections` | Upsert maintainer collection |
| `DELETE /api/maintainer/collections/{id}` | Delete maintainer collection |
| `POST /api/maintainer/exclusions` | Upsert maintainer exclusion |
| `POST /api/maintainer/exclusions/sync` | Sync maintainer exclusions |
| `DELETE /api/maintainer/exclusions/{id}` | Delete maintainer exclusion |
| `POST /api/maintainer/protections` | Upsert maintainer protection |
| `DELETE /api/maintainer/protections/{id}` | Delete maintainer protection |
| `POST /api/maintainer/rules` | Upsert maintainer rule |
| `GET /api/maintainer/rules/export` | Export maintainer rules |
| `POST /api/maintainer/rules/import` | Import maintainer rules |
| `POST /api/maintainer/rules/preview` | Preview maintainer rule |
| `DELETE /api/maintainer/rules/{id}` | Delete maintainer rule |
| `POST /api/maintainer/rules/{id}/toggle` | Toggle maintainer rule |
| `POST /api/maintainer/scan` | Run a maintainer scan |
| `GET /api/me` | Current session user |
| `GET /api/mobile/auth/done` | Mobile auth done |
| `GET /api/mobile/auth/login` | Mobile auth login |
| `POST /api/mobile/session` | Mobile session |
| `GET /api/notifications` | List notifications |
| `POST /api/notifications` | Configure notification channel |
| `PUT /api/notifications` | Configure notification channel |
| `POST /api/notifications/test` | Test notification |
| `GET /api/passkeys` | List passkeys |
| `POST /api/passkeys/register/begin` | Begin passkey register |
| `POST /api/passkeys/register/complete` | Complete passkey register |
| `DELETE /api/passkeys/{id}` | Delete passkey |
| `ANY /api/password-reset` | Password reset requests (POST request, GET list) |
| `POST /api/password-reset/{id}/dismiss` | Dismiss password reset |
| `POST /api/password-reset/{id}/password` | Set password reset |
| `ANY /api/quickconnect` | Quick Connect code flow (GET|POST) |
| `GET /api/session` | Current session user (alias of `/api/me`) |
| `GET /api/sessions` | Active playback sessions |
| `GET /api/sessions/events` | Playback session event stream |
| `POST /api/sessions/{id}/stop` | Stop playback session |
| `GET /api/subtitles/blacklist` | List subtitle blacklist |
| `DELETE /api/subtitles/blacklist/{id}` | Remove subtitle blacklist |
| `POST /api/subtitles/download` | Download a subtitle for an item |
| `DELETE /api/subtitles/files/{id}` | Delete item subtitle |
| `GET /api/subtitles/history` | List subtitle history |
| `POST /api/subtitles/history/clear` | Clear subtitle history |
| `GET /api/subtitles/languages` | List subtitle languages |
| `GET /api/subtitles/media` | List subtitle media |
| `POST /api/subtitles/media/mass-edit` | Mass edit subtitle media |
| `PATCH /api/subtitles/media/{id}` | Patch subtitle media |
| `GET /api/subtitles/profiles` | List subtitle profiles |
| `POST /api/subtitles/profiles` | Upsert subtitle profile |
| `PUT /api/subtitles/profiles` | Upsert subtitle profile |
| `GET /api/subtitles/providers` | List subtitle providers |
| `POST /api/subtitles/providers/{id}` | Set subtitle provider |
| `PUT /api/subtitles/providers/{id}` | Set subtitle provider |
| `GET /api/subtitles/search` | Search subtitle providers |
| `GET /api/subtitles/wanted` | List subtitle wanted |
| `POST /api/subtitles/wanted` | Create subtitle wanted |
| `POST /api/subtitles/wanted/search` | Search subtitle wanted |
| `DELETE /api/subtitles/wanted/{id}` | Delete subtitle wanted |
| `GET /api/tagging` | Tagging rules and tags state |
| `POST /api/tagging/classify` | Classify items with tagging rules |
| `POST /api/tagging/rules` | Upsert tagging rule |
| `PUT /api/tagging/rules` | Upsert tagging rule |
| `DELETE /api/tagging/rules/{id}` | Delete tagging rule |
| `POST /api/tagging/tags` | Create tagging tag |
| `DELETE /api/tagging/tags/{id}` | Delete tagging tag |
| `DELETE /api/totp` | Disable TOTP |
| `GET /api/totp` | Get TOTP |
| `POST /api/totp` | Enable TOTP |
| `POST /api/totp/verify` | Verify TOTP |
| `GET /api/users` | List users |
| `POST /api/users` | Create user |
| `DELETE /api/users/{id}` | Delete user |
| `PATCH /api/users/{id}` | Patch user |
| `POST /api/users/{id}/password` | Set user password |
| `PUT /api/users/{id}/password` | Set user password |
| `GET /api/watch-notify` | Watch-notify destinations and rules |
| `POST /api/watch-notify/destinations` | Upsert watch notify destination |
| `PUT /api/watch-notify/destinations` | Upsert watch notify destination |
| `DELETE /api/watch-notify/destinations/{id}` | Delete watch notify destination |
| `POST /api/watch-notify/destinations/{id}/test` | Test watch notify destination |
| `POST /api/watch-notify/rules` | Upsert watch notify rule |
| `PUT /api/watch-notify/rules` | Upsert watch notify rule |
| `DELETE /api/watch-notify/rules/{id}` | Delete watch notify rule |
| `GET /api/watch-stats` | Watch statistics summary |
| `GET /api/watch-stats/charts` | Watch statistics chart series |
| `GET /api/watch-stats/duplicates` | Duplicate library items |
| `POST /api/watch-stats/import-jellystat` | Import history from Jellystat |
| `POST /api/watch-stats/import-tautulli` | Import history from Tautulli |
| `GET /api/watch-stats/item` | Watch statistics for one item |
| `GET /api/watch-stats/stale` | Stale (unwatched) items |
| `GET /api/watch-stats/storage` | Library storage usage |
| `GET /api/watch-stats/storage-history` | Library storage usage over time |

## Operator authorization and transcoder boundary

List-source configuration, sync, testing, history and items; indexer create/update/delete; Arr migration; and root create/update require an `admin` session. A `manager` session receives HTTP 403. Other library permissions are unchanged.

Transcode, HLS-index and trickplay `src` accept only relative `/stream/movies/<id>` or `/stream/tv/<id>` routes (including TV season/episode segments). Absolute URLs, local paths, query strings, fragments, traversal and encoded separators are rejected with HTTP 400, including when playback falls back to a direct stream. HLS assets use the same authenticated upstream transport.

In household, `TRANSCODER_HTTP_URL` uses HTTPS and the BFF presents its enrolled `media-ui` mesh certificate. Dev containers use `TRANSCODER_HTTP_TOKEN`; browser cookies, bearer tokens and caller identity headers are stripped. An explicitly empty `TRANSCODER_HTTP_URL` disables the optional transcoder. Arr migration uses the shared netguard Integration client: LAN/loopback servers are allowed, metadata/link-local targets are rejected at dial time, and redirects are refused to protect the Arr API key.
