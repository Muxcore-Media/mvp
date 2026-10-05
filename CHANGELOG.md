# Changelog

## [Unreleased]

### Changed
- Pin all install paths to release train `train-2026.10.1` (T-M2-04, FR-INS-006): core / image tag `v0.6.6` in `docker-compose.registry.yml` defaults, `household-manifest.yaml` `core_tag`, Helm `coreTag` + image strings, kustomize base/overlay images, publish/smoke scripts and install docs.
- `scripts/bump-core-pins.sh` now also updates Helm values, kustomize image pins, `local-registry.sh` and the doc pin text, so one run keeps every pin in agreement.
