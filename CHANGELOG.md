# Changelog

## [Unreleased]

### Added
- `scripts/check-publish-set.sh` (+ `_test.sh`, run by `run-script-tests.sh`): fails when any image in `docker-compose.registry.yml` (all profiles) is missing from `publish-module-images.sh` `DEFAULT_MODULES` (T-M2-02, TDD section 5). `muxcored` (module `core`) is published by `publish-muxcored-local.sh`.
- `cmd/mediauiprox/routes_inventory_test.go` and a complete `BFF-API.md` "Route inventory" table: the test fails on any registered route missing from the doc or documented but not registered (T-M2-08, TDD section 7).

### Changed
- `publish-module-images.sh` default publish set now covers every compose profile: added auth-oidc, backup-local, cache-local, downloader-debrid, downloader-native-torrent, downloader-native-usenet, downloader-qbittorrent, downloader-sabnzbd, emby, indexer-piratebay, indexer-torznab, media-dlna, media-intro-outro, media-tagging, media-transcoder-pool, playback-guard, playback-monitor, plex, secrets-vault.
- `mediauiprox`: route registration extracted from `main` into `(*server).registerRoutes` (no behaviour change).
- Pin all install paths to release train `train-2026.10.1` (T-M2-04, FR-INS-006): core / image tag `v0.6.6` in `docker-compose.registry.yml` defaults, `household-manifest.yaml` `core_tag`, Helm `coreTag` + image strings, kustomize base/overlay images, publish/smoke scripts and install docs.
- `scripts/bump-core-pins.sh` now also updates Helm values, kustomize image pins, `local-registry.sh` and the doc pin text, so one run keeps every pin in agreement.

### Fixed
- `mediauiprox` panicked at startup on two `net/http` ServeMux pattern conflicts: `/api/tv/login` (now `POST /api/tv/login`) vs `PATCH /api/tv/{id}`, and `GET /api/music/tracks/` (now `GET /api/music/tracks/{id}/lyrics`) vs `GET /api/music/{id}/history`.

