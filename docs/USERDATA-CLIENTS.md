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
| media-ui BFF userdata blob proxy | `cmd/mediauiprox/userdata.go`, `userdata_migrate.go` | same client, canonical `GET\|PUT /api/userdata` (the old `/userdata` path never existed on the provider, so the BFF always used its local store before S9b; see [Data migration](#data-migration-pre-upgrade-local-userdata)). Not used in `TENANT_MODE=1` ([limitation](#tenant_mode-limitation)) | **Migrated (S9b)** |
| admin-ui policy form, trusted migration, PIN blob | admin-ui `handler/parental_provider.go`, `handler/userdata_parental.go`, `main.go` (`ADMIN_UI_USERDATA_URL`) | `httpclient` with the `admin-ui` identity | S9c (admin-ui); compose/host pass `https://` in household |
| admin-ui parental seed helper (smoke) | admin-ui S9c: `/app/module parental-seed --bearer-file - --user <id>` | runs inside the admin-ui container with its own identity; invoked by `smoke.sh` via `SMOKE_PARENTAL_SEED_CMD` | **Wired** (see [Smoke policy seed](#smoke-policy-seed)) |
| userdata-local readiness probe | userdata-local `cmd/userdata-health`; MVP images `/app/userdata-health` (`dockerfiles/module*.Dockerfile`, `scripts/build-module-binaries.sh`); host `bin/userdata-health` | own `userdata-local` identity, loopback | **Wired (S9d)**: compose healthchecks, Helm exec probe, `./run-host.sh status` |
| health-monitor | health-monitor module | admitted for `/health`, but health-monitor has **no** HTTP probe client for userdata-local | Not configured; unsupported until health-monitor ships a verified client |
| Jellyfin bridge background sync (`USERDATA_SYNC`) | jellyfin `v0.3.5` `internal/userdata_sync.go`, `internal/module.go` | `PUT /userdata` with `X-User-ID`, no user bearer, shared external HTTP client | **Unsupported, disabled** (below) |
| jellyfin module's own sample compose | jellyfin `deploy/docker-compose.yml` (`USERDATA_LOCAL_URL` default empty) | — | Outside this repo; leave empty |
| `smoke.sh` / `scripts/lib/parental-smoke.sh` | this repo | dev: plaintext curl; household: admin-ui helper only | **Updated (S9d)** |
| `smoke.sh` restricted-member journey (`scripts/lib/parental-journey.sh`) | this repo | not a direct client: writes the member's policies through admin-ui's form (`POST /users/{id}/parental`, admin-ui identity), its own blob through the BFF's `/api/userdata`, and checks enforcement through the BFF; pauses the userdata-local container once to prove the BFF fails closed (unpaused by a trap and a preflight) | **Added (T-M4-01)**, household registry only |

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
| `run-host.sh` secure host modes (`MUXCORE_PROFILE=staging`, `MUXCORE_REQUIRE_TLS=1`, or no explicit insecure flag) | **Unsupported.** `up`/`restart` refuse to start userdata-local and media-ui with an ADR-0033 message (`scripts/lib/userdata-transport.sh`) | — | — |
| Helm | not wired to media-ui; `media.enabled` renders only in insecure dev | — | exec `/app/userdata-health` |
| Kustomize overlays | no userdata-local | — | — |

Why secure host modes are unsupported: `scripts/issue-staging-module-certs.sh`
does not issue userdata-local, media-ui or admin-ui certificates by default, and
the certificates it issues carry only `localhost`/`127.0.0.1`/`::1` SANs, so the
checked client fails (`x509: certificate is valid for localhost, not
userdata-local`) and so would the provider's own identity check. Rather than
fail at runtime, the runner refuses; set `MVP_ENABLE_USERDATA_LOCAL=0
MVP_ENABLE_MEDIA_UI=0` to run the rest of a secure host platform. admin-ui's
userdata pages then fail closed. Supporting it needs enrollment-equivalent host
certificates (CN = module ID, SAN `userdata-local`, client+server EKU).

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
  restricted policy or any error). admin-ui's S9c helper is
  `/app/module parental-seed --bearer-file - --user <id>` in the admin-ui
  container; for the registry stack:

  ```bash
  SMOKE_PARENTAL_SEED_CMD='docker exec -i muxcore-mvp-registry-admin-ui-1 /app/module parental-seed --bearer-file - --user' ./smoke.sh
  ```

  (the container name follows the compose project; `docker compose -f
  docker-compose.registry.yml ps -q admin-ui` finds it). Without
  `SMOKE_PARENTAL_SEED_CMD` the household seed step fails with that example; an
  empty/whitespace-only value or a command that is not found is refused before
  anything runs. It never falls back to curl, `-k`, plaintext or the BFF
  identity.

After the seed and the unconfigured-account check, household registry runs add
the restricted-member journey (`scripts/lib/parental-journey.sh`, README smoke
step 12b): the member `smoke-kid` gets a restricted policy through admin-ui's
provider-backed form (an admin-ui session with the CSRF double-submit, current
revision, read back), so the S9c admin-ui → userdata-local mTLS write path is
exercised end to end; the provider itself is never called by the smoke.

## Data migration (pre-upgrade local userdata)

Before S9b the BFF never reached the provider's blob route, so every user's
continue-watching, favorites, prefs, playlists and queue live only in the BFF's
local store (`MEDIA_UI_USERDATA_DIR/<user>.json`). The BFF migrates them lazily,
once per user, on the first provider operation for that user (GET or PUT):

- Provider collections (progress, favorites, playlists, queue) empty and local
  data present: the local blob is PUT with that user's bearer through the
  checked client, then a marker `MEDIA_UI_USERDATA_DIR/.provider-migration/<sha256>.json`
  (0600, written atomically) records `migrated` — only after a confirmed 200.
  Local prefs replace the provider's only while those are still
  userdata-local's defaults; prefs the provider already customized (e.g. an
  admin-ui PIN) are kept.
- Provider already holds collections: the provider copy is served, the local
  file is never modified or deleted, one log line (no blob content) and a
  `provider_kept` marker are written.
- Nothing local: marker `nothing_to_migrate`.
- Migration PUT fails: no `migrated` marker; the local copy is served (and
  receives that user's writes) and the next request retries. An `attempted`
  intent record makes a retry after an uncertain outcome re-send the full local
  blob (the provider's merge is idempotent for keyed entries).
- Concurrent first requests for a user are serialized; exactly one migrates.
- Bearer-less sessions (Quick Connect, auth-less dev) and `TENANT_MODE=1` keep
  using the local store only and never migrate.

Operator notes: keep `MEDIA_UI_USERDATA_DIR` (the `media-ui-data` volume) across
the upgrade and do not delete it afterwards — it is the migration source and,
for users with `provider_kept`, the only copy of their pre-upgrade data. Users
who never sign in keep their data local until they do. To re-run a user's
migration (e.g. after restoring an empty provider), delete that user's marker.
The migration is never a parental-policy path.

Provider follow-up (userdata-local, not changed here): a v0.1.6 HTTP PUT that
omits `prefs`, `playlists` or `queue` resets them (ParseBlob fills defaults,
MergeBlob replaces). The SPA, muxcore-ios and media-tvos-app send full blobs,
and the migration always re-sends the provider's prefs, but any partial-PUT
client would reset those fields.

## TENANT_MODE limitation

userdata-local v0.1.6 keys blobs by user ID only and its HTTP admin check
ignores tenants. Through the provider, a tenant-A admin overriding the target
(`X-MuxCore-User-Id`) could read or write a tenant-B user's blob. With
`TENANT_MODE=1` the BFF therefore never uses the provider for blobs: it keeps the
tenant-scoped local store (`tenants/<tenant>/<user>.json`), as before S9b, and
does not migrate. Parental policy is unaffected (its provider route verifies the
tenant). The BFF only ever sends another user's ID as `X-MuxCore-User-Id` when
the session is privileged and tenant mode is off. Tenant-aware provider blobs
are a userdata-local follow-up.

## Fallback classes (blobs)

Only a provider **outage** falls back to the local store: transport/TLS
failure, module-admission denial, refused redirect, unreadable response, HTTP
5xx, 408 or 429. A provider 4xx is the client's answer, without the provider's
body and with nothing written locally: 401 → `401 userdata.session_invalid`,
403 → `403 userdata.forbidden` (e.g. a manager override of another user), 413 →
`413 userdata.too_large`, 400 → `400 userdata.invalid`, any other 4xx → same
status, `userdata.provider_rejected`.

## Rollout

The household switch is coordinated: a v0.1.6 provider rejects the previous
plaintext BFF/admin-ui, and the S9d compose expects the new probe in the
provider image. Deploy userdata-local v0.1.6 images (with `/app/userdata-health`),
the S9b BFF and the S9c admin-ui together, then this compose. Keep the
`media-ui-data` volume: the BFF migrates each user's pre-upgrade local userdata
on first use (above). A rollback to a plaintext household provider is a
rollback, not a secure configuration; after a rollback the BFF again serves
its local store, which does not contain writes made through the provider in the
meantime.
