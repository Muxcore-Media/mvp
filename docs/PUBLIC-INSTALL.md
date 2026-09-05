# Non-developer install (Forgejo / LAN registry)

Primary path for household / homelab installs: pull prebuilt images from a **Forgejo or LAN OCI registry**. No sibling Go clones, and **no GHCR `write:packages`**.

Developers keep using [`../run-host.sh`](../run-host.sh). GHCR ([`../docker-compose.ghcr.yml`](../docker-compose.ghcr.yml)) is a **future public mirror** once packages write exists on the GitHub consumer org.

## Prerequisites

- Docker Compose v2 (or Podman Compose)
- Images available under `${MUXCORE_REGISTRY}/…` (see Publish below)
- Fixture smoke: `DOWNLOADER_ENGINE=fixture`

### LAN registry (no Forgejo yet)

```bash
cd _mvp
./scripts/local-registry.sh start
export MUXCORE_REGISTRY=localhost:5000/muxcore
./scripts/publish-muxcored-local.sh v0.5.7
```

## Install (primary)

```bash
cd _mvp
export MUXCORE_REGISTRY=localhost:5000/muxcore   # or git.zem.systems/muxcore
export MUXCORE_IMAGE_TAG=v0.5.7
export DOWNLOADER_ENGINE=fixture
docker compose -f docker-compose.registry.yml pull
docker compose -f docker-compose.registry.yml up -d
./smoke.sh
```

Operator UI defaults ([`PORTS.md`](../PORTS.md)):

- Admin UI: `http://127.0.0.1:9080`
- API: `http://127.0.0.1:18080`
- Core health: `http://127.0.0.1:8080/health`

## Publish images (origin / LAN — no GHCR)

Build and push `muxcored` with [`../scripts/publish-muxcored-local.sh`](../scripts/publish-muxcored-local.sh):

```bash
# Forgejo package registry (default MUXCORE_REGISTRY)
./scripts/publish-muxcored-local.sh v0.5.7

# LAN registry (see ../local-registry.sh)
./local-registry.sh start
MUXCORE_REGISTRY=localhost:5000/muxcore ./scripts/publish-muxcored-local.sh v0.5.7

# Build + tag only (no push)
BUILD_ONLY=1 MUXCORE_REGISTRY=localhost:5000/muxcore ./scripts/publish-muxcored-local.sh v0.5.7
```

`MUXCORE_REGISTRY` defaults to `git.zem.systems/muxcore`. Compose defaults to `localhost:5000/muxcore` when unset — set the same value on publish and install hosts.

Tag module images (`api-rest`, `auth-local`, …) under the same registry prefix and `${MUXCORE_IMAGE_TAG}` using [`scripts/publish-module-images.sh`](scripts/publish-module-images.sh).

### Podman / Docker

The publish script prefers `podman`, then `docker` (`CONTAINER_RUNTIME` overrides). If neither is installed:

```bash
cd ../core
docker build --build-arg VERSION=0.5.7 -t localhost/muxcored:v0.5.7 -f Dockerfile .
docker tag localhost/muxcored:v0.5.7 ${MUXCORE_REGISTRY:-git.zem.systems/muxcore}/muxcored:v0.5.7
# push when ready:
docker push ${MUXCORE_REGISTRY:-git.zem.systems/muxcore}/muxcored:v0.5.7
```

Same commands work with `podman` instead of `docker`.

### Forgejo login

```bash
echo "$FORGEJO_TOKEN" | podman login git.zem.systems -u <user> --password-stdin
# or: docker login git.zem.systems -u <user>
```

Use a Forgejo token with package read/write for the `muxcore` org. Do **not** require GitHub `write:packages`.

## Fixture acquisition gate

Smoke and day-1 demos must use `DOWNLOADER_ENGINE=fixture`. Do not require live indexers, torrents, paid Usenet, or paid debrid for install verification.

## Future: public GHCR mirror

[`../docker-compose.ghcr.yml`](../docker-compose.ghcr.yml) + [`../scripts/publish-muxcored-ghcr.sh`](../scripts/publish-muxcored-ghcr.sh) + [`../scripts/smoke-ghcr-build.sh`](../scripts/smoke-ghcr-build.sh) for a public `ghcr.io/muxcore-media/*` mirror.

Build-only smoke (no push); uses `core/Dockerfile.monorepo` when monorepo siblings exist (verified vault podman 2026-08-22):

```bash
./scripts/smoke-ghcr-build.sh v0.5.7
```

Unlock push (interactive on desk — needs read:packages + write:packages):

```bash
gh auth refresh -h github.com -s write:packages,read:packages,repo
./scripts/publish-muxcored-ghcr.sh v0.5.7
```

Alternative when desk lacks packages scope: trigger the **GitHub Release** workflow on `Muxcore-Media/core` (GoReleaser publishes `ghcr.io/muxcore-media/muxcored` via Actions `packages:write`). Self-hosted runners mirror Forgejo — tag `refs/tags/v*` on GitHub must match the mirror or checkout must pin `github.sha` (see `core/.github/workflows/release.yml`; pushing workflow files needs `workflow` scope on `gh`).

**Vault host-build path** (desk without podman; verified BUILD_ONLY 2026-08-22):

```bash
gh auth refresh -h github.com -s write:packages,read:packages,repo   # required for push
GHCR_TOKEN="$(gh auth token)" ../../core/scripts/publish-muxcored-vault-ghcr.sh v0.5.7
# BUILD_ONLY: BUILD_ONLY=1 ../../core/scripts/publish-muxcored-vault-ghcr.sh v0.5.7
```

Forgejo Actions on vault is currently failing all jobs (~2s); use the script above until runner CI is repaired.

Until `write:packages` is on the gh token, Forgejo/LAN is the supported non-dev path.

## Playback product decision

**End state:** `media-ui-app` replaces Jellyfin web for browse **and** play (see workspace `MASTER-ROADMAP.md`).

**Near-term:** after install, configure the `jellyfin` bridge and `USERDATA_SYNC` / `USERDATA_LOCAL_URL` so households can hand off playback to Jellyfin while MuxCore userdata stays coherent. That handoff must not be treated as the final UI architecture.
