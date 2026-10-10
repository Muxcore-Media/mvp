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
- `openssl` (enrollment secret; the smoke's client certificate) and `python3` or `openssl` for the token HMAC
- Sibling checkouts of the module repos from `https://github.com/Muxcore-Media/<repo>` on the build host (the umbrella `git submodule update --init` provides them). The repos are private: run `gh auth setup-git` once (or export `GH_TOKEN`) — never embed tokens in URLs.

### LAN registry

```bash
cd _mvp
./local-registry.sh start
export MUXCORE_REGISTRY=localhost:5000/muxcore
./scripts/publish-muxcored-local.sh v0.6.15
./scripts/publish-module-images.sh v0.6.15
```

## Install (primary)

```bash
cd _mvp
export MUXCORE_REGISTRY=localhost:5000/muxcore   # or ghcr.io/muxcore-media once images are published
export MUXCORE_IMAGE_TAG=v0.6.15
export DOWNLOADER_ENGINE=fixture
export SECRETS_MASTER_KEY=...   # generate ONCE with `openssl rand -hex 32`; keep it outside backups and reuse it
./scripts/gen-enrollment.sh     # mesh enrollment secret + per-module tokens into .env (0600); safe to rerun
./scripts/gen-erasure-set.sh --env-file .env   # user-erasure ledger set (ADR-0035); rerun after enabling a profile
docker compose -f docker-compose.registry.yml pull
docker compose -f docker-compose.registry.yml up -d
./scripts/smoke-registry.sh
```

`./scripts/smoke-registry.sh` (or `./smoke.sh` when the registry compose stack is up and host `go build` is unavailable) verifies the install using **curl**, **docker compose**, **openssl** and a **grpcurl** container only — no sibling clones and no Go toolchain on the host. In the household profile it enrolls a client identity of its own (`mvp-smoke`, token from `gen-enrollment.sh --print-token`, kept in `run/smoke-id/`) and talks TLS to core and the modules.

For an external secrets backend, follow [provider switching](INFRA-BACKENDS.md#secrets-file--secrets-vault): stop the old provider, then append `docker-compose.secrets-vault.yml` last. The override requires Compose `!reset` support (tested with Docker Compose v2.40.3; Podman Compose compatibility is unverified). A bare `--profile secrets-vault` also selects `secrets-file` and is unsupported.

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
./scripts/publish-muxcored-local.sh v0.6.15

# Build + tag only (no push)
BUILD_ONLY=1 ./scripts/publish-muxcored-local.sh v0.6.15
```

`MUXCORE_REGISTRY` defaults to `localhost:5000/muxcore` in the publish scripts and in the compose file — set the same value on publish and install hosts.

Tag module images (`api-rest`, `auth-local`, …) under the same registry prefix and `${MUXCORE_IMAGE_TAG}` using [`../scripts/publish-module-images.sh`](../scripts/publish-module-images.sh).

The module repositories are private, so an in-image `go build` (`dockerfiles/module.Dockerfile`) cannot fetch their dependencies without credentials inside the build. The working path builds every binary once on the host, in umbrella workspace mode with the ADR-0012 flags ([ADR-0014](https://github.com/Muxcore-Media/umbrella/blob/main/docs/adr/0014-end-to-end-gate-placement.md)), and only packages it:

```bash
./scripts/build-module-binaries.sh /tmp/muxcore-bin            # every image in the publish set; or list modules
PREBUILT_DIR=/tmp/muxcore-bin ./scripts/publish-module-images.sh v0.6.15
```

`PREBUILT_DIR` uses `dockerfiles/module-prebuilt.Dockerfile` (alpine, uid 1000 `app`, same pre-created mount points as `module.Dockerfile`) and `dockerfiles/media-ui-prebuilt.Dockerfile` (BFF binary + SPA built from `media-ui-app`). Missing binaries are built first. Module runtime files (`policies*.yaml`) and runtime packages (`ffmpeg` for media-ffprobe) go into both image variants.

## Clean-room check (release gate)

The umbrella's [`scripts/clean-room.sh`](https://github.com/Muxcore-Media/umbrella/blob/main/scripts/clean-room.sh) runs this whole page end to end on one machine: ephemeral `registry:2` on `localhost:5000`, host-built images at the release-train tag, `docker compose up --wait` from the umbrella root with generated secrets, `smoke.sh` with the fixture acquisition profiles (`indexer-piratebay`, `downloader-torrent`; `SMOKE_REQUIRE_ACQUISITION=1`), idle RSS report (NFR-PERF-003), optional `--restore-drill`, then `down -v`. It runs the household profile (`gen-enrollment.sh` into its temporary env file; it fails unless core resolves `household` with TLS and every running service logs a mesh identity); `--dev` runs the dev override instead. CI runs it from `.github/workflows/clean-room.yml` (manual; needs `MUXCORE_CI_TOKEN`, B-1).

Rootless **podman** (4.x) needs, besides `podman system service` + `DOCKER_HOST` for compose:

- container DNS: with the CNI network backend, the `dnsname` plugin (`golang-github-containernetworking-plugin-dnsname` on Debian) — otherwise containers cannot resolve each other;
- the plain-HTTP registry marked insecure (the script writes a `registries.conf.d` drop-in for `localhost:5000` and removes it afterwards);
- healthchecks run from systemd timers, so in a container without systemd the script drives `podman healthcheck run` itself (otherwise `up --wait` never sees `core` healthy).

## Security profile: household (default)

The registry compose runs the **household** profile ([ADR-0016](https://github.com/Muxcore-Media/umbrella/blob/main/docs/adr/0016-security-profiles.md)): every gRPC hop is TLS against core's own CA, and every module proves who it is with a certificate core issued to it ([ADR-0017](https://github.com/Muxcore-Media/umbrella/blob/main/docs/adr/0017-mesh-module-identity.md)). Nothing in [`../docker-compose.registry.yml`](../docker-compose.registry.yml) sets `MUXCORE_INSECURE_DISABLE_TLS` ([`../scripts/check-compose-security_test.sh`](../scripts/check-compose-security_test.sh)).

How the pieces fit:

| Piece | Where | What |
|-------|-------|------|
| Enrollment secret + tokens | `.env`, from [`../scripts/gen-enrollment.sh`](../scripts/gen-enrollment.sh) | `MUXCORE_ENROLL_SECRET` (random, kept on reruns) and `MUXCORE_ENROLL_TOKEN_<ID>` = `mct_2_<id>_` + hex(HMAC-SHA256(secret, id)), the same value `muxcored enroll token <id>` prints. Compose refuses to start without them. |
| Erasure ledger set | `.env`, from [`../scripts/gen-erasure-set.sh`](../scripts/gen-erasure-set.sh) | `AUTH_ERASURE_CONSUMERS` (the modules auth-local serves the user-erasure ledger to, by certificate CN) and `AUTH_ERASURE_REQUIRED` (the modules whose acknowledgement completes an erasure; the same list). Derived from `household-manifest.yaml`: `state` entries with `personal: true` and `erasure: consumer` (default) that are enabled — required/recommended, or an `optional_env_gated` module whose `MVP_ENABLE_*` is `1`/`true` or whose compose profile is in `COMPOSE_PROFILES` (`--profile NAME` repeats the `docker compose --profile` flags). The identity providers (`erasure: provider`) and personal state without a ledger disposition (`erasure: none`) are excluded by the manifest. Never edit the list by hand: `gen-erasure-set.sh --env-file .env --check` fails when it is stale, and compose refuses to start without it. |
| Core CA | `core-ca` volume (`MUXCORE_GRPC_CA_CERT_DIR=/app/ca`) | CA key, the single-use enrollment ledger (`enrolled.json`) and core's server certificate (SAN `core`, `MUXCORE_TLS_SERVER_SANS`). The core image (≥ v0.6.15) pre-creates the mount points, so the volume is owned by core's user. |
| Public CA | `mesh-ca` volume | Core exports `ca.crt` there (`MUXCORE_CA_EXPORT_DIR`); every module mounts it read-only at `/data/mesh-ca` (`MUXCORE_TLS_CA`). |
| Module identity | one `<service>-id` volume per service at `/data/mesh-id` (`MUXCORE_TLS_DIR`) | On first start the module generates its key, sends a CSR with its token (`MUXCORE_BOOTSTRAP_TOKEN`) and stores the certificate (CN = module ID; SANs = module ID, loopback and `MUXCORE_ENROLL_DNS_NAMES`=service name where core's `MUXCORE_ENROLL_SAN_ALLOW` allows it). Later starts reuse it without a token. |

Consequences for operators:

- **Core's HTTP port is HTTPS** too (same certificate). Use the CA from the `mesh-ca` volume: `docker run --rm -v muxcore-mvp-registry_mesh-ca:/m:ro alpine cat /m/ca.crt > ca.crt; curl --cacert ca.crt https://127.0.0.1:8080/health`.
- **Security sidecars run in their own containers.** `auth-local`, `call-policy-default` and `publish-policy-default` are ordinary services again; core dials them over TLS and checks that the certificate's CN is the module ID it registered (no loopback-only workaround, roadmap conflict C-18).
- **Identity is key material, not backed-up state.** The `core-ca`, `mesh-ca` and `*-id` volumes are in `excluded_volumes` of [`../household-manifest.yaml`](../household-manifest.yaml): a backup never contains them, and a restore leaves them untouched, so restored modules keep working. A fresh machine starts with a new CA and enrolls every module again with the tokens in `.env`.
- **Lost a module's `<service>-id` volume** (or removed it to rotate the key): its token is already used. Run `docker compose exec core ./muxcored enroll reset <module-id>`, then restart the module. `./muxcored enroll list` shows who has enrolled.
- **Lost `core-ca`**: every stored module certificate is now from an unknown CA. Remove the `*-id` volumes too and restart the stack; the new CA has an empty ledger, so the existing tokens enroll again.
- **Rotating the enrollment secret** (`gen-enrollment.sh --rotate`) only affects future enrollments.

### Insecure dev loop

[`../docker-compose.dev.yml`](../docker-compose.dev.yml) is the only compose file that sets the insecure flag. It switches core and every module to `MUXCORE_PROFILE=dev` with `MUXCORE_INSECURE_DISABLE_TLS=true` (plaintext mesh, loud warnings) for local debugging; never use it for real data. `gen-enrollment.sh` is still needed because the base file interpolates the enrollment variables.

```bash
docker compose -f docker-compose.registry.yml -f docker-compose.dev.yml up -d
./scripts/smoke-registry.sh   # detects the dev profile (MUXCORE_SMOKE_TLS=0 forces plaintext)
```

In the dev profile core dials its security sidecars in plaintext **only on loopback**, so the override puts `auth-local`, `call-policy-default` and `publish-policy-default` back into core's network namespace (`network_mode: service:core`; their ports published on `core`, their names as aliases on `core`, host-less gRPC binds). If you recreate `core`, recreate them too. Modules rewrite a host-less gRPC bind (`:9420`) to `127.0.0.1` in insecure mode and advertise the address they listen on, which is why every module's `*_GRPC_ADDR` in the base file is `<service>:<port>` ([`../scripts/check-compose-grpc-binds_test.sh`](../scripts/check-compose-grpc-binds_test.sh)). [`../run-host.sh`](../run-host.sh) keeps the dev profile by default.

Core and the modules serve no gRPC reflection; registry smoke gives grpcurl `proto/smoke.protoset`, generated by `cmd/smokeprotoset` (`scripts/check-smoke-protoset_test.sh` checks it is current).

### Podman / Docker

The publish script prefers `podman`, then `docker` (`CONTAINER_RUNTIME` overrides). If neither is installed:

```bash
cd ../core
docker build --build-arg VERSION=0.6.15 -t localhost/muxcored:v0.6.15 -f Dockerfile .
docker tag localhost/muxcored:v0.6.15 ${MUXCORE_REGISTRY:-localhost:5000/muxcore}/muxcored:v0.6.15
# push when ready:
docker push ${MUXCORE_REGISTRY:-localhost:5000/muxcore}/muxcored:v0.6.15
```

Same commands work with `podman` instead of `docker`.

## Fixture acquisition gate

Smoke and day-1 demos must use `DOWNLOADER_ENGINE=fixture`. Do not require live indexers, torrents, paid Usenet, or paid debrid for install verification.

## GHCR (`MUXCORE_REGISTRY=ghcr.io/muxcore-media`)

GHCR uses the **same** [`../docker-compose.registry.yml`](../docker-compose.registry.yml); there is no separate GHCR compose file. Once images are published under `ghcr.io/muxcore-media/*`:

```bash
export MUXCORE_REGISTRY=ghcr.io/muxcore-media MUXCORE_IMAGE_TAG=v0.6.15
docker compose -f docker-compose.registry.yml pull
docker compose -f docker-compose.registry.yml up -d
```

Publishing is **blocked** until a token with `write:packages` exists. Build-only smoke (no push); uses `core/Dockerfile.monorepo` when monorepo siblings exist:

```bash
./scripts/smoke-ghcr-build.sh v0.6.15
```

Unlock push (interactive — needs read:packages + write:packages):

```bash
gh auth refresh -h github.com -s write:packages,read:packages,repo
./scripts/publish-muxcored-ghcr.sh v0.6.15
# module images:
gh auth token | podman login ghcr.io -u <user> --password-stdin
MUXCORE_REGISTRY=ghcr.io/muxcore-media ./scripts/publish-module-images.sh v0.6.15
```

Alternative: a GitHub Actions release workflow on `Muxcore-Media/core` with `packages: write` can publish `ghcr.io/muxcore-media/muxcored` (see `core/.github/workflows/release.yml`; pushing workflow files needs `workflow` scope on `gh`).

Until `write:packages` is available, the LAN registry is the supported non-dev path.

## Playback product decision

**End state:** `media-ui-app` replaces Jellyfin web for browse **and** play (see workspace `MASTER-ROADMAP.md`).

**Near-term:** after install, configure the `jellyfin` bridge so households can hand off playback to Jellyfin. Its background Jellyfin→MuxCore userdata sync (`USERDATA_SYNC`) is unsupported with the authenticated userdata-local transport (ADR-0033) and is disabled in every MVP launcher; see [`USERDATA-CLIENTS.md`](USERDATA-CLIENTS.md). That handoff must not be treated as the final UI architecture.
