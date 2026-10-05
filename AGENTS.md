# AGENTS.md — _mvp

MuxCore **MVP household stack** — local `run-host.sh`, registry compose, smoke gate, and vault deploy helpers. Workspace deploy and SSH: [`../AGENTS.md`](../AGENTS.md). Ports: [`PORTS.md`](PORTS.md).

## What this repo is

| Artifact | Role |
|----------|------|
| `run-host.sh` | Primary dev/soak launcher (sibling module binaries under `bin/`) |
| `docker-compose.registry.yml` | Household install from an OCI registry (`MUXCORE_REGISTRY`, default LAN `localhost:5000/muxcore`; GHCR `ghcr.io/muxcore-media` once published) |
| `docker-compose.yml` | Reference compose over sibling clones (dev) |
| `household-manifest.yaml` | Canonical required/recommended module set + `core_tag` pin |
| `smoke.sh` | Fixture acquisition gate — must PASS before “deployable” claims |
| `cmd/mediauiprox` | Consumer BFF (`media-ui` image) — see [`BFF-API.md`](BFF-API.md) |
| `scripts/deploy-module-to-vault.sh` | Build + scp + restart one module on vault |
| `scripts/check-household-manifest.sh` | Registry compose ↔ manifest parity (CI script-tests) |

`_mvp/muxcore.json` is **core server listen config**, not a sidecar module manifest.

## Operator surfaces

```bash
cd _mvp
./run-host.sh up          # local stack from bin/
./run-host.sh status
./smoke.sh                # fixture gate (DOWNLOADER_ENGINE=fixture)
./scripts/check-household-manifest.sh

# Registry install (see docs/PUBLIC-INSTALL.md)
./local-registry.sh start
export MUXCORE_REGISTRY=localhost:5000/muxcore MUXCORE_IMAGE_TAG=v0.5.7
docker compose -f docker-compose.registry.yml up -d

# Vault deploy (from workspace root)
../_mvp/scripts/deploy-module-to-vault.sh admin-ui --verify-all
../_mvp/scripts/smoke-vault-all.sh
```

## Agent rules

- Do not edit polluted workspace dumps (`media-ui/`, `media-jellyfin/`, …) — see `MASTER-ROADMAP.md` Appendix H.
- Acquisition smoke stays on **fixture** paths; no live pirate indexer tasks.
- When changing household module sets, update `household-manifest.yaml`, `docker-compose.registry.yml`, and `publish-module-images.sh` together; run `check-household-manifest.sh`.
- `MVP_ENABLE_JELLYFIN=0` is the default — standalone NixOS Jellyfin is separate from the bridge module.
- Cross-module events: `github.com/Muxcore-Media/contracts-media/events`.

## Build & test

```bash
cd _mvp
nix-shell -p go --run 'go test ./...'   # or plain `go test ./...` with mise
./scripts/run-script-tests.sh
```

Source, issues, and CI live on GitHub (`github.com/Muxcore-Media/mvp`). CI runs on GitHub-hosted runners: `.github/workflows/ci.yml` (generated from the umbrella template by `scripts/sync-ci.sh`) and `.github/workflows/script-tests.yml`. Private modules: `gh auth setup-git` locally; `MUXCORE_CI_TOKEN` in CI.
