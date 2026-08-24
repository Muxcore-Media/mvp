# Release builds (Forgejo CI / registry images)

Monorepo development uses `replace => ../sibling` in `_mvp/go.mod` and many module `go.mod` files. **Release branches and OCI images must not.**

## Dev overlay (keep)

| File | Role |
|------|------|
| `_mvp/go.mod` | Central replace overlay for local `./run-host.sh` |
| `core/go.mod` | `replace core/pkg/contracts => ./pkg/contracts` |
| `core/pkg/contracts/go.mod` | Sibling replaces for contract repos |

## Release branch checklist

1. Run `./scripts/bump-core-pins.sh vX.Y.Z` on the release train.
2. Strip all `replace` directives except none — each module `go.mod` uses tagged requires only.
3. Contract repos tagged first (`contracts-* v0.1.x`), then `core vX.Y.Z`, then module tags.
4. `./scripts/publish-module-images.sh vX.Y.Z` — module list from `household-manifest.yaml`.
5. `./scripts/check-household-manifest.sh` — registry compose ↔ manifest parity.

## Forgejo runner without monorepo layout

CI jobs that build a **single module repo** must:

- `go mod download` against tagged deps (no sibling paths).
- Use `GOPRIVATE=github.com/Muxcore-Media/*` and Forgejo token for private modules.
- For contract stubs: `require github.com/Muxcore-Media/contracts-scanner v0.1.0` (not implementer module paths).

## Anti-patterns

- Copying `admin-ui`'s 20 sibling replaces into release branches.
- Importing `media-scanner/proto/scannerv1` — use `contracts-scanner/muxcore/scanner/v1`.
- Publishing images without updating `household-manifest.yaml` + `docker-compose.registry.yml`.

See also: `household-manifest.yaml`, `docs/INFRA-BACKENDS.md`.
