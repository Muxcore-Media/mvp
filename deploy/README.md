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
```

## Helm

```bash
helm upgrade --install muxcore deploy/helm/muxcore \
  --namespace muxcore --create-namespace \
  -f deploy/helm/muxcore/values.yaml
```

Override image tags / `insecureDisableTLS` via `--set` or a values overlay.

## Not in scope yet

- Full media stack (movies/TV/automation/scanner/…)
- Operator / CRDs (separate §4.1 item)
- mTLS cert injection matching `run-host-staging.sh`
