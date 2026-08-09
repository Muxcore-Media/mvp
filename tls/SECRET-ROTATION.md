# Secret rotation (gringotts / MVP)

Exposed chat/history values for **TMDB** and **Proton WireGuard** must be rotated. Keep files mode `600` and gitignored (`wg.conf`, `wg-mux.conf`, `**/tmdb-read-access.token`).

## TMDB

1. https://www.themoviedb.org/settings/api — revoke/create a new **API Key (v3)** (and/or Read Access Token if used elsewhere).
2. On gringotts:
   ```bash
   cd ~/Projects/muxcore/mvp
   # Prefer v3 api_key for metadata-tmdb
   sed -i 's/^TMDB_API_KEY=.*/TMDB_API_KEY=NEW_KEY/' .env
   chmod 600 .env
   # Optional JWT store (not used by current module if api_key is set):
   umask 077
   printf '%s' 'NEW_READ_ACCESS_JWT' > data/tmdb-read-access.token
   ./run-host.sh stop; ./run-host.sh up
   ```
3. Confirm search: admin/media request path or `mvp` smoke against TMDB.

## Proton WireGuard

1. Proton Account → WireGuard → **create a new peer** (do not reuse the chat-exposed private key).
2. Download the new conf; install as `~/Projects/muxcore/wg-mux.conf` mode `600`.
3. Ensure Interface has `DNS = 10.2.0.1` (NAT-PMP gateway) and leave kill-switch **off** on gringotts.
4. Point `WG_CONF` in `mvp/.env` at that path; restart `downloader-native-torrent` only (or full `./run-host.sh up`).
5. Delete or shred the old conf (`shred -u wg.conf` / remove `.bak`).

## VPN policy (never on mesh hub)

- Default: **source-routed** `wg-mux` (see README in this directory).
- Never set `WG_USE_WG_QUICK=1` / full-tunnel kill-switch on gringotts.
- Isolated download-only hosts may use kill-switch.

## Apply helper

```bash
# After exporting NEW_TMDB_API_KEY and NEW_WG_CONF_PATH:
./tls/apply-rotated-secrets.sh
```
