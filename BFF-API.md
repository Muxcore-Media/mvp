# mediauiprox BFF JSON contracts

Consumer SPA (`media-ui-app`) talks to `mvp/cmd/mediauiprox` on `:5173`.

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

Appends a timer to the same JSON file.

## Errors

Backend/gRPC failures return JSON (not silent empty lists):

```json
{ "error": "human message", "code": "movies.list_failed" }
```

Typical HTTP status: `400` / `401` / `404` / `502` / `503` from gRPC code mapping.

## Jellyfin play deep-link

### `GET /api/jellyfin/play?mux_id=`

Resolves a MuxCore library id through jellyfin item links → `{ "url": "https://…/web/index.html#!/details?id=…" }`.

`404` when unlinked or Jellyfin base URL unset; `503` when the jellyfin bridge is unreachable.

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

`ready` is true only when at least one indexer **and** one downloader are live. HTTP peers answer `< 500` on `/healthz` or `/`. Torznab/Prowlarr counts when `ListIndexers` returns a configured child. Defaults: `INDEXER_PIRATEBAY_HTTP_URL`, `INDEXER_TORZNAB_GRPC_CLIENT_ADDR` (`:9486`), `DOWNLOADER_TORRENT_HTTP_URL` (`:9464`), `DOWNLOADER_QBIT_HTTP_URL`, `DOWNLOADER_SAB_HTTP_URL`, `DOWNLOADER_USENET_HTTP_URL`, `DEBRID_HTTP_URL`.

### `GET /api/indexers`

Household Prowlarr/Jackett catalog from `indexer-torznab` `ListIndexers`. `{ available, indexers: [{ id, name, protocol, language, configured }] }`. Module unset/down → `{ available: false, indexers: [] }`. Not a feature key — Settings → Acquisition shows the catalog. There is no household add/delete: indexers are created in Prowlarr/Jackett.

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

Imports Recyclarr-compatible TRaSH packs (`SyncTrashGuides`). Body: `{ scoreSet, importProfiles, services, official }`. Default uses the bundled household fixture (or the module's `FORMATS_TRASH_GUIDES_PATH`). `{ official: true }` downloads the hardcoded TRaSH-Guides GitHub archive — admin/manager only. Client `guidesPath` is ignored except the `official` sentinel; arbitrary URLs/paths are not accepted.

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

Body `{ "monitored": true|false, "quality_profile_id": "qp_uhd", "root_folder_path": "/data/movies" }`. At least one field is required on movie/series PATCH. Forwards to media-movies `UpdateMovie` or media-tvshows `UpdateTVShow` / `UpdateSeasonMonitored` / `UpdateEpisodeMonitored`. Season monitor updates cascade to that season's episodes. After a successful movie/series/episode PATCH the BFF also calls automation `UpdateQueueItem` so Search now / grab scoring uses the new profile immediately (`queue_id` matches wanted id, library item id, or TV series id). Automation down does not fail the library PATCH.

### `GET /api/roots`

Household library roots from `media-root-folders` (`?kind=movies|tv|…`). `{ available, roots: [{ id, path, name, media_kind, accessible, free_bytes, total_bytes, is_default }] }`. Module unset/down → `{ available: false, roots: [] }`. List is available to any signed-in household session (movie/TV root picker).

### `GET /api/roots/browse` · `POST /api/roots` · `DELETE /api/roots/{id}`

Arr-style library roots for admin/manager. Browse (`?path=`) returns `{ available, path, parent, entries: [{ name, path, is_dir }] }` (module prefixes still apply). POST body `{ path, name, media_kind, is_default }` creates a root. DELETE removes the catalog row (not files on disk). Module unset/down → browse `{ available: false, entries: [] }`; create/delete HTTP 503. Not a feature key — Settings hides Libraries when the session is not admin/manager.

### `GET|POST /api/scan`

Household library scan (admin-ui `/library-scan`). Admin/manager only. GET `{ available, status, last_scan_at, watch_dirs, total_imported, last_found, last_imported, last_skipped, last_error }` — scanner unset/down → `{ available: false, … }`. POST `{ type: watch|library_roots, path?, media_type? }` runs `Scan` or `ScanLibraryRoots` and returns `{ type, files_found, files_imported, files_skipped, message }`. Not a feature key — Settings → Libraries hides scan actions when the scanner is unavailable.

### `GET|POST /api/scan/watch-dirs` · `PATCH|PUT /api/scan/watch-dirs/{id}` · `DELETE /api/scan/watch-dirs/{id}`

Household Arr-style watch/download folders. Admin/manager only. GET `{ available, dirs: [{ id, path, media_type, library_path, tv_library_path, music_library_path, enabled, created_at }] }`. POST `{ path, media_type, library_path }` calls `AddWatchDir`. PATCH/PUT `{ enabled?, path?, media_type?, library_path?, tv_library_path?, music_library_path? }` pauses (`SetWatchDirEnabled`) or retargets (`UpdateWatchDir`) without delete-and-re-add. DELETE removes the watch. Scanner unset/down → GET `{ available: false, dirs: [] }`. Same Libraries privilege gate.

### `GET /api/rename/preview` · `POST /api/rename`

Radarr/Sonarr Preview Rename. `GET ?movie_id=` or `?tv_id=` (`&episode_id=` optional) returns `{ available, items: [{ file_id, episode_id, title, current_path, new_path, new_filename, changed, quality }] }`. `POST` body `{ movie_id }` or `{ tv_id, episode_id? }` applies changed rows (`media-rename` Execute) and updates `movie_files` / `episode_files`. Module unset/down → GET `{ available: false, items: [] }`. Not a feature key — the SPA hides the card when unavailable.

### `POST /api/rename/organize`

Household Organize (admin-ui `/rename/organize` → `BatchRename`). Admin/manager only. Body `{ directory, media_type, dry_run, import_mode }`. `directory` must be a configured library root or watch-dir path (or a child of one). Relative paths, `/`, and anything outside those prefixes return `organize.path_forbidden`. No roots and no watch folders → `organize.root_required`. Default `dry_run` is true. Response `{ available, directory, media_type, dry_run, total, renamed, errors, items: [{ original, renamed_to, success, error }] }`. Not a feature key — Settings Naming is already admin/manager.

### `GET|POST /api/rename/templates` · `PATCH|DELETE /api/rename/templates/{id}`

Household naming-template CRUD (admin-ui `/rename/templates`). Admin/manager only. GET `?media_type=` optional returns `{ available, templates: [{ id, name, media_type, pattern, is_default, updated_at }] }`. POST `{ name, media_type, pattern, is_default }` creates. PATCH `{ name, pattern, is_default }` updates. DELETE removes (module refuses the last template for a media type). Module unset/down → GET `{ available: false, templates: [] }`; writes HTTP 503. Not a feature key — Settings hides Naming when the session is not admin/manager.

## Series overrides

### `GET /api/tv/{id}/override` · `PUT /api/tv/{id}/override` · `DELETE /api/tv/{id}/override`

Per-show grab delay and preferred/ignored release groups (`ListSeriesOverrides` / `UpsertSeriesOverride` / `DeleteSeriesOverride`). PUT body `{ delay_minutes, preferred_groups, ignored_groups }`. `{ available, found, override }`. Automation unset/down → GET `{ available: false, found: false }`.

## Remove / refresh

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

Unblock one release `{ wanted_item_id, guid? }` or `{ clear_all: true }`. Forwards to `ClearBlocklist`. Interactive Search still uses `POST /api/releases/block` to add entries.

## Delay profiles

### `GET /api/delay-profiles`

Lists automation per-protocol grab delays (`ListDelayProfiles`). `{ available, profiles: [{ protocol, wait_minutes }] }`. Seeded defaults are torrent=15, usenet=0. Automation unset/down → HTTP 200 `{ available: false, profiles: [] }`.

### `PUT /api/delay-profiles` · `POST /api/delay-profiles`

Upsert one protocol delay. Body `{ protocol, wait_minutes }` (`wait_minutes` 0–10080). Forwards to `UpsertDelayProfile`. The grab loop waits this long after first seeing a release before dispatching.

## Manual import

### `GET /api/import/candidates`

Lists unimported files in scanner watch/download folders (`media-scanner` `ListImportCandidates`). `{ items, total, available }`. Rows include `path`, `title`, `media_type`, `year`, `size`. When the scanner is unset or down: `{ available: false, items: [] }`.

### `POST /api/import`

Imports one candidate via `ImportPath`. Body: `{ path, title?, media_type?, year?, tmdb_id?, season_number?, episode_number? }`. `path` is required. Response: `{ imported, skipped, found, message }`.

## Saved offline (browser cache)

Household browsers save a title via Cache API (`media-ui-app` `/offline`). Playback prefers the cached blob when `src` matches. This is device-local — not a server download queue (`/activity`).

## Request policy (Seerr-style quotas)

Proxied to request-media `GET|PUT /api/request-policy`. The BFF forwards `X-Caller-Id` (session user id) and `X-MuxCore-User`. `PUT` requires an approve role. HTTP 429 `{ "code": "request.quota" }` when a household member exceeds pending or weekly limits.

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

Household Arr tags (movies + TV catalogs). GET `?media=movie|tv|all` returns `{ available, tags: [{ id, label, media, created_at }] }` to any signed-in session. POST `{ label, media: movie|tv }` and DELETE `?media=` are admin/manager. Module unset/down → GET `{ available: false, tags: [] }`. Not a feature key — Settings hides Tags when the session is not admin/manager.

### `GET|PUT /api/movies/{id}/tags` · `GET|PUT /api/tv/{id}/tags`

Per-title tag assignment. GET `{ available, tags }`. PUT `{ tag_ids: [] }` replaces the item’s tags (admin/manager).

### `GET|POST /api/movies/{id}/titles` · `DELETE /api/movies/{id}/titles/{titleId}` · `GET|POST /api/tv/{id}/titles` · `DELETE /api/tv/{id}/titles/{titleId}`

Household Arr alternate titles used for grab/import matching. GET `{ available, titles: [{ id, title, clean_title, source, user }] }` — any signed-in session; module unset/down → `{ available: false, titles: [] }`. POST `{ title }` adds a user title (admin/manager). DELETE removes a user-sourced title only (the module refuses primary/TMDB rows). Not a feature key — movie/TV detail shows the card when the catalog is available.

### `GET /api/movies/{id}/history` · `GET /api/tv/{id}/history`

Household Arr title history (grab / import / delete file / delete item) from movies and TV `ListHistory`. Any signed-in session. Optional `?event=grab|import|delete_file|delete_item`. Response `{ available, items: [{ id, event_type, item_id, title, source_title, quality, indexer, file_path, download_id, created_at }], total }`. Module unset/down → `{ available: false, items: [] }`. Not a feature key — movie/TV detail hides the card when unavailable.

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

- `POST /api/debrid/add` — enqueue magnet/hoster
- `GET /api/debrid/vfs` — virtual file listing
- `GET /api/debrid/stream` — proxied playback URL

## Playback helpers (native player)

Session required unless noted.

| Route | Purpose |
|-------|---------|
| `GET /api/playback/subtitles?src=` | List subtitle tracks (`media-subtitles`) |
| `GET /api/playback/subtitles/{id}?src=` | Serve subtitle bytes |
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

`GET /api/sessions` returns `{ items, total, available }` with snake/camel-normalized rows (`title`, `user`, `href`, `paused`, `transcode`, `serverType`, progress). Listing uses the same operator token as ingest (`PLAYBACK_MONITOR_HTTP_TOKEN`). When the monitor is unset or down the route still returns **HTTP 200** `{ "available": false, "items": [] }`. `POST /api/sessions/{id}/stop` returns `{ stopped, id, serverType, jellyfinStopped }`.

`GET /api/capabilities` includes `features.playbackMonitor` when monitor `/healthz` is live.

All `/api/playback/*` routes return JSON errors `{ "error", "code" }` on upstream failure (no silent empty success for mutating paths). Invalid session payloads are `400`; monitor outages are `202`, not `5xx`.

## Auth session

- `GET /login` — redirect to `AUTH_HTTP_URL/login?redirect=…`
- `GET /auth/callback?code=` — exchange OAuth-style code for `session` cookie
- `GET /logout` — clear session
- `GET /api/session` · `GET /api/me` — current household identity (`user_id`, `username`, `roles`, optional `tenant_id`). Session required. Also sets `X-MuxCore-User-Id`. Used by media-ui `refreshCurrentUserId` for PIN salt.

Set `MEDIA_UI_PUBLIC_URL` and `MEDIA_UI_TRUSTED_PROXIES` (dawn/dusk `/128` CIDRs) when behind edge nginx so Secure cookies and callback origins match `https://mux.zem.systems`.

