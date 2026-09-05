# Kubernetes deploy scaffolds

Phase 3 starting point — **Helm chart / Kustomize production overlays** aligned with [`docker-compose.registry.yml`](../docker-compose.registry.yml) and [`household-manifest.yaml`](../household-manifest.yaml).

These manifests mirror the **minimal platform** slice (core + api-rest + auth-local + sqlite + secrets + encryption + call/publish policy + health-monitor + admin-ui). Enable `media.enabled` for the household media stack.

## Prerequisites

- Images published to **Forgejo or LAN OCI** (`MUXCORE_REGISTRY`, default `git.zem.systems/muxcore`) — see [`docs/PUBLIC-INSTALL.md`](../docs/PUBLIC-INSTALL.md).
- `coreTag` / image strings in `helm/muxcore/values.yaml` track `household-manifest.yaml` `core_tag` (currently **v0.5.7**).
- Cluster with a default StorageClass for PVCs.
- Secrets created out-of-band (do not commit credentials):

```bash
kubectl -n muxcore create secret generic muxcore-auth \
  --from-literal=admin-password='change-me'
```

## Kustomize

Image strings use **Forgejo/LAN OCI** (`git.zem.systems/muxcore/*`) at `household-manifest.yaml` `core_tag` — same defaults as Helm `values.yaml`. GHCR is optional (`docker-compose.ghcr.yml`).

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

Public **GHCR** mirror (`docker-compose.ghcr.yml`) remains optional when `write:packages` exists — not the origin install path.

## Acquisition sidecars (Helm)

When `acquisition.enabled=true`, the chart deploys `indexer-torznab`, `downloader-native-torrent`, `downloader-sabnzbd`, and `downloader-debrid` as mesh sidecars (`templates/acquisition-stack.yaml`). Pair with `media.enabled=true` for acquire→library flows.

## Not in scope yet

- mTLS cert injection matching `run-host-staging.sh`
- Operator CR reconciliation (see muxcore-operator); Helm covers static sidecar Deployments

## Operator (CRDs)

Early scaffold: [`Muxcore-Media/muxcore-operator`](https://github.com/Muxcore-Media/muxcore-operator) **v0.1.0** — `MuxCorePlatform` CR reconciles muxcored + sidecar Deployments/Services. Install CRD/RBAC/manager from that repo’s `config/`; sample CR mirrors this minimal platform module set. GHCR operator image publish still P0-blocked.
