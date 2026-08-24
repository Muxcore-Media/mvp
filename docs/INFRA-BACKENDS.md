# Infrastructure backend swaps

Vault soak defaults: `database-sqlite` + `secrets-file` + `auth-local` + process-local cache. Alternate backends are implemented and env-gated but require a **stop-one + env flip** — no live migration wizard yet.

## Quick reference

| Capability | Default (vault) | Alternate | Env flag |
|------------|-----------------|-----------|----------|
| Database | `database-sqlite` | `database-postgres` | `MVP_ENABLE_DATABASE_POSTGRES=1` |
| Secrets | `secrets-file` | `secrets-vault` | `MVP_ENABLE_SECRETS_VAULT=1` |
| Auth | `auth-local` | `auth-oidc` | `MVP_ENABLE_AUTH_OIDC=1` |
| Cache | process-local | `cache-redis` | `MVP_ENABLE_CACHE_REDIS=1` |

**Rule:** only one module per capability role. Do not run sqlite + postgres simultaneously.

## Swap procedure (generic)

```bash
MVP=/mnt/fast-storage/appdata/muxcore/mvp
cd "$MVP"

# 1. Stop the stack module(s) for that capability
./run-host.sh stop-one auth-local   # example

# 2. Update .env or muxcore-test.nix env for the new backend + disable the old flag

# 3. Restart mesh
./run-host.sh restart core
./run-host.sh up

# 4. Smoke
curl -s http://127.0.0.1:9401/health   # auth
./bin/muxcorectl health status
```

## auth-local → auth-oidc

1. Configure OIDC issuer, client ID/secret in `auth-oidc` env (`AUTH_OIDC_*`).
2. Set `MVP_ENABLE_AUTH_OIDC=1`, ensure `auth-local` is **not** started.
3. Update `AUTH_HTTP_URL`, `ADMIN_UI_PUBLIC_URL`, `MEDIA_UI_PUBLIC_URL` for OIDC login redirects.
4. Re-create users via OIDC provider (no automatic user import from auth-local SQLite).

## secrets-file → secrets-vault

1. Export secrets from file backend (`SECRETS_FILE_DIR`) — manual copy to Vault paths.
2. Set `MVP_ENABLE_SECRETS_VAULT=1`, point `VAULT_ADDR` + token env.
3. Restart modules that read secrets on boot.

## database-sqlite → database-postgres

**Not automated.** Each media module holds its own SQLite file under `$DATA/`. Postgres module provides a shared gRPC DB facade but modules do not auto-migrate.

Planned path:

1. Export per-module SQLite schemas to SQL dump.
2. Import into Postgres with module-specific migration scripts.
3. Flip module env from `*_DB_PATH` sqlite files to postgres DSN via `database-postgres`.

Until migration scripts exist: **stay on sqlite** for single-host homelab.

## cache-local → cache-redis

1. Set `MVP_ENABLE_CACHE_REDIS=1` with `REDIS_ADDR`.
2. Restart core + modules using cache capability.
3. Expect cold cache (no data migration).

## Preflight (admin-ui future)

Before swap: verify target backend health, list dependent modules, confirm no duplicate capability providers in mesh registry.

## Nix / vault

Edit `nix-production/modules/muxcore-test.nix` env block; `nixos-rebuild switch --flake .#vault`. Do not edit runtime `.env` on vault without syncing Nix.
