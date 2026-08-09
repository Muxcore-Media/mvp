# mTLS staging cutover (dev → staging)

Goal: boot the host stack **without** `MUXCORE_INSECURE_DISABLE_TLS=true`, using core’s auto-issued module certs (see [Module TLS Authentication](../../core.wiki/Module-TLS-Authentication.md)).

## Checklist

1. **Packaging gate** — sidecars pin `core@v0.5.0` (no `replace => ../core*`) so CA/bootstrap APIs match.
2. **CA dir** — `mkdir -p mvp/tls/ca-mesh && chmod 700 mvp/tls/ca-mesh`. Core creates `ca.crt` / `ca.key` on first start when `grpc.mtls_enabled` is true.
3. **Config** — use [`mvp/muxcore.staging.json`](../muxcore.staging.json) (`mtls_enabled: true`).
4. **Runner** — start with staging profile:
   ```bash
   cd mvp
   MUXCORE_CONFIG="$PWD/muxcore.staging.json" \
   MUXCORE_PROFILE=staging \
   ./run-host-staging.sh up
   ```
   This wrapper **does not** export `MUXCORE_INSECURE_DISABLE_TLS`.
5. **Sidecar spawn** — prefer core-managed `--tag` spawn (auto-issue certs), **or** issue bootstrap tokens and call `BootstrapRegister` for external binaries.
6. **Verify** — `grpcurl` / admin discovery lists modules; `/status` healthy; no process env contains `MUXCORE_INSECURE_DISABLE_TLS=true`.
7. **Keep insecure for unit tests only** — CI and local `go test` may still set the flag; never on staging/prod hosts.

## Rollback

```bash
./run-host.sh stop
MUXCORE_CONFIG="$PWD/muxcore.json" ./run-host.sh up
```
