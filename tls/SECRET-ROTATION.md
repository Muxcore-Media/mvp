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
4. Point `WG_CONF` in `mvp/.env` at that path; restart affected media peers (or full `./run-host.sh up`).
5. Delete or shred the old conf (`shred -u wg.conf` / remove `.bak`).

### Rotation drill (verify egress IP change)

```bash
# 1) Note current egress via wg-mux
cd ~/Projects/muxcore/mvp
./scripts/check-vpn-up.sh   # record egress_ip=...

# 2) Create new Proton peer; save conf somewhere absolute, e.g. /tmp/wg-mux-new.conf
export NEW_TMDB_API_KEY='…'   # required by helper even if only WG changed; or edit .env WG only
export NEW_WG_CONF_PATH=/tmp/wg-mux-new.conf
./tls/apply-rotated-secrets.sh
# Installs mode-600 ~/Projects/muxcore/wg-mux.conf and sets WG_CONF=…; forces WG_USE_WG_QUICK=0

# 3) Restart downloader only (leave mesh up)
./run-host.sh restart downloader-native-torrent
# or: stop/start the module process your host runner uses

# 4) Confirm tunnel + new public IP
./scripts/check-vpn-up.sh   # egress_ip should differ from step 1

# 5) Shred the old peer conf / revoke old Proton peer in the account UI
```

## VPN policy (never on mesh hub)

- Default: **source-routed** `wg-mux` (see README in this directory).
- Never set `WG_USE_WG_QUICK=1` / full-tunnel kill-switch on gringotts.
- Isolated download-only hosts may use kill-switch.

## Apply helper

```bash
# After exporting NEW_TMDB_API_KEY and NEW_WG_CONF_PATH:
./tls/apply-rotated-secrets.sh
```
