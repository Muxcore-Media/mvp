# Default module gRPC / HTTP ports

Host `run-host.sh` sets explicit `*_GRPC_ADDR` / `*_HTTP_ADDR` env vars. Module code defaults below must stay unique so a bare `go run` / compose without remaps does not collide.

| Port | Module | Notes |
|------|--------|-------|
| 9101 | call-policy-default | |
| 9102 | publish-policy-default | |
| 9202 / 9203 | health-monitor gRPC / HTTP | |
| 9380 / 9481 | request-media HTTP / gRPC | |
| 9400 | api-rest gRPC | |
| 9401 | auth-local HTTP | |
| 9402 / 9404 | feature-flags-file gRPC / HTTP | |
| 9403 | auth-local gRPC | |
| 9410 | metadata-tmdb | |
| 9411 / 9412 | auth-oidc HTTP / gRPC | |
| 9420 / 9430 | media-movies gRPC / HTTP | |
| 9440 / 9450 | media-tvshows gRPC / HTTP | |
| 9441 | notification-default | |
| 9445 | notification-apprise | optional (`MVP_ENABLE_NOTIFICATION_APPRISE=1` / compose `apprise`) |
| 9460 | media-automation | |
| 9470 | media-scanner | |
| 9475 / 8475 | jellyfin gRPC / HTTP | |
| 9480 | media-ffprobe | |
| 9490 | media-custom-formats | |
| 9510 | media-rename | |
| 9520 | media-subtitles | |
| 9525 | media-transcoder | |
| 9530 | media-list-sync | optional (`MVP_ENABLE_MEDIA_LIST_SYNC=1` / compose `list-sync`) |
| 9540 | media-root-folders | |
| 9550 / 9551 | secrets-file / secrets-vault | |
| 9600 | cache-redis | optional (`MVP_ENABLE_CACHE_REDIS=1` or `REDIS_ADDR`) |
| 9601 | encryption-aesgcm | |
| 9603 | workflow-tapestry | optional (`MVP_ENABLE_WORKFLOW_TAPESTRY=1`) |
| 9610 / 9611 | storage-s3 | optional S3/MinIO StorageProvider (gRPC / health) |
| 9680 / 9681 | storage-ceph | optional Ceph/Rook RGW StorageProvider (gRPC / health) |
| 9690 / 9691 | storage-overlay | optional storage overlay encrypt/compress/dedup (gRPC / health) |
| 9700 | database-sqlite | SQLite DatabaseProvider (gRPC) |
| 9730 / 9731 | media-graph | optional unified media graph (gRPC / health) |
| 9740 / 9741 | media-tagging | optional content tagging / classification (gRPC / health) |
| 9461 | downloader-native-torrent | native torrent engine (gRPC) |
| 9462 / 9463 | downloader-qbittorrent | optional qBittorrent WebUI bridge (gRPC / health) |
| 9672 / 9673 | userdata-local | optional household userdata (HTTP / gRPC); `MVP_ENABLE_USERDATA_LOCAL=1` |
| 9620 / 9621 | downloader-sabnzbd | optional SABnzbd usenet bridge (gRPC / health) |
| 9630 / 9631 | downloader-debrid | optional Real-Debrid/AllDebrid bridge (gRPC / health) |
| 9640 / 9641 | media-music | optional Lidarr-class music manager (gRPC / health) |
| 9650 / 9651 | media-books | optional Readarr-class book manager (gRPC / health) |
| 9660 / 9661 | media-comics | optional manga/comic manager (gRPC / health) |
| 9670 / 9671 | media-audiobooks | optional audiobook manager (gRPC / health) |
| 9800 | ratelimit-tokenbucket | spool `default` (fail-open until `RATELIMIT_ENABLED`) |
| 18080 | api-rest HTTP | |

When adding a module, pick a free port and update this table.
