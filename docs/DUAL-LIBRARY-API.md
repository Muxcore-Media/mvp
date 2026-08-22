# Dual library API (consumer vs admin)

MuxCore exposes two gRPC surfaces for library metadata. Both are intentional today; long-term convergence behind `contracts-media-admin` read paths in `mediauiprox` is optional.

## Consumer path (`mgmntv1`)

| Piece | Location |
|-------|----------|
| Proto | `media-movies` / `media-tvshows` local `proto/mgmntv1` (and related mgmt RPCs) |
| BFF | `_mvp/cmd/mediauiprox` — REST for `media-ui-app`, mobile/TV clients |
| Audience | Household browse, home rows, detail pages, playback resolve inputs |

**Rules for consumer clients**

- Call **mediauiprox** HTTP only (`/api/movies`, `/api/tv`, …).
- Do not dial module gRPC ports from SPAs or native apps.
- Normalize responses in the BFF/data layer; components stay backend-agnostic.

## Admin path (`mediaadminv1` / `contracts-media-admin`)

| Piece | Location |
|-------|----------|
| Contract | `github.com/Muxcore-Media/contracts-media-admin` |
| Server | `admin-ui` handlers, `muxcorectl` parity commands |
| Audience | Operator CRUD, history, missing, tags, calendar, queue triage |

**Rules for admin tools**

- Prefer `contracts-media-admin` gRPC via discovery (`media.admin` capability).
- `admin-ui` may use sibling `replace => ../module` overrides in monorepo dev; published CI uses tagged modules.

## When to use which

| Need | API |
|------|-----|
| Poster / title / browse shelf for end users | Consumer BFF → `mgmntv1` backends |
| Add movie, delete files, import history, operator calendar | Admin → `mediaadminv1` |
| Automation dispatch / queue | `media-automation` (`automationv1`) — separate from both |

## Convergence (optional, not required for MVP)

1. Extend `contracts-media-admin` with read-only list/get RPCs that mirror consumer needs.
2. Teach `mediauiprox` to call `mediaadminv1` for shared read paths when consumer modules are offline.
3. Keep write/admin-only RPCs on the admin contract to avoid widening consumer attack surface.

Until that lands, **do not** assume one proto replaces the other — both are production surfaces.

## Related

- [`MASTER-ROADMAP.md`](../MASTER-ROADMAP.md) §1 platform engineering
- [`AGENT-MODULE-AUDIT-2026-08-21.md`](../AGENT-MODULE-AUDIT-2026-08-21.md) — audit item #17
