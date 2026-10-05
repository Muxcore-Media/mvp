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
./scripts/publish-muxcored-local.sh v0.6.13
./scripts/publish-module-images.sh v0.6.13
```

## Install (primary)

```bash
cd _mvp
export MUXCORE_REGISTRY=localhost:5000/muxcore   # or ghcr.io/muxcore-media once images are published
export MUXCORE_IMAGE_TAG=v0.6.13
export DOWNLOADER_ENGINE=fixture
export SECRETS_MASTER_KEY=...   # generate ONCE with `openssl rand -hex 32`; keep it outside backups and reuse it
docker compose -f docker-compose.registry.yml pull
docker compose -f docker-compose.registry.yml up -d
./scripts/smoke-registry.sh
```

`./scripts/smoke-registry.sh` (or `./smoke.sh` when the registry compose stack is up and host `go build` is unavailable) verifies the install using **curl**, **docker compose**, and a **grpcurl** container only — no sibling clones and no Go toolchain on the host.

**State and backups (ADR-0013).** Every stateful module keeps its data on a named volume mounted at a fixed path, as declared in the `state:` map of [`household-manifest.yaml`](../household-manifest.yaml). The `backup-local` profile mounts each of those volumes read-only at `/source/<module-id>` and archives them by default. Library media (`movies`, `shows`, `downloads`) and key material are not archived: keep `SECRETS_MASTER_KEY` (and an export of the `encryption-aesgcm-data` keyring) somewhere else, because a restored `secrets.json` cannot be read without the key. `./scripts/check-state-coverage.sh` verifies that the manifest, the compose file, and the backup mounts agree.

Operator UI defaults ([`PORTS.md`](../PORTS.md)):

- Admin UI: `http://127.0.0.1:8082`
- API: `http://127.0.0.1:18080`
- Core health: `http://127.0.0.1:8080/health`

## Publish images (LAN registry — no GHCR)

Build and push `muxcored` with [`../scripts/publish-muxcored-local.sh`](../scripts/publish-muxcored-local.sh):

```bash
# LAN registry (default MUXCORE_REGISTRY=localhost:5000/muxcore; see ../local-registry.sh)
./local-registry.sh start
./scripts/publish-muxcored-local.sh v0.6.13

# Build + tag only (no push)
BUILD_ONLY=1 ./scripts/publish-muxcored-local.sh v0.6.13
```

`MUXCORE_REGISTRY` defaults to `localhost:5000/muxcore` in the publish scripts and in the compose file — set the same value on publish and install hosts.

Tag module images (`api-rest`, `auth-local`, …) under the same registry prefix and `${MUXCORE_IMAGE_TAG}` using [`../scripts/publish-module-images.sh`](../scripts/publish-module-images.sh).

The module repositories are private, so an in-image `go build` (`dockerfiles/module.Dockerfile`) cannot fetch their dependencies without credentials inside the build. The working path builds every binary once on the host, in umbrella workspace mode with the ADR-0012 flags ([ADR-0014](https://github.com/Muxcore-Media/umbrella/blob/main/docs/adr/0014-end-to-end-gate-placement.md)), and only packages it:

```bash
./scripts/build-module-binaries.sh /tmp/muxcore-bin            # every image in the publish set; or list modules
PREBUILT_DIR=/tmp/muxcore-bin ./scripts/publish-module-images.sh v0.6.13
```

`PREBUILT_DIR` uses `dockerfiles/module-prebuilt.Dockerfile` (alpine, uid 1000 `app`, same pre-created mount points as `module.Dockerfile`) and `dockerfiles/media-ui-prebuilt.Dockerfile` (BFF binary + SPA built from `media-ui-app`). Missing binaries are built first. Module runtime files (`policies*.yaml`) and runtime packages (`ffmpeg` for media-ffprobe) go into both image variants.

## Clean-room check (release gate)

The umbrella's [`scripts/clean-room.sh`](https://github.com/Muxcore-Media/umbrella/blob/main/scripts/clean-room.sh) runs this whole page end to end on one machine: ephemeral `registry:2` on `localhost:5000`, host-built images at the release-train tag, `docker compose up --wait` from the umbrella root with generated secrets, `smoke.sh` with the fixture acquisition profiles (`indexer-piratebay`, `downloader-torrent`; `SMOKE_REQUIRE_ACQUISITION=1`), idle RSS report (NFR-PERF-003), optional `--restore-drill`, then `down -v`. CI runs it from `.github/workflows/clean-room.yml` (manual; needs `MUXCORE_CI_TOKEN`, B-1).

Rootless **podman** (4.x) needs, besides `podman system service` + `DOCKER_HOST` for compose:

- container DNS: with the CNI network backend, the `dnsname` plugin (`golang-github-containernetworking-plugin-dnsname` on Debian) — otherwise containers cannot resolve each other;
- the plain-HTTP registry marked insecure (the script writes a `registries.conf.d` drop-in for `localhost:5000` and removes it afterwards);
- healthchecks run from systemd timers, so in a container without systemd the script drives `podman healthcheck run` itself (otherwise `up --wait` never sees `core` healthy).

## Dev-profile topology notes

This compose file runs the **dev** profile (`MUXCORE_INSECURE_DISABLE_TLS=true`, plaintext mesh) until the household switch (roadmap T-M3-02g). Two consequences shape the file:

- Modules rewrite a host-less gRPC bind (`:9420`) to `127.0.0.1` in insecure mode and advertise the address they listen on, so every module's `*_GRPC_ADDR` is `<service>:<port>` (`scripts/check-compose-grpc-binds_test.sh`).
- Core dials its security sidecars (auth, call policy, publish policy) in plaintext **only on loopback**, so `auth-local`, `call-policy-default` and `publish-policy-default` share core's network namespace (`network_mode: service:core`; their ports are published on `core`, and `core` carries their names as network aliases). If you recreate `core`, recreate them too.

Core and the modules serve no gRPC reflection; registry smoke gives grpcurl `proto/smoke.protoset`, generated by `cmd/smokeprotoset` (`scripts/check-smoke-protoset_test.sh` checks it is current).

### Podman / Docker

The publish script prefers `podman`, then `docker` (`CONTAINER_RUNTIME` overrides). If neither is installed:

```bash
cd ../core
docker build --build-arg VERSION=0.6.13 -t localhost/muxcored:v0.6.13 -f Dockerfile .
docker tag localhost/muxcored:v0.6.13 ${MUXCORE_REGISTRY:-localhost:5000/muxcore}/muxcored:v0.6.13
# push when ready:
docker push ${MUXCORE_REGISTRY:-localhost:5000/muxcore}/muxcored:v0.6.13
```

Same commands work with `podman` instead of `docker`.

## Fixture acquisition gate

Smoke and day-1 demos must use `DOWNLOADER_ENGINE=fixture`. Do not require live indexers, torrents, paid Usenet, or paid debrid for install verification.

## GHCR (`MUXCORE_REGISTRY=ghcr.io/muxcore-media`)

GHCR uses the **same** [`../docker-compose.registry.yml`](../docker-compose.registry.yml); there is no separate GHCR compose file. Once images are published under `ghcr.io/muxcore-media/*`:

```bash
export MUXCORE_REGISTRY=ghcr.io/muxcore-media MUXCORE_IMAGE_TAG=v0.6.13
docker compose -f docker-compose.registry.yml pull
docker compose -f docker-compose.registry.yml up -d
```

Publishing is **blocked** until a token with `write:packages` exists. Build-only smoke (no push); uses `core/Dockerfile.monorepo` when monorepo siblings exist:

```bash
./scripts/smoke-ghcr-build.sh v0.6.13
```

Unlock push (interactive — needs read:packages + write:packages):

```bash
gh auth refresh -h github.com -s write:packages,read:packages,repo
./scripts/publish-muxcored-ghcr.sh v0.6.13
# module images:
gh auth token | podman login ghcr.io -u <user> --password-stdin
MUXCORE_REGISTRY=ghcr.io/muxcore-media ./scripts/publish-module-images.sh v0.6.13
```

Alternative: a GitHub Actions release workflow on `Muxcore-Media/core` with `packages: write` can publish `ghcr.io/muxcore-media/muxcored` (see `core/.github/workflows/release.yml`; pushing workflow files needs `workflow` scope on `gh`).

Until `write:packages` is available, the LAN registry is the supported non-dev path.

## Playback product decision

**End state:** `media-ui-app` replaces Jellyfin web for browse **and** play (see workspace `MASTER-ROADMAP.md`).

**Near-term:** after install, configure the `jellyfin` bridge and `USERDATA_SYNC` / `USERDATA_LOCAL_URL` so households can hand off playback to Jellyfin while MuxCore userdata stays coherent. That handoff must not be treated as the final UI architecture.
