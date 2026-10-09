# userdata-local HTTP clients and transport runbook (ADR-0033)

userdata-local **v0.1.6+** serves `/api/userdata`, `/api/parental-policy` and
`/health` on one HTTP listener (`USERDATA_LOCAL_HTTP_ADDR`, `:9672` in every MVP
stack). Outside explicit insecure dev that listener is **TLS-only with a
required core-CA client certificate**, and it admits callers by the verified
certificate CN only:

| Verified module CN | `/api/parental-policy` | `/api/userdata` | `/health` |
|---|---|---|---|
| `media-ui` (BFF) | GET | GET, PUT | GET, HEAD |
| `admin-ui` | GET, PUT | GET, PUT | GET, HEAD |
| `userdata-local` (its own probe) | denied | denied | GET, HEAD |
| `health-monitor` | denied | denied | GET, HEAD |
| anything else (`jellyfin`, `muxcore`, `mvp-smoke`, …) | denied | denied | denied |

Admission is additional to user authorization: data routes still need the
signed-in user's auth-local bearer and `X-MuxCore-User-Id`. Clients verify the
provider's CN **and** SAN `userdata-local` against the core CA, whatever host
they dial (service name in compose, loopback on a host install).

## Inventory of direct clients

Source inspection on 2026-10-09 of the umbrella checkouts named below. "Direct"
means a process that opens a connection to the provider's HTTP listener.

| Client | Source | Transport | Status |
|---|---|---|---|
| media-ui BFF (`mediauiprox`) parental policy read | `cmd/mediauiprox/parental_gate.go` | published `httpclient`, own `media-ui` identity, `USERDATA_LOCAL_URL` | **Migrated (S9b)** |
| media-ui BFF userdata blob proxy | `cmd/mediauiprox/userdata.go` | same client, canonical `GET\|PUT /api/userdata` (the old `/userdata` path never existed on the provider, so the BFF always used its local store before S9b) | **Migrated (S9b)** |
| admin-ui policy form, trusted migration, PIN blob | admin-ui `handler/parental_provider.go`, `handler/userdata_parental.go`, `main.go` (`ADMIN_UI_USERDATA_URL`) | must use `httpclient` with the `admin-ui` identity | **Pending S9c.** Compose/host now pass `https://` (household); an admin-ui image without S9c cannot reach a v0.1.6 household provider |
| admin-ui parental seed helper (smoke) | S9c | runs inside admin-ui's service context; invoked by `smoke.sh` via `SMOKE_PARENTAL_SEED_CMD` | **Pending S9c** (see below) |
| userdata-local readiness probe | userdata-local `cmd/userdata-health`; MVP images `/app/userdata-health` (`dockerfiles/module*.Dockerfile`, `scripts/build-module-binaries.sh`); host `bin/userdata-health` | own `userdata-local` identity, loopback | **Wired (S9d)**: compose healthchecks, Helm exec probe, `./run-host.sh status` |
| health-monitor | health-monitor module | admitted for `/health`, but health-monitor has **no** HTTP probe client for userdata-local | Not configured; unsupported until health-monitor ships a verified client |
| Jellyfin bridge background sync (`USERDATA_SYNC`) | jellyfin `v0.3.5` `internal/userdata_sync.go`, `internal/module.go` | `PUT /userdata` with `X-User-ID`, no user bearer, shared external HTTP client | **Unsupported, disabled** (below) |
| jellyfin module's own sample compose | jellyfin `deploy/docker-compose.yml` (`USERDATA_LOCAL_URL` default empty) | — | Outside this repo; leave empty |
| `smoke.sh` / `scripts/lib/parental-smoke.sh` | this repo | dev: plaintext curl; household: admin-ui helper only | **Updated (S9d)** |

Not direct clients (they reach userdata through the BFF's `/api/userdata`, so
they are unaffected by the provider transport):

- media-ui-app SPA (`src/api/client.ts`, `src/lib/userdata.ts`) — relative `/api/userdata`.
- muxcore-ios (`830d14d`): `APIClient` uses the user-configured server
  (`SessionStore.serverURL()`, the BFF) and calls `/api/userdata`
  (`Sources/Services/APIClient+Extended.swift`); no reference to the provider
  port or name.
- media-tvos-app (`a424ac6`): `MuxCoreAPI.Client(baseURL:)` is the BFF
  (`/api/tv/login`, Quick Connect) and calls `/api/userdata`; no provider
  reference.
- The restore drill writes/reads userdata through the BFF.
- media-library-maintainer reads the userdata volume read-only with the public
  `store` package (files, not HTTP).

Native evidence is source inspection only: no device run was made. Quick Connect
sessions carry no auth-local bearer, so their userdata stays in the BFF's local
store (unchanged behaviour; the BFF no longer makes the doomed provider call).
Household browsers and devices never receive mesh private keys; any client not
listed here must move behind the BFF.

## Jellyfin background sync

The optional Jellyfin→MuxCore userdata sync sends blob writes without a user
bearer and userdata-local admits no `jellyfin` caller. For this train every MVP
launcher sets `USERDATA_SYNC=0` and wires no provider URL to the bridge
(`docker-compose.registry.yml`, `docker-compose.yml`, `run-host.sh`). Playback
and the BFF→bridge progress push (`USERDATA_PUSH_TO_JELLYFIN`, a different
listener) are unaffected.

Caveat: the bridge also persists `userdata_sync` / `userdata_local_url` as
durable settings in its data volume, and a persisted `true` overrides the env
value. If an operator enabled sync earlier, clear it in the bridge settings.
Even then it fails closed in household: the provider rejects plaintext and
denies the `jellyfin` CN. Re-enabling sync needs a reviewed service-operation
contract (ADR-0020) and jellyfin module work.

## Stacks

| Stack | Client origin | Provider host port | Probe |
|---|---|---|---|
| `docker-compose.registry.yml` (household) | `https://userdata-local:9672` (BFF, admin-ui) | none | `/app/userdata-health` healthcheck |
| `+ docker-compose.dev.yml` (explicit insecure dev) | `http://userdata-local:9672` | `${USERDATA_LOCAL_PORT:-9672}` (plaintext, dev only) | same probe (plaintext) |
| `docker-compose.yml` (reference, insecure dev) | `http://userdata-local:9672` | `${USERDATA_LOCAL_PORT:-9672}` | same probe (needs a userdata-local checkout >= v0.1.6) |
| `run-host.sh` dev (default) | `http://127.0.0.1:9672` | host process on `:9672` | `./run-host.sh status` runs `bin/userdata-health` |
| `run-host.sh` with `MUXCORE_REQUIRE_TLS=1` / staging | `https://127.0.0.1:9672` (identity still `userdata-local`) | host process on `:9672`, TLS-only | `status` (staging passes `tls/module-certs/userdata-local`) |
| Helm | not wired to media-ui; `media.enabled` renders only in insecure dev | — | exec `/app/userdata-health` |
| Kustomize overlays | no userdata-local | — | — |

The household enrollment needs no extra SAN allowance: the service name equals
the module ID `userdata-local`, which core always puts in the certificate
(`MUXCORE_ENROLL_DNS_NAMES: userdata-local`).

## Smoke policy seed

`smoke.sh` seeds an explicit `unrestricted` policy for its admin before any
stream step (ADR-0031). The provider only accepts policy writes from the
`admin-ui` identity, and ADR-0033 forbids borrowing another service's key or
exporting it to the smoke runner, so:

- **Explicit insecure dev** (run-host default, the dev override, the reference
  compose): unchanged — plaintext `GET/PUT {SMOKE_USERDATA_URL}/api/parental-policy`
  with the admin bearer (`SMOKE_USERDATA_URL`, default `http://127.0.0.1:9672`).
- **Household/staging** (`parental_smoke_userdata_tls`: `SMOKE_USERDATA_TLS=1`,
  an `https://` `SMOKE_USERDATA_URL`, a TLS registry stack, or
  `MUXCORE_PROFILE=household|staging` / `MUXCORE_REQUIRE_TLS=1` on the host):
  the smoke runs `SMOKE_PARENTAL_SEED_CMD` — the admin-ui seed helper executed
  inside admin-ui's service context — with the target user id as the last
  argument and the admin bearer as the only line on stdin (never argv; masked
  in output). The helper must leave the account with a configured unrestricted
  policy (seed when unconfigured, no write when already unrestricted, fail on a
  restricted policy or any error). Example once S9c publishes it:
  `SMOKE_PARENTAL_SEED_CMD='docker compose -f docker-compose.registry.yml exec -T admin-ui /app/<helper>'`.
  Until then the household seed step fails with a message naming S9c; it never
  falls back to curl, `-k`, plaintext or the BFF identity.

## Rollout

The household switch is coordinated: a v0.1.6 provider rejects the previous
plaintext BFF/admin-ui, and the S9d compose expects the new probe in the
provider image. Deploy userdata-local v0.1.6 images (with `/app/userdata-health`),
the S9b BFF and the S9c admin-ui together, then this compose. A rollback to a
plaintext household provider is a rollback, not a secure configuration.
