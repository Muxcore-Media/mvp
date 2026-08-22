# MuxCore — Master Roadmap (Source of Truth)

**Scope:** Entire workspace (`/home/ender/Projects/muxcore`) — core, MVP stack, all modules, CI/GHCR, ops, docs.
**Rule:** This is the **only** living workspace notes document. Open work only — delete items when done; do not keep checked-off or historical narratives.

**How to use**
- Track open work with `[ ]` (missing) or `[~]` (partial).
- When an item is finished, **delete it** from this file (do not leave `[x]`).
- Priority: **P2** (parity stretch) → **P3** (long-term).

---

## 1. Open platform work

### Playback product decision (agents: read carefully)

**End state (do not abandon):** `media-ui-app` **replaces** Jellyfin’s consumer web client — including production playback (OSD, tracks, resume, transcoder wiring). Appendix C player/parity rows stay in scope until that is true. Do **not** treat the bridge as the permanent product UI.

**Near-term production posture (until native player is household-ready):** households may play via the **`jellyfin` bridge** (deep link / JF clients) with MuxCore owning browse, request, automation, and userdata sync — so day-1 installs work without blocking on full player parity.

| Layer | Now | Destination |
|-------|-----|-------------|
| Browse / request / userdata | `media-ui-app` | `media-ui-app` |
| Production play | Jellyfin handoff OK | **`media-ui-app` player** |
| Bridge module | Keep for JF library sync / optional clients | Optional peer, not the UI end state |

`contracts-playback` stays deferred until a second playback *backend* (e.g. Plex) is committed — that is unrelated to replacing Jellyfin *web* with `media-ui-app`.

### Install product decision (locked)

**Origin / household install** is Forgejo or LAN OCI registry: `docker-compose.registry.yml` + `publish-muxcored-local.sh` + `local-registry.sh` + [`PUBLIC-INSTALL.md`](_mvp/docs/PUBLIC-INSTALL.md) (Appendix E). Fixture smoke remains the gate (`DOWNLOADER_ENGINE=fixture`).

**Public GHCR consumer mirror** stays deferred until GitHub `write:packages` exists on the public org — not an origin install blocker (same posture as deferred `contracts-playback`).

### P0 — Distribution & CI

- [x] **Forgejo origin CI** — `.forgejo/workflows/ci.yml` on all Go modules + `media-ui-app`; install via [`_mvp/scripts/install-forgejo-ci.sh`](_mvp/scripts/install-forgejo-ci.sh); runner: [`_mvp/tls/FORGEJO-RUNNER.md`](_mvp/tls/FORGEJO-RUNNER.md) (`gitea-runner-vault`, `runs-on: native`)
- [x] **Required spool checksums** — `core` rejects `required: true` modules without `checksum` in spool tags
- [x] **admin-ui `/automation` fail-fast** — bounded page/dial/read timeouts + regression tests (`admin-ui/handler/automation_timeout_test.go`)
- [x] **Forgejo mirrors for GitHub-only org repos** — pushed via [`_mvp/scripts/mirror-github-only-to-forgejo.sh`](_mvp/scripts/mirror-github-only-to-forgejo.sh) (contracts-*, emby, plex, playback-*, media-library-maintainer, muxcore-ios, muxcore-tvos); `_mvp`→`mvp`, `_wave1`→`wave1` already synced
- [ ] **Public GHCR consumer mirror** — `docker-compose.ghcr.yml` + `publish-muxcored-ghcr.sh` when org has `write:packages` (origin install stays Forgejo/LAN)

### P1 — Platform engineering

- [~] **Drop remaining publish-path `replace => ../core`** — `downloader-native-usenet`, `indexer-torznab`, `downloader-sabnzbd`, `metadata-musicbrainz` pinned; `admin-ui` + `request-media` keep monorepo `replace` until all sibling modules and `core/pkg/tenant` publish path land

### P2 — Household product parity (Appendix A → checklist)

- [~] **Seerr-class request UX** — role permissions (`REQUEST_ALLOWED_ROLES`, viewer blocked), manager auto-approve tier, pending queue in consumer UI; full discovery polish still open
- [ ] **Library-plus acquire loops** — music, books, comics, audiobooks managers exist; full Lidarr/Readarr-grade acquire→import automation incomplete
- [~] **Debrid product path** — `POST /api/add` on `downloader-debrid` + consumer Settings → Debrid tab via BFF; optional VFS still open
- [~] **Operator daily-driver UX** — unified calendar + queue + failure triage; dashboard queue alert when failures exist (2026-08-22); Panelarr/Seerr-grade polish still open

### P2 — Consumer UI (`media-ui-app/AGENTS.md` §12)

### P2 — Native clients (documented intentional gaps)

- [ ] **`media-android`** — TOTP 2FA UI (no BFF endpoints yet) ([`media-android/AGENTS.md`](media-android/AGENTS.md))
- [ ] **`muxcore-ios`** — custom Video OSD parity, theme engine ([`muxcore-ios/README.md`](muxcore-ios/README.md))
- [ ] **`media-tvos-app`** — live validation on physical Apple TV (CI ships unsigned IPA only)

### P3 — Phase 3 / long-term

- [~] **Kubernetes production overlays** — platform + media-stack Helm/Kustomize shipped; **acquisition** sidecars via `acquisition.enabled` + `templates/acquisition-stack.yaml` (2026-08-22); operator CR reconciliation still open ([`muxcore-operator`](https://github.com/Muxcore-Media/muxcore-operator))
- [ ] **`muxcore-operator` image publish** — GHCR/Forgejo operator image + soak validation (blocked same as public GHCR today)
- [ ] **`storage-ceph` native RADOS/CephFS** — MinIO RGW stand-in today ([`storage-ceph/README.md`](storage-ceph/README.md))

(No other open P3 rows — multi-region placement, cross-cluster storage sync, distro rootfs / vsock Firecracker microVM config, and native VideoPlayer OSD + transcoder wiring shipped 2026-08-20.)

---

## 2. Maintenance rules

1. **One file** — Update this document (roadmap + appendices); do not recreate parallel root note files or status novels.
2. **Delete when done** — Prefer small atomic items; remove the line when the PR/tag lands.
3. **Smoke is sacred** — No “Deployable” claim without `mvp/smoke.sh` PASS on fixture path.
4. **Dumps are dead** — Never open feature work in polluted archive directories (Appendix H).
5. **Wiki follows code** — After material milestones, sync Getting-Started / Deployment / Security; keep wiki Roadmap as a stub to this file.

---

## Appendices

- [Appendix A: Market research — competitive gaps](#appendix-a-market-research-competitive-gaps)
- [Appendix B: Radarr / Sonarr operator parity](#appendix-b-radarr-sonarr-operator-parity)
- [Appendix C: Jellyfin web → native UI parity](#appendix-c-jellyfin-web-native-ui-parity)
- [Appendix D: Bazarr → media-subtitles parity](#appendix-d-bazarr-media-subtitles-parity)
- [Appendix E: Working constraints](#appendix-e-working-constraints)
- [Appendix F: Vault loop improvements](#appendix-f-vault-loop-improvements)
- [Appendix G: Indexer / torrent VPN gates](#appendix-g-indexer-torrent-vpn-gates)
- [Appendix H: Archived / polluted workspace dumps](#appendix-h-archived-polluted-workspace-dumps)

---

## Appendix A: Market research — competitive gaps

## MuxCore Market Research — Competitive Gaps

**Date:** 2026-08-20  
**Purpose:** Determine what MuxCore is lacking to become the perfect competitor to existing options.

**Verdict:** MuxCore’s thesis is right — replace the multi-app Arr stack with one modular platform. What’s missing isn’t more architecture; it’s the **household-ready product surface**, **Arr-grade decision quality**, and **trust/distribution** that make people leave Sonarr/Radarr/Seerr/Jellyfin.

---

### Competitive map

| Camp | Examples | What they optimize | Threat to MuxCore |
|------|----------|--------------------|-------------------|
| **Fragmented stack (incumbent)** | Sonarr, Radarr, Prowlarr, Lidarr, Bazarr + qBit/SAB + Seerr + Jellyfin/Plex | Depth per job, huge community, TRaSH/Recyclarr | Default choice; “good enough + guides” |
| **Unified control planes** | Prismarr, Panelarr, Streamarr | One UI over existing Arrs | Steal “single dashboard” narrative without replacing backends |
| **Unified automation (closest peer)** | Riven | Debrid + plugins + VFS → Plex/Jellyfin | Same “one system” pitch, debrid-first |
| **Request / invite layers** | Seerr, Wizarr | Family request + onboarding UX | Own the non-admin experience MuxCore must match |
| **MuxCore** | Loom + modules | One platform, multi-media, mesh, multi-quality | Wins only if day-2 ops feel *better*, not just different |

Dashboards are **not** your competition for “replace the stack.” **Servarr maturity + Seerr UX + Jellyfin playback** are. Riven is the nearest architectural peer.

---

### Gaps that block “perfect competitor” status

#### 1. Household product (vs Seerr + Wizarr + Jellyfin)

Homelabbers don’t leave Arr for architecture. They leave when **family can request, watch, and stay out of admin**.

Appendix C (Jellyfin web parity) shows the hole:

- No durable **continue watching / favorites / watched / resume**
- Weak **global search**, home rows, collections, playlists, queue
- Player is basic `<video>` (no mature OSD, tracks, intro skip wired to UX)
- No **invite / onboarding** (Wizarr’s job)
- Request flow exists but lacks Seerr-class **approval, permissions, discovery, watchlists**

**Must-have to compete:** Seerr-grade request/discovery + Jellyfin-grade userdata/playback *or* a rock-solid “use Jellyfin for play, MuxCore for everything else” story with zero friction.

#### 2. Arr decision quality (vs Sonarr/Radarr + TRaSH)

The automation loop for movies/TV is real and hardening. What’s still below market bar:

- **TRaSH/Recyclarr-level profile ecosystem** — community-scored custom formats people trust day one
- **Decade of release-name edge cases** (scene quirks, anime, foreign, packs) — live fixes continue; Arrs already “just work”
- **Upgrade until quality/score**, interactive search UX, blocklists, history depth operators live in
- **Usenet + torrent as equal paths** — SAB/debrid modules exist as scaffolds; not “install and forget” like Arr + qBit + SAB
- **External clients** — power users want **qBittorrent/Transmission** as peers, not only native torrent
- **Music/books/comics/audiobooks** — managers scaffolded; not a full Lidarr/Readarr-grade acquire→import loop

Without TRaSH-class scoring + battle-tested parsing, MuxCore feels like a clever beta next to Arr.

#### 3. Playback identity (vs Jellyfin / Plex / Riven)

Today MuxCore is mostly **automation + bridge**, not a complete media *experience*.

- Native UI far from Jellyfin web
- No mobile clients (Plex/JF win households here)
- No Riven-style **debrid VFS** path for “request → appear instantly”
- Transcoder / intro-outro / graph modules are early; not productized in the player

**Chosen story:**

- **Near-term:** deep Jellyfin sync/handoff so households can play today (bridge + userdata).
- **End state:** `media-ui-app` **replaces** Jellyfin web for browse **and** playback (Appendix C). Agents must keep shipping native player parity — do not freeze on “Jellyfin is forever the player.”

#### 4. Trust, distribution, migration (vs linuxserver + Servearr wiki)

This is where Arr stacks still crush newcomers:

| Need | Market standard | MuxCore today |
|------|-----------------|---------------|
| Install | `docker compose` + known images | Forgejo/LAN registry compose ready; public GHCR mirror still gated |
| Migrate | Export/API from Sonarr/Radarr | No first-class migrate story |
| Guides | TRaSH, Servearr wiki, Discord | Strong internal wiki; little public gravity |
| Version trust | Years of `v3/v4` production | Many `v0.1.x` modules; beta disclaimer correct |
| Marketplace | Arr plugins + Recyclarr | Runtime DeployTag + checksum/signature trust + `/marketplace/trust` |

**Must-have:** one public compose path, **Sonarr/Radarr import**, and “Recyclarr for MuxCore” (sync curated format packs).

#### 5. Day-2 ops polish operators feel immediately

Market pain Arr users hate (and dashboards try to paper over):

- Unified calendar / upcoming
- One queue across media types with clear stuck/warning actions
- Activity / failed-import triage
- Devices, sessions, API keys, backups as first-class admin (see Jellyfin-admin parity list)

Pieces exist (automation, events, health). The **daily-driver control center** people open 20×/day is not there yet.

---

### Priority: what to build to actually win

Ordered by competitive impact, not roadmap nostalgia:

1. **Household loop** — userdata + continue watching + resume player + Seerr-like request/approval + invite links
2. **Quality moat** — import/sync TRaSH-style format packs; upgrade-until; blocklist; interactive search parity
3. **One install + migrate** — public images, Arr DB/API import, “bring your library in an afternoon”
4. **Acquisition completeness** — qBit peer + production Usenet + production debrid (or explicitly cede debrid to Riven and own local)
5. **Playback commitment** — near-term JF handoff OK; **end state** is `media-ui-app` replacing Jellyfin web (Appendix C player milestones)
6. **Calendar + unified queue + failure UX** — beat Panelarr/Prismarr at their own game *without* needing Arr backends
7. **Library-plus automation** — only after movies/TV feel better than Arr for normal users

---

### Positioning that markets will believe

**Winning claim (when gaps close):**  
“One install replaces Sonarr + Radarr + Prowlarr + Seerr + download client — and multi-quality without dual instances — with Arr-grade picks and a family UI.”

**Losing claim (today):**  
“Modular mesh loom with 80 modules.” That’s architecture marketing; Arr users buy outcomes.

**Don’t compete with:** Homarr/Dashy (start pages).  
**Do outcompete:** Arr complexity + Seerr UX + (optionally) Jellyfin playback.  
**Watch closely:** Riven on debrid/VFS; Panelarr/Prismarr on “one screen” habits.

---

### Bottom line

MuxCore isn’t lacking “more modules.” It’s lacking the **incumbent experience contract**:

- Arr-level **release decisions** (especially TRaSH-class scoring)
- Seerr/Wizarr-level **household UX**
- Jellyfin-level **watch experience** *or* a perfect JF handoff
- linuxserver-level **trust: install, migrate, stay updated**

Close those four, and the loom architecture becomes the reason people stay. Leave them open, and unified dashboards + Arr will keep the market no matter how elegant the mesh is.
---

## Appendix B: Radarr / Sonarr operator parity

## Radarr / Sonarr → MuxCore operator parity

**Goal:** MuxCore’s media stack (`media-movies`, `media-tvshows`, `media-automation`, leaf modules) plus `admin-ui` are a full operator replacement for **Radarr** and **Sonarr**.

**Canonical modules:** `media-movies`, `media-tvshows`, `media-automation`, `media-custom-formats`, `media-rename`, `media-root-folders`, `media-scanner`, `media-list-sync`, `indexer-*`, `downloader-*`, `admin-ui`. Do not develop archived dumps.

Legend: `[~]` partial · `[ ]` missing · `N/A` not applicable · **Waiver** = accepted MuxCore-native equivalent. Delete rows when fully done.

---

### A. Shared (*Arr) operator surfaces

| *Arr surface | MuxCore target | Status |
|--------------|----------------|--------|
| Import Lists | `media-list-sync` + `/list-sync` | [x] [`LISTSYNC-PARITY.md`](./LISTSYNC-PARITY.md) |
| Full library migrate | `admin-ui` `/migrate` | [x] One-shot Radarr/Sonarr import |

---

### B. Radarr (movies) — `media-movies` + admin

| Radarr surface | MuxCore target | Status |
|----------------|----------------|--------|
| Calendar | N/A (movies) | N/A |
| Import Lists | `media-list-sync` | [x] [`LISTSYNC-PARITY.md`](./LISTSYNC-PARITY.md) |

---

### C. Sonarr (TV) — `media-tvshows` + admin

| Sonarr surface | MuxCore target | Status |
|----------------|----------------|--------|
| Import Lists | `media-list-sync` | [x] [`LISTSYNC-PARITY.md`](./LISTSYNC-PARITY.md) |

---
---

## Appendix C: Jellyfin web → native UI parity

## Jellyfin web → MuxCore native UI parity

**Goal:** `media-ui-app` replaces Jellyfin’s **non-admin** web client; `admin-ui` replaces Jellyfin’s **dashboard** web client.

**Sources:** jellyfin-web `src/apps/modern`, `src/apps/legacy` (user), `src/apps/dashboard` (admin) routes (master, 2026-08).

**Canonical modules:** `media-ui-app` (consumer), `admin-ui` (operator). Do not develop archived `media-ui` / `media-jellyfin` dumps.

Legend: `[~]` partial · `[ ]` missing · `N/A` not applicable · **Waiver** = accepted MuxCore-native equivalent. Delete rows when fully done.

---

### A. Consumer (`media-ui-app`) ↔ Jellyfin user web

**End state:** `media-ui-app` replaces Jellyfin web (including playback). **Near-term:** JF handoff is OK so installs work; that must not stop native player work.

Delete rows when companion *or* player parity is production-grade for that surface.

**Shipped (removed from open checklist):** home/next-up, global search, movies/TV browse+detail, genres/studios/collections, upcoming, music+lyrics, books library section, mixed, favorites, playlists, queue, settings/profile/prefs, quick connect, login/forgot-password path, music videos / home videos (`?library=` + path-prefix config), live TV guide/channels/recordings/timers (**Waiver** — file-backed companion; no physical tuners / EPG grabbers), **Video OSD player** (`/player` + `VideoPlayer` — custom overlay OSD, resume, tracks, skip intro, keyboard shortcuts, BFF `/api/playback/resolve` + transcoder path wiring).

---

### B. Admin (`admin-ui`) ↔ Jellyfin dashboard

**Shipped operator surfaces (removed):** dashboard, users (+ password-reset queue), devices, libraries/roots, metadata, playback→transcoder, tasks, modules/marketplace/plugins, logs (file viewer), branding, networking, API keys, backups, live TV admin (file-backed), auth/SSO.

No open Jellyfin-dashboard checklist rows for near-term ops. MuxCore-native extras (keep): Automation, Queue, Calendar, Subtitles, Jellyfin bridge, List Sync, Migrate, Invites, Formats, Profiles, Naming, Cluster, Events, Storage, Audit, Request.

---
---

## Appendix D: Bazarr → media-subtitles parity

**Done** — product parity shipped in `media-subtitles` **v0.5.5**. Canonical record: [`BAZARR-PARITY.md`](./BAZARR-PARITY.md). No open checklist rows.

---
---

## Appendix E: Working constraints

#### Constraints (non-negotiable)

| Allowed | Not allowed as task drivers |
|---------|------------------------------|
| This laptop (Go, local Docker/Podman if present) | User creating paid accounts or entering payment info |
| **Forgejo origin** (`git.zem.systems/muxcore`, vault `forgejo-runner` `native`) | Treating `.github/workflows` as origin CI (use `.forgejo/workflows` only) |
| GitHub later as **public** consumer (Releases/GHCR for homelabbers) | Treating GHCR/`write:packages` as a current origin blocker |
| Fixture / httptest / mock / offline paths | Live pirate indexers, real torrent swarms, Apibay/VPN live grabs |
| Local MinIO, Postgres, Redis, Vault/OpenBao, Keycloak, Jellyfin in Docker | Real-Debrid / AllDebrid / paid Usenet / paid TMDB as hard deps |
| Forgejo or LAN image registry | Assuming paid GHCR packages scope |

**Acquisition rule:** keep `DOWNLOADER_ENGINE=fixture` / indexer soft-empty / httptest fixtures as the gate. Do **not** add tasks that require browsing or hitting pirate websites.

**Definition of done (product):** a non-developer can install MuxCore on one machine, add movies/TV via fixture or mock acquisition, browse/play in consumer UI, manage via admin-ui, and expand into music/books/etc. without a monorepo of sibling clones.
---

## Appendix F: Vault loop improvements

**Done** — all 14 vault-loop follow-ups are implemented and covered by module tests (`media-automation`, `media-scanner`, `request-media`, `_mvp/run-host.sh`). Source: live host (vault) acquisition watch, 2026-08-18. Re-open only if a regression appears on vault.
---

## Appendix G: Indexer / torrent VPN gates

### Non-negotiable VPN wiring (live indexer / torrent)

| Knob | Required value on gringotts |
|------|-----------------------------|
| `WG_CONF` | `/home/ender/Projects/muxcore/wg-mux.conf` (or rotated peer under `mvp/secrets/`; keep mode `600`) |
| `WG_USE_WG_QUICK` | `0` |
| `WG_KILL_SWITCH` | `false` |
| Interface | Source-routed iface from conf basename (typically `wg-mux`) |
| Gateway / DNS in conf | Proton `10.2.0.1` (do not change without rotation drill) |

Docs: [`_mvp/tls/README.md`](_mvp/tls/README.md), [`_mvp/tls/SECRET-ROTATION.md`](_mvp/tls/SECRET-ROTATION.md).

**Prove VPN before any live grab** on gringotts: downloader must source-route via `WG_CONF`; confirm public egress IP is Proton, not residential/mesh egress.


---

## Appendix H: Archived / polluted workspace dumps

These directories under the muxcore workspace are **not** modules. Do not open feature work, CI PRs, or spool pins against them.

| Dump path | Canonical replacement |
|-----------|----------------------|
| `media-ui/` | [`media-ui-app`](https://github.com/Muxcore-Media/media-ui-app) |
| `media-jellyfin/` | [`jellyfin`](https://github.com/Muxcore-Media/jellyfin) |
| `cache-memory/` | [`cache-local`](https://github.com/Muxcore-Media/cache-local) (process-local) or [`cache-redis`](https://github.com/Muxcore-Media/cache-redis) |
| `custom-scripts/` | None — ignore; use real module repos |
| `muxcorectl/` | [`muxcorectl-cli`](https://github.com/Muxcore-Media/muxcorectl-cli) |

Each dump directory has `ARCHIVED.md` + a stub `README.md`.

