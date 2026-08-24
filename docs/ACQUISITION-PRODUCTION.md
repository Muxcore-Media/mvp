# Production acquisition profile (vault / gringotts)

Source env for live indexer + torrent acquisition. **Requires VPN** (`WG_CONF` readable, `./scripts/check-vpn-up.sh` passing).

## Usage

```bash
cd _mvp
set -a; source profiles/acquisition-production.env; set +a
./run-host.sh up
```

On vault: merge vars into `muxcore-test.nix` or `$MVP/.env` (never commit secrets).

## Prerequisites

- `./scripts/check-vpn-up.sh` — WireGuard interface up, egress via tunnel
- Origin-pinned binaries: `./scripts/install-origin-module.sh --verify-live media-scanner media-automation downloader-native-torrent`
- `TMDB_API_KEY` for discover/search
- Library roots configured (`MVP_LIBRARY_ROOT`, `MVP_TV_LIBRARY_ROOT`)

## Flags enabled

See `profiles/acquisition-production.env` for the full list. Summary:

| Component | Setting |
|-----------|---------|
| Downloader | `DOWNLOADER_ENGINE=live`, VPN required |
| Indexer | `MVP_ENABLE_INDEXER_PIRATEBAY=1` (or torznab) |
| Automation | always-on; event subscribe delay 1s |
| Jellyfin bridge | `MVP_ENABLE_JELLYFIN=0` when NixOS Jellyfin is canonical |
| Userdata | `MVP_ENABLE_USERDATA_LOCAL=1` |

## Verify

```bash
./scripts/install-origin-module.sh --verify-live media-scanner media-automation downloader-native-torrent
export MUXCORE_GRPC_ADDR=127.0.0.1:9090 MUXCORE_INSECURE_DISABLE_TLS=true
./bin/muxcorectl modules list | grep -E 'automation|scanner|downloader'
```

Submit a test request via admin-ui or `mux.zem.systems`; confirm wanted row → download event → import in automation logs.
