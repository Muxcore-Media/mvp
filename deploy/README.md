# Kubernetes deploy scaffolds

Phase 3 starting point — **Helm chart / Kustomize production overlays** aligned with [`docker-compose.registry.yml`](../docker-compose.registry.yml) and [`household-manifest.yaml`](../household-manifest.yaml).

These manifests mirror the **minimal platform** slice (core + api-rest + auth-local + sqlite + secrets + encryption + call/publish policy + health-monitor + admin-ui). Enable `media.enabled` for the household media stack.

## Prerequisites

- Images published to a **LAN OCI registry** (`MUXCORE_REGISTRY`, default `localhost:5000/muxcore`; see [`local-registry.sh`](../local-registry.sh)) or, once published, GHCR (`ghcr.io/muxcore-media`) — see [`docs/PUBLIC-INSTALL.md`](../docs/PUBLIC-INSTALL.md). Cluster nodes must be able to pull from that registry; override the prefix when `localhost:5000` is not reachable from the nodes.
- `coreTag` / image strings in `helm/muxcore/values.yaml` track `household-manifest.yaml` `core_tag` (currently **v0.6.15**).
- Cluster with a default StorageClass for PVCs.
- Secrets created out-of-band (do not commit credentials):

```bash
kubectl -n muxcore create secret generic muxcore-auth \
  --from-literal=admin-password="$(openssl rand -hex 16)"
```

- **health-monitor bearer token** (NFR-SEC-011). health-monitor (v0.1.7+) binds `0.0.0.0:9203` here and refuses to start on a non-loopback address without `HEALTH_MONITOR_HTTP_TOKEN`; admin-ui reads the same value as `ADMIN_UI_HEALTH_MONITOR_TOKEN`. Neither the chart nor the overlays ship a default, so a deploy without a token fails at render time:
  - **Kustomize**: create the git-ignored env file next to the base before applying (`kubectl kustomize` fails with a missing-file error otherwise; the generated Secret is `health-monitor-token-<hash>`, refs are rewritten automatically):
    ```bash
    echo "HEALTH_MONITOR_HTTP_TOKEN=$(openssl rand -hex 32)" > deploy/kustomize/base/health-monitor.env
    ```
    See `health-monitor.env.example`.
  - **Helm**: set exactly one of `healthMonitor.token` (the chart renders Secret `health-monitor-token`) or `healthMonitor.existingSecret` (a Secret you created; key `healthMonitor.secretKey`, default `token`). Rendering fails with `required` if both are empty.
    ```bash
    kubectl -n muxcore create secret generic health-monitor-token --from-literal=token="$(openssl rand -hex 32)"
    helm upgrade --install muxcore deploy/helm/muxcore -n muxcore --set healthMonitor.existingSecret=health-monitor-token
    ```
  - No scheduler-cron module is deployed from this directory, so `SCHEDULER_HTTP_TOKEN` does not apply here.

## Kustomize

Image strings use the **LAN OCI registry** (`localhost:5000/muxcore/*`) at `household-manifest.yaml` `core_tag` — same defaults as Helm `values.yaml`. For GHCR, override with a Kustomize `images:` entry pointing at `ghcr.io/muxcore-media/*`.

```bash
# Dev (insecure TLS flag for mesh bring-up)
kubectl apply -k deploy/kustomize/overlays/dev

# Production-shaped (TLS insecure flag off; tighten images/resources)
kubectl apply -k deploy/kustomize/overlays/production

# Full media MVP stack (platform + movies/TV/request/automation/scanner + consumer UI)
kubectl apply -k deploy/kustomize/overlays/media-stack
```

## Helm

```bash
helm upgrade --install muxcore deploy/helm/muxcore \
  --namespace muxcore --create-namespace \
  -f deploy/helm/muxcore/values.yaml

# Enable media stack peers
helm upgrade --install muxcore deploy/helm/muxcore \
  --namespace muxcore --set media.enabled=true

# Optional acquisition sidecars (with media stack)
helm upgrade --install muxcore deploy/helm/muxcore \
  --namespace muxcore --set media.enabled=true --set acquisition.enabled=true
```

Override `registry`, `coreTag`, or individual `images.*` strings via `--set` or a values overlay.

**GHCR** (`registry=ghcr.io/muxcore-media`) is optional and blocked until images are published there (needs a `write:packages` token; [ADR-0007](https://github.com/Muxcore-Media/umbrella/blob/main/docs/adr/0007-install-paths-github-only.md)).

## Acquisition sidecars (Helm)

When `acquisition.enabled=true`, the chart deploys `indexer-torznab`, `downloader-native-torrent`, `downloader-sabnzbd`, and `downloader-debrid` as mesh sidecars (`templates/acquisition-stack.yaml`). Pair with `media.enabled=true` for acquire→library flows.

## userdata-local transport (ADR-0033 S9) — unsupported here

userdata-local >= v0.1.6 serves its HTTP API (`/api/userdata`, `/api/parental-policy`, `/health`) over mTLS only outside explicit insecure dev, admits only verified module certificates (media-ui, admin-ui; health for userdata-local/health-monitor) and is probed with its packaged `userdata-health` executable. These manifests provision no mesh identities or core CA, so:

- **Helm**: `media.enabled=true` renders only with `insecureDisableTLS=true` (explicit insecure dev). With TLS on, rendering fails with an ADR-0033 message instead of claiming readiness. The userdata-local readiness probe is the packaged `/app/userdata-health` (exec), not a bare HTTP probe, and the pod binds `USERDATA_LOCAL_HTTP_ADDR=:9672` to match its declared port. media-ui is not wired to userdata-local in this chart (no `USERDATA_LOCAL_URL`), so signed-in users get `503 parental.policy_unavailable` on gated routes.
- **Kustomize**: the overlays deploy no userdata-local and no `USERDATA_LOCAL_URL`; the same 503 applies. Unsupported for S9.

Use `docker-compose.registry.yml` (household) for a supported install; see [`docs/USERDATA-CLIENTS.md`](../docs/USERDATA-CLIENTS.md).

## Not in scope yet

- mTLS cert injection matching `run-host-staging.sh`
- Operator CR reconciliation (see muxcore-operator); Helm covers static sidecar Deployments

## Operator (CRDs)

Early scaffold: [`Muxcore-Media/muxcore-operator`](https://github.com/Muxcore-Media/muxcore-operator) **v0.1.0** — `MuxCorePlatform` CR reconciles muxcored + sidecar Deployments/Services. Install CRD/RBAC/manager from that repo’s `config/`; sample CR mirrors this minimal platform module set. GHCR operator image publish still P0-blocked.

## Media confinement

The scanner gets `SCANNER_ALLOWED_ROOTS` (`media.scannerAllowedRoots` in Helm); match these to your mounted library and download paths. Helm's native torrent sidecar exposes `acquisition.downloaderIndexerHosts`, empty by default (public targets only), for explicitly trusted LAN indexer hosts.

These scaffolds do not deploy media-transcoder, media-root-folders, media-rename, media-subtitles or qBittorrent. The BFF therefore disables transcoding with an empty `TRANSCODER_HTTP_URL`; adding an external transcoder requires its HTTPS URL and the BFF's mesh certificate/CA, or a shared dev token for plaintext. Complete media persistence and household mesh enrollment remain prerequisites for a production Kubernetes deployment. The compose and host launchers wire the guards for their deployed modules to actual media mounts.
