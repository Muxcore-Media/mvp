# Acquisition runbook (gringotts)

Live indexer HTTP and torrent peer traffic must egress via Proton WireGuard (`wg-mux` / `WG_CONF`). Mesh SSH stays on `wg0`. Never set `WG_USE_WG_QUICK=1` or `WG_KILL_SWITCH=true` on this mesh hub.

Related: [`../tls/README.md`](../tls/README.md), [`../tls/SECRET-ROTATION.md`](../tls/SECRET-ROTATION.md), [`../../TASKS-INDEXER-TORRENT.md`](../../TASKS-INDEXER-TORRENT.md).

## Library / storage layout

Mass-storage mounts on gringotts (`/mnt/mass-storage/media` btrfs `@media`):

| Role | Path | Env |
|------|------|-----|
| Movies library | `/mnt/mass-storage/media/movies` | `MVP_LIBRARY_ROOT` |
| TV library | `/mnt/mass-storage/media/shows` | `MVP_TV_LIBRARY_ROOT` |
| Torrent downloads / scanner watch | `/mnt/mass-storage/media/torrents` | `MVP_DOWNLOADS_DIR` (also `DOWNLOAD_DIR` for downloader) |

Symlinks `Movies → movies`, `TV → shows` exist for convenience; prefer the real dirs in `.env`.

`run-host.sh` passes `MVP_LIBRARY_ROOT` → `SCANNER_LIBRARY_ROOT` and `MVP_DOWNLOADS_DIR` → `SCANNER_DEFAULT_WATCH_DIR` with `SCANNER_IMPORT_MODE=copy`.

### Scanner NAS permissions

- Operator user `ender` is in group `mediacontent` and has POSIX ACL `user:ender:rwx` (default ACL inherited) on `movies`, `shows`, and `torrents`.
- Library dirs are `root:mediacontent` with setgid (`drwxrwsr-x+`). New files should stay group-writable for Jellyfin (NixOS) and MuxCore scanner.
- `torrents` is currently `nobody:1000` with the same `ender` ACL — scanner/downloader run as `ender` and can read/write.
- Do **not** run acquisition as root. If imports fail with permission errors, refresh ACL:

```bash
# example — only if ACL drifted
sudo setfacl -R -m u:ender:rwx,d:u:ender:rwx /mnt/mass-storage/media/{movies,shows,torrents}
sudo setfacl -R -m g:mediacontent:rwx,d:g:mediacontent:rwx /mnt/mass-storage/media/{movies,shows}
```

### Backup-local and incomplete torrents

`backup-local` archives whatever is in `BACKUP_SOURCE_DIRS` / per-request `source_paths` — it has **no** built-in exclude filter.

**Do not** point `BACKUP_SOURCE_DIRS` at `/mnt/mass-storage/media/torrents` (or the whole `@media` tree) without excluding incomplete work:

- Directory: `/mnt/mass-storage/media/torrents/.incomplete`
- Session/partials: `*.parts`, `.torrent.db`, `.torrent.bolt.db`
- Client leftovers: `*.part`, `*.!qb`, `*.!ut`, `*.crdownload`, `*.tmp`, `*.download`

Prefer backing up MuxCore state under `mvp/data/` (sqlite / module DBs) and finished library trees (`movies`, `shows`) only. If you must snapshot torrents, stage a filtered copy first or pass narrow `source_paths` that omit `.incomplete` and `*.parts`.

**Enable in MVP:** `MVP_ENABLE_BACKUP_LOCAL=1` in `.env` for `run-host.sh`, or `docker compose --profile backup-local up -d` (registry: `docker compose -f docker-compose.registry.yml --profile backup-local up -d`). Point `BACKUP_SOURCE_DIRS` at household state only — defaults in `.env.example` cover `auth-local`, `database-sqlite`, and `userdata-local` data paths; see excludes above before widening.

Scanner already ignores non-video extensions (so `*.parts` / `.incomplete` are not imported as media).

---

## Request flow (no jellyfin bridge)

MuxCore jellyfin bridge stays off (`MVP_ENABLE_JELLYFIN=0`). NixOS Jellyfin is separate.

Path:

1. Consumer / admin **Request** UI → `request-media` (`:9380`)
2. `request-media` creates the request record and async `AddToQueue` on `media-automation`
3. Admin **Automation** queue shows the wanted item (Search / Fixture dispatch)

Verify on gringotts:

```bash
# jellyfin bridge must not be running
pgrep -af 'mvp/bin/jellyfin' || echo 'jellyfin bridge absent (expected)'

curl -sS -X POST http://127.0.0.1:9380/api/request \
  -H 'Content-Type: application/json' \
  -d '{"itemType":"movie","tmdbId":550,"title":"Fight Club","year":1999}'
# expect requestId + status added; request-media.log: queued for acquisition
```

---

## Enable live acquisition

**Prerequisite:** VPN prove, then flip flags. Leave fixtures until intentionally going live.

```bash
cd ~/Projects/muxcore/mvp
grep -E '^WG_' .env
# Must keep:
#   WG_CONF=…/wg-mux.conf
#   WG_USE_WG_QUICK=0
#   WG_KILL_SWITCH=false
#   MVP_ENABLE_JELLYFIN=0

./scripts/check-vpn-up.sh
```

Edit `.env` (or merge carefully):

```bash
MVP_ENABLE_INDEXER_PIRATEBAY=1
# Offline first:
INDEXER_FIXTURE=1
# Live Apibay only when intentional + VPN OK — then unset INDEXER_FIXTURE and set:
# PIRATEBAY_API_BASE=https://apibay.org

# Optional Torznab peer
# MVP_ENABLE_INDEXER_TORZNAB=1
# TORZNAB_URL=…   # remote URL requires WG_CONF

MVP_ENABLE_DOWNLOADER_TORRENT=1
DOWNLOADER_ENGINE=fixture   # switch to live engine only after VPN check
# DOWNLOAD_DIR=/mnt/mass-storage/media/torrents   # optional; defaults via MVP_DOWNLOADS_DIR

# Optional external clients (not default host):
# MVP_ENABLE_DOWNLOADER_QBITTORRENT=1
# QBIT_FIXTURE=1                  # CI / offline — no live qBit
# # QBITTORRENT_URL=http://127.0.0.1:8080
# MVP_ENABLE_DOWNLOADER_SABNZBD=1
# # SABNZBD_URL=… SABNZBD_API_KEY=…
```

Restart only the peers you enabled:

```bash
./run-host.sh restart indexer-piratebay      # if enabled
./run-host.sh restart downloader-native-torrent
# or after env edits: ./run-host.sh up
```

Confirm admin **Automation** page lists indexer/downloader peers, and history shows indexer names after Search/dispatch.

Live torrent engine: set `DOWNLOADER_ENGINE` to something other than `fixture`/`fake` **only** when `check-vpn-up.sh` passes. On gringotts, kill-switch / `wg-quick` remain forbidden.

---

## Pin live binaries to origin

Do not `scp` an unpublished binary onto the vault host. `media-scanner`, `media-automation`, and `downloader-native-torrent` must match `origin/main` (`muxcore.json` version + `Info()` Version, HEAD == origin).

```bash
cd mvp   # or _mvp
./scripts/install-origin-module.sh media-scanner --verify-only
./scripts/install-origin-module.sh media-automation --dest ./bin
# stop the peer, mv bin/<name>.new bin/<name>, chmod +x, restart
MVP_DEPLOY_SSH=ender@vault ./scripts/install-origin-module.sh --verify-live
# appends run/ORIGIN-BINARIES.log (sha + version) when live logs match origin
```

Refuse cases: dirty `muxcore.json`, HEAD not pushed, requested version string not on origin, `Info()` Version ≠ `muxcore.json`.

After a vault deploy, force one wanted-search pass (do not wait 15 minutes for RSS):

```bash
./scripts/search-now.sh
# or: grpcurl -plaintext -d '{}' 127.0.0.1:9460 muxcore.automation.v1.AutomationService/SearchNow
```

---

## Disable live acquisition instantly

Flip flags off (or force fixtures) and restart. Do **not** touch `WG_*` or re-enable jellyfin.

```bash
cd ~/Projects/muxcore/mvp

# Instant safe state — no live Apibay / no live swarm
# In .env:
#   MVP_ENABLE_INDEXER_PIRATEBAY=0
#   MVP_ENABLE_INDEXER_TORZNAB=0
#   MVP_ENABLE_DOWNLOADER_TORRENT=0
#   unset PIRATEBAY_API_BASE  (or leave empty)
#   DOWNLOADER_ENGINE=fixture   # if you keep the downloader peer for offline smoke

./run-host.sh restart indexer-piratebay || true
./run-host.sh restart indexer-torznab || true
./run-host.sh restart downloader-native-torrent || true
# Prefer stop-one (plain `stop` kills the entire host stack):
./run-host.sh stop-one indexer-piratebay 2>/dev/null || true
./run-host.sh stop-one indexer-torznab 2>/dev/null || true
./run-host.sh stop-one downloader-native-torrent 2>/dev/null || true
# Full `./run-host.sh stop` sweeps all MVP binaries (including acquisition peers).
# Use stop-one/restart when swapping one peer without restarting the whole stack.
```

Optional offline-only leave-on:

```bash
MVP_ENABLE_INDEXER_PIRATEBAY=1
INDEXER_FIXTURE=1
PIRATEBAY_API_BASE=
MVP_ENABLE_DOWNLOADER_TORRENT=1
DOWNLOADER_ENGINE=fixture
```

Then `./run-host.sh restart indexer-piratebay downloader-native-torrent`.

---

## Vault soak (desk → vault)

Origin-pinned acquisition modules on the homelab vault MVP use Forgejo `main` pins — do not scp ad-hoc binaries.

```bash
# From umbrella workspace (desk/thin)
_mvp/scripts/install-origin-module.sh media-scanner --verify-only
MVP_DEPLOY_SSH=ender@fd2c:a2fd:5d9e:ab72:9d99:930d:f160:3e95 \
  _mvp/scripts/install-origin-module.sh --verify-live

_mvp/scripts/deploy-module-to-vault.sh media-scanner
_mvp/scripts/smoke-vault-all.sh
```

See [`../../AGENTS.md`](../../AGENTS.md) (Agent deploy policy) and [`../scripts/install-origin-module.sh`](../scripts/install-origin-module.sh).
