# Default module gRPC / HTTP ports

Host `run-host.sh` sets explicit `*_GRPC_ADDR` / `*_HTTP_ADDR` env vars. Module code defaults below must stay unique so a bare `go run` / compose without remaps does not collide.

| Port | Module | Notes |
|------|--------|-------|
| 9101 | call-policy-default | |
| 9102 | publish-policy-default | |
| 9200 | scheduler-cron | default HTTP (`SCHEDULER_HTTP_ADDR`; compose / systemd) |
| 9204 | scheduler-cron | `_mvp/run-host.sh` when `MVP_ENABLE_SCHEDULER_CRON=1` (`127.0.0.1:9204`) |
| 9202 / 9203 | health-monitor gRPC / HTTP | |
| 9300 | worker-pool-memory HTTP | optional (`MVP_ENABLE_WORKER_POOL=1`; default `127.0.0.1:9300`) |
| 9302 / 9303 | backup-local gRPC / health | optional (`MVP_ENABLE_BACKUP_LOCAL=1` / compose `backup-local`) |
| 9380 / 9481 | request-media HTTP / gRPC | |
| 9400 | api-rest gRPC | |
| 9401 | auth-local HTTP | |
| 9402 / 9404 | feature-flags-file gRPC / HTTP | |
| 9403 | auth-local gRPC | |
| 9410 / 9412 | auth-oidc gRPC / HTTP | |
| 9411 | metadata-tmdb | gRPC |
| 9413 | metadata-musicbrainz | gRPC |
| 9420 / 9430 | media-movies gRPC / HTTP | |
| 9440 / 9450 | media-tvshows gRPC / HTTP | |
| 9441 | notification-default | |
| 9445 | notification-apprise | optional (`MVP_ENABLE_NOTIFICATION_APPRISE=1` / compose `apprise`) |
| 9460 | media-automation | |
| 9461 | downloader-native-torrent | native torrent engine (gRPC) |
| 9462 / 9463 | downloader-qbittorrent | optional qBittorrent WebUI bridge (gRPC / health) |
| 9470 | media-scanner | |
| 9475 / 8475 | jellyfin gRPC / HTTP | |
| 9476 / 8476 | plex | optional playback bridge (gRPC / HTTP) |
| 9477 / 8477 | emby | optional playback bridge (gRPC / HTTP) |
| 9480 | media-ffprobe | |
| 9485 | indexer-piratebay | optional (`acquisition` profile) |
| 9486 | indexer-torznab | optional |
| 9490 | media-custom-formats | |
| 9510 | media-rename | |
| 9520 / 9521 | media-subtitles gRPC / HTTP | |
| 9525 / 9526 | media-transcoder gRPC / playback HTTP | mediauiprox uses `:9526` for transcode |
| 9530 | media-list-sync | optional (`MVP_ENABLE_MEDIA_LIST_SYNC=1` / compose `list-sync`) |
| 9540 | media-root-folders | |
| 9545 | media-library-maintainer | optional (`MVP_ENABLE_MEDIA_LIBRARY_MAINTAINER=1` / compose `library-maintainer`) |
| 9550 / 9551 | secrets-file / secrets-vault | gRPC |
| 9560 / 8560 | playback-monitor gRPC / HTTP | |
| 9561 | playback-guard | gRPC |
| 9600 | cache-redis | optional (`MVP_ENABLE_CACHE_REDIS=1` or `REDIS_ADDR`) |
| 9601 | encryption-aesgcm | |
| 9602 | cache-local | |
| 9603 | workflow-tapestry | optional (`MVP_ENABLE_WORKFLOW_TAPESTRY=1`) |
| 9604 | distributed-lock-sqlite | |
| 9605 | executor-shell | optional (`MVP_ENABLE_EXECUTOR_SHELL=1`) |
| 9610 / 9611 | storage-s3 | optional S3/MinIO StorageProvider (gRPC / health) |
| 9613 | tracing-otlp | optional (`MVP_ENABLE_TRACING_OTLP=1`) |
| 9614 | config-watcher | optional (`MVP_ENABLE_CONFIG_WATCHER=1`) |
| 9620 / 9621 | downloader-sabnzbd | optional SABnzbd usenet bridge (gRPC / health) |
| 9622 / 9623 | downloader-native-usenet | native usenet engine (gRPC / health) |
| 9625 | logging-file | |
| 9630 / 9631 | downloader-debrid | optional Real-Debrid/AllDebrid bridge (gRPC / health) |
| 9635 | serialization-safe | |
| 9640 / 9641 | media-music | optional Lidarr-class music manager (gRPC / health) |
| 9645 | circuitbreaker-simple | |
| 9650 / 9651 | media-books | optional Readarr-class book manager (gRPC / health) |
| 9655 | data-redaction-pattern | |
| 9660 / 9661 | media-comics | optional manga/comic manager (gRPC / health) |
| 9665 | input-validate-jsonschema | |
| 9670 / 9671 | media-audiobooks | optional audiobook manager (gRPC / health) |
| 9672 / 9673 | userdata-local | optional household userdata (HTTP / gRPC); `MVP_ENABLE_USERDATA_LOCAL=1` |
| 9675 | spool-resolver-http | |
| 9680 / 9681 | storage-ceph | optional Ceph/Rook RGW StorageProvider (gRPC / health) |
| 9690 / 9691 | storage-overlay | optional storage overlay encrypt/compress/dedup (gRPC / health) |
| 9700 | database-sqlite | SQLite DatabaseProvider (gRPC) |
| 9701 | database-postgres | PostgreSQL DatabaseProvider (gRPC) |
| 9710 / 9711 | media-intro-outro | optional intro/outro detection (gRPC / health) |
| 9720 / 9721 | media-transcoder-pool | optional transcode worker pool (gRPC / health) |
| 9730 / 9731 | media-graph | optional unified media graph (gRPC / health); mediauiprox `GRAPH_HTTP_URL` defaults to `:9731` for `GET /api/graph/related` |
| 9740 / 9741 | media-tagging | optional content tagging / classification (gRPC / health) |
| 9750 / 9751 / 8751 | media-dlna | optional DLNA/UPnP server (DLNA HTTP / gRPC / health); `MVP_ENABLE_MEDIA_DLNA=1` |
| 9800 | ratelimit-tokenbucket | spool `default` (fail-open until `RATELIMIT_ENABLED`) |
| 9900 / 9901 | metrics-prometheus gRPC / HTTP | Prometheus scrape on HTTP |
| 8082 | admin-ui HTTP | registry / dev compose (`ADMIN_UI_HTTP_PORT`; `run-host.sh` `ADMIN_UI_ADDR`) |
| 5173 | media-ui HTTP | consumer SPA (`MEDIA_UI_PORT`; registry compose) |
| 18080 | api-rest HTTP | |

When adding a module, pick a free port and update this table.
