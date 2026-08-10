# Live acquisition (VPN + Apibay + anacrolix)

Offline default: `DOWNLOADER_ENGINE=fixture` (no BitTorrent). Live path is **opt-in** and must not gate PRs.

## Required env

| Variable | Example | Purpose |
|----------|---------|---------|
| `PIRATEBAY_API_BASE` | `https://apibay.org` | Starts `indexer-piratebay` in `run-host.sh` |
| `DOWNLOADER_ENGINE` | `anacrolix` | Real torrent client (not `fixture`) |
| `WG_CONF` | `/home/ender/Projects/muxcore/wg-mux.conf` | Source-routed Proton WireGuard |
| `WG_KILL_SWITCH` | `false` | **Required on gringotts** (mesh hub) |
| `SMOKE_LIVE_ACQUISITION` | `1` | Enables live smoke step / nightly script |

Optional: `SMOKE_LIVE_TIMEOUT`, `SMOKE_LIVE_MIN_SEEDERS`, `SMOKE_LIVE_MIN_BYTES`, `TORRENT_LISTEN_PORT`, `NAT_PMP_PORT`. Prefer `TORZNAB_URL` + `indexer-torznab` for multi-indexer; Apibay remains the live-smoke peer.

## VPN policy (gringotts)

1. Use **source-routed** WireGuard (`wg-mux`), not full-tunnel `wg-quick` on the mesh hub.
2. Never set `WG_USE_WG_QUICK=1` or `WG_KILL_SWITCH=true` on gringotts — that blackholes mesh SSH.
3. Conf should include Proton `DNS = 10.2.0.1` when NAT-PMP is desired.
4. File mode `600`; rotate per [tls/SECRET-ROTATION.md](tls/SECRET-ROTATION.md).

Details: [tls/README.md](tls/README.md).

## Manual verify

```bash
cd mvp
# .env already has PIRATEBAY_API_BASE, DOWNLOADER_ENGINE=anacrolix, WG_CONF=...
./run-host.sh up
SMOKE_LIVE_ACQUISITION=1 ./smoke.sh
```

Smoke skips fixture Dispatch when engine is not `fixture` and runs `liveacquisition` instead.

## Nightly job (not a PR gate)

Use the helper on the acquisition host (self-hosted runner or cron):

```bash
# once: chmod +x scripts/nightly-live-acquisition.sh
# cron example (02:17 local, logs under mvp/run/):
# 17 2 * * * /home/ender/Projects/muxcore/mvp/scripts/nightly-live-acquisition.sh >>/home/ender/Projects/muxcore/mvp/run/nightly-live.log 2>&1
```

Or GitHub Actions `workflow_dispatch` / `schedule` on a **self-hosted** runner with VPN + secrets — never on `ubuntu-latest` without a tunnel. See [scripts/nightly-live-acquisition.sh](scripts/nightly-live-acquisition.sh).
