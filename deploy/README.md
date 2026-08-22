# Kubernetes deploy scaffolds

Phase 3 starting point for [MASTER-ROADMAP §4.1](../../MASTER-ROADMAP.md) — **Helm chart / Kustomize production overlays**.

These manifests mirror the **minimal platform** slice of `docker-compose.yml` (core + api-rest + auth-local + sqlite + secrets + encryption + call/publish policy + health-monitor + admin-ui). Media peers can be added as extra resources later.

## Prerequisites

- Images published to `ghcr.io/muxcore-media/*` (see P0 GHCR publish; local `run-host.sh` remains the verified path today).
- Cluster with a default StorageClass for PVCs.
- Secrets created out-of-band (do not commit credentials):

```bash
kubectl -n muxcore create secret generic muxcore-auth \
  --from-literal=admin-password='change-me'
```

## Kustomize

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

Override image tags / `insecureDisableTLS` via `--set` or a values overlay.

## Acquisition sidecars (Helm)

When `acquisition.enabled=true`, the chart deploys `indexer-torznab`, `downloader-native-torrent`, `downloader-sabnzbd`, and `downloader-debrid` as mesh sidecars (`templates/acquisition-stack.yaml`). Pair with `media.enabled=true` for acquire→library flows.

## Not in scope yet

- mTLS cert injection matching `run-host-staging.sh`
- Operator CR reconciliation (see muxcore-operator); Helm covers static sidecar Deployments

## Operator (CRDs)

Early scaffold: [`Muxcore-Media/muxcore-operator`](https://github.com/Muxcore-Media/muxcore-operator) **v0.1.0** — `MuxCorePlatform` CR reconciles muxcored + sidecar Deployments/Services. Install CRD/RBAC/manager from that repo’s `config/`; sample CR mirrors this minimal platform module set. GHCR operator image publish still P0-blocked.
