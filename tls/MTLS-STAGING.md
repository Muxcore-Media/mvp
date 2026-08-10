# mTLS staging cutover (dev → staging)

Goal: boot the host stack **without** `MUXCORE_INSECURE_DISABLE_TLS=true`, using core’s auto-issued module certs (see [Module TLS Authentication](../../core.wiki/Module-TLS-Authentication.md)).

## Checklist

1. **Packaging gate** — sidecars pin `core@v0.5.0` (no `replace => ../core*`) so CA/bootstrap APIs match.
2. **CA dir** — `mkdir -p mvp/tls/ca-mesh && chmod 700 mvp/tls/ca-mesh`. With `grpc.mtls_enabled: true` and **no** `cert_file`/`key_file`, core auto-creates `ca.crt`/`ca.key` and issues `tls/ca-mesh/server/module.{crt,key}` for both gRPC and HTTP (core ≥ this cutover fix).
3. **Config** — use [`mvp/muxcore.staging.json`](../muxcore.staging.json) (`mtls_enabled: true`). `run-host-staging.sh` exports absolute `MUXCORE_GRPC_CA_CERT_DIR`.
4. **Runner** — start with staging profile:
   ```bash
   cd mvp
   ./run-host-staging.sh up
   ```
   This wrapper **does not** export `MUXCORE_INSECURE_DISABLE_TLS`. Core alone should reach `gRPC TLS enabled … auto_ca=true` and `API server TLS enabled`.
5. **Sidecar spawn** — After core creates the CA:
   ```bash
   ./scripts/issue-staging-module-certs.sh   # openssl client certs under tls/module-certs/<id>/
   ./run-host-staging.sh up                  # start_one injects MUXCORE_TLS_* when certs exist
   ```
   Prefer core-managed `--tag` spawn when available (auto-issue + `--muxcore-tls-ca`). Manual peers need SDK ≥ this cutover (mTLS dial) rebuilt against core.
6. **Verify** — `grpcurl` / admin discovery lists modules; `curl -sk https://127.0.0.1:8080/health`; `tr '\0' '\n' < /proc/<pid>/environ | grep INSECURE` shows nothing for staging processes.
7. **Keep insecure for unit tests only** — CI and local `go test` may still set the flag; never on staging/prod hosts.

## Rollback

```bash
./run-host.sh stop
MUXCORE_CONFIG="$PWD/muxcore.json" ./run-host.sh up
```
