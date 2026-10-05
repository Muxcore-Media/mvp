# Non-developer install (registry compose)

Primary path for household / homelab installs: pull prebuilt images from an **OCI registry** with [`../docker-compose.registry.yml`](../docker-compose.registry.yml). No sibling Go clones, and **no GHCR `write:packages`** for the default path ([ADR-0007](https://github.com/Muxcore-Media/umbrella/blob/main/docs/adr/0007-install-paths-github-only.md)).

One compose file serves every registry; `MUXCORE_REGISTRY` picks the source:

| Registry | `MUXCORE_REGISTRY` | Status |
|----------|--------------------|--------|
| **LAN registry** (default) | `localhost:5000/muxcore` | Supported — images built from GitHub sources with the publish scripts below and pushed to [`../local-registry.sh`](../local-registry.sh) |
| **GHCR** | `ghcr.io/muxcore-media` | Blocked until images are published there (needs a token with `write:packages`) |

Developers keep using [`../run-host.sh`](../run-host.sh).

## Prerequisites

- Docker Compose v2 (or Podman Compose)
- Images available under `${MUXCORE_REGISTRY}/…` (see Publish below)
- Fixture smoke: `DOWNLOADER_ENGINE=fixture`
- Sibling checkouts of the module repos from `https://github.com/Muxcore-Media/<repo>` on the build host (the umbrella `git submodule update --init` provides them). The repos are private: run `gh auth setup-git` once (or export `GH_TOKEN`) — never embed tokens in URLs.

### LAN registry

```bash
cd _mvp
./local-registry.sh start
export MUXCORE_REGISTRY=localhost:5000/muxcore
./scripts/publish-muxcored-local.sh v0.6.7
./scripts/publish-module-images.sh v0.6.7
```

## Install (primary)

```bash
cd _mvp
export MUXCORE_REGISTRY=localhost:5000/muxcore   # or ghcr.io/muxcore-media once images are published
export MUXCORE_IMAGE_TAG=v0.6.7
export DOWNLOADER_ENGINE=fixture
docker compose -f docker-compose.registry.yml pull
docker compose -f docker-compose.registry.yml up -d
./scripts/smoke-registry.sh
```

`./scripts/smoke-registry.sh` (or `./smoke.sh` when the registry compose stack is up and host `go build` is unavailable) verifies the install using **curl**, **docker compose**, and a **grpcurl** container only — no sibling clones and no Go toolchain on the host.

Operator UI defaults ([`PORTS.md`](../PORTS.md)):

- Admin UI: `http://127.0.0.1:8082`
- API: `http://127.0.0.1:18080`
- Core health: `http://127.0.0.1:8080/health`

## Publish images (LAN registry — no GHCR)

Build and push `muxcored` with [`../scripts/publish-muxcored-local.sh`](../scripts/publish-muxcored-local.sh):

```bash
# LAN registry (default MUXCORE_REGISTRY=localhost:5000/muxcore; see ../local-registry.sh)
./local-registry.sh start
./scripts/publish-muxcored-local.sh v0.6.7

# Build + tag only (no push)
BUILD_ONLY=1 ./scripts/publish-muxcored-local.sh v0.6.7
```

`MUXCORE_REGISTRY` defaults to `localhost:5000/muxcore` in the publish scripts and in the compose file — set the same value on publish and install hosts.

Tag module images (`api-rest`, `auth-local`, …) under the same registry prefix and `${MUXCORE_IMAGE_TAG}` using [`../scripts/publish-module-images.sh`](../scripts/publish-module-images.sh).

### Podman / Docker

The publish script prefers `podman`, then `docker` (`CONTAINER_RUNTIME` overrides). If neither is installed:

```bash
cd ../core
docker build --build-arg VERSION=0.6.7 -t localhost/muxcored:v0.6.7 -f Dockerfile .
docker tag localhost/muxcored:v0.6.7 ${MUXCORE_REGISTRY:-localhost:5000/muxcore}/muxcored:v0.6.7
# push when ready:
docker push ${MUXCORE_REGISTRY:-localhost:5000/muxcore}/muxcored:v0.6.7
```

Same commands work with `podman` instead of `docker`.

## Fixture acquisition gate

Smoke and day-1 demos must use `DOWNLOADER_ENGINE=fixture`. Do not require live indexers, torrents, paid Usenet, or paid debrid for install verification.

## GHCR (`MUXCORE_REGISTRY=ghcr.io/muxcore-media`)

GHCR uses the **same** [`../docker-compose.registry.yml`](../docker-compose.registry.yml); there is no separate GHCR compose file. Once images are published under `ghcr.io/muxcore-media/*`:

```bash
export MUXCORE_REGISTRY=ghcr.io/muxcore-media MUXCORE_IMAGE_TAG=v0.6.7
docker compose -f docker-compose.registry.yml pull
docker compose -f docker-compose.registry.yml up -d
```

Publishing is **blocked** until a token with `write:packages` exists. Build-only smoke (no push); uses `core/Dockerfile.monorepo` when monorepo siblings exist:

```bash
./scripts/smoke-ghcr-build.sh v0.6.7
```

Unlock push (interactive — needs read:packages + write:packages):

```bash
gh auth refresh -h github.com -s write:packages,read:packages,repo
./scripts/publish-muxcored-ghcr.sh v0.6.7
# module images:
gh auth token | podman login ghcr.io -u <user> --password-stdin
MUXCORE_REGISTRY=ghcr.io/muxcore-media ./scripts/publish-module-images.sh v0.6.7
```

Alternative: a GitHub Actions release workflow on `Muxcore-Media/core` with `packages: write` can publish `ghcr.io/muxcore-media/muxcored` (see `core/.github/workflows/release.yml`; pushing workflow files needs `workflow` scope on `gh`).

Until `write:packages` is available, the LAN registry is the supported non-dev path.

## Playback product decision

**End state:** `media-ui-app` replaces Jellyfin web for browse **and** play (see workspace `MASTER-ROADMAP.md`).

**Near-term:** after install, configure the `jellyfin` bridge and `USERDATA_SYNC` / `USERDATA_LOCAL_URL` so households can hand off playback to Jellyfin while MuxCore userdata stays coherent. That handoff must not be treated as the final UI architecture.
