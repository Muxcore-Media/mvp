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
3. Update `AUTH_HTTP_URL`, `ADMIN_UI_PUBLIC_URL`, `MEDIA_UI_PUBLIC_URL` for OIDC login redirects. Configure the BFF `AUTH_GRPC_CLIENT_ADDR` to that provider's gRPC endpoint as well: linked BFF sessions require compatible `Validate` semantics on each protected request (see [`BFF-API.md`](../BFF-API.md#auth-session)); unsupported/unavailable validation returns retryable 503 without deleting the session.
4. Re-create users via OIDC provider (no automatic user import from auth-local SQLite).

## secrets-file → secrets-vault

Both modules implement the same `secrets` capability. Core does not reject two secrets providers, so select exactly one at deployment time. Migration of secret values is manual: read them through the file provider and write them to the external backend's configured namespace. Keep the encrypted file store and its master key for rollback; there is no plaintext export directory.

**Compose:** the default remains `secrets-file`. Set `SECRETS_BACKEND` (`vault`, `infisical`, `aws`, `gcp` or `azure`) and that backend's provider variables in `.env`. Keep the normal enrollment generation, which produces tokens for both providers: base-file variables interpolate before the overlay removes a service.

```bash
./scripts/gen-enrollment.sh
docker compose -f docker-compose.registry.yml -f docker-compose.secrets-vault.yml config -q
docker compose -f docker-compose.registry.yml stop secrets-file
docker compose -f docker-compose.registry.yml -f docker-compose.secrets-vault.yml up -d
```

Use `docker-compose.yml` instead of the registry base for source builds. For dev, order files as **registry → dev → secrets-vault**. The selection overlay must be last: it removes the file service with `!reset null` and enables Vault. Requires Compose `!reset` support; tested with Docker Compose v2.40.3. Earlier versions and Podman Compose are unverified. Run the `config -q` check before startup; parsing failure is a failure, not a skipped check.

Raw `--profile secrets-vault` (including `--profile '*'` without the selection overlay) is unsupported: profiles are additive and select both providers. The overlay selects only Vault even with wildcard profiles. Removing a service from the model does not stop an existing container, so stop the old provider explicitly before switching. Do not use `down -v` or delete either identity/data volume.

Rollback: stop Vault using the same file set, then start the default file provider from the base file. Retain the original `SECRETS_MASTER_KEY`, file data and both providers' mesh identity volumes. Restart modules that cache secrets or provider connections after either switch. Vault credentials and connectivity are validated by the provider at runtime; successful compose rendering does not establish backend readiness.

**Host:** run `./run-host.sh stop-one secrets-file`, set `MVP_ENABLE_SECRETS_VAULT=1` plus the backend variables, then `./run-host.sh up`. The flag now selects Vault instead of file. Missing `SECRETS_BACKEND`, a live opposing runner process, or a restart request for the unselected provider fails before stop/start actions. To roll back, stop Vault, set the flag to `0`, and start the stack. An unrelated single-module restart does not change provider selection.

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
