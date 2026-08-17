# mediauiprox BFF JSON contracts

Consumer SPA (`media-ui-app`) talks to `mvp/cmd/mediauiprox` on `:5173`.

## List endpoints

### `GET /api/movies?page=&page_size=`
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

### TV show item fields

Same poster/backdrop/stream conventions under `/images/tv/` and `/stream/tv/{episode_id}`. Includes nested `seasons[].episodes[]` with `has_file` / `stream_url`.

## Detail

- `GET /api/movies/{id}` → `{ "movie": {…} }`
- `GET /api/tv/{id}` → `{ "show": {…} }`

Missing entity → `404` with `{ "error": "…", "code": "movies.not_found" | "tv.not_found" }`.

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
