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

When transcoding is enabled and direct play is not preferred, `mode` is `transcode` and `stream_url` is `/stream/transcode?src=…` (BFF proxies the underlying module stream).

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

## Userdata (session)

### `GET|POST|DELETE /api/userdata`

Progress, favorites, watched state, and preferences stored under `MEDIA_UI_USERDATA_DIR` / userdata-local when configured. JSON bodies mirror admin playback policy fields where shared.

GET/PUT responses include `user_id` (household id used as the parental PIN salt: `SHA-256(userID+":"+pin)`) and echo `X-MuxCore-User-Id` with the same value. Scope is the session principal; admin/manager may override via `?user_id=` or `X-MuxCore-User-Id` (same header as admin-ui userdata sync).

## Watchlist & collections

### `GET|POST|DELETE /api/watchlist`

Requires `media-list-sync` when adding external list sources; local watchlist rows stored in userdata.

### `GET /api/collections` · `GET /api/collections/{id}`

Curated rows from module catalogs + userdata; empty list is `{ "items": [] }`.

## Invites & onboarding

### `GET /api/invite/peek?token=`

Public. Returns invite metadata before redemption.

### `POST /api/invite/redeem`

Public. Body: `{ "token", "username", "password" }` — creates auth-local user via auth HTTP.

## Quick Connect & TV login

### `GET|POST /api/quickconnect`

Jellyfin-style quick connect code flow (file-backed store under userdata dir).

### `GET|POST /api/tv/login` · `POST /api/tv/login/totp`

Device/TV pairing: returns short code or completes TOTP step; redirects into session cookie on success.

## Mobile auth handoff

### `GET /api/mobile/auth/login` · `GET /api/mobile/auth/done` · `POST /api/mobile/session`

Deep-link friendly mobile login: poll `done` after browser auth, exchange for bearer/session.

## Password reset (consumer)

### `POST /api/password-reset`

Public. Queues reset request JSON (`MEDIA_UI_PASSWORD_RESET_FILE`, shared with admin-ui queue).

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
| `GET /api/playback/segments?src=` | Intro/outro/credits skip segments (`media-intro-outro`) |
| `GET /api/playback/chapters?src=` | Chapter markers (`media-ffprobe`) |
| `GET /api/playback/analysis?src=` | Container/codec summary for OSD |
| `GET /stream/transcode?src=` | On-the-fly transcode proxy (`media-transcoder`) |
| `GET /stream/trickplay?src=` | Trickplay sprite sheet |

All `/api/playback/*` routes return JSON errors `{ "error", "code" }` on upstream failure (no silent empty success for mutating paths).

## Auth session

- `GET /login` — redirect to `AUTH_HTTP_URL/login?redirect=…`
- `GET /auth/callback?code=` — exchange OAuth-style code for `session` cookie
- `GET /logout` — clear session
- `GET /api/session` · `GET /api/me` — current household identity (`user_id`, `username`, `roles`, optional `tenant_id`). Session required. Also sets `X-MuxCore-User-Id`. Used by media-ui `refreshCurrentUserId` for PIN salt.

Set `MEDIA_UI_PUBLIC_URL` and `MEDIA_UI_TRUSTED_PROXIES` (dawn/dusk `/128` CIDRs) when behind edge nginx so Secure cookies and callback origins match `https://mux.zem.systems`.

