module github.com/Muxcore-Media/mvp-smoke

go 1.26.5

require (
	github.com/Muxcore-Media/backup-local v0.1.0
	github.com/Muxcore-Media/contracts-automation v0.1.1-0.20260824174909-b7b0cb83d8b3
	github.com/Muxcore-Media/contracts-indexer v0.1.0
	github.com/Muxcore-Media/contracts-media-admin v0.1.1-0.20260905225357-350de7622545
	github.com/Muxcore-Media/contracts-metadata v0.1.1-0.20260824175102-c62591db5268
	github.com/Muxcore-Media/contracts-notification v0.1.1
	github.com/Muxcore-Media/contracts-scanner v0.1.0
	github.com/Muxcore-Media/core v0.5.8
	github.com/Muxcore-Media/core/sdk/go/client v0.5.8
	github.com/Muxcore-Media/jellyfin v0.3.0
	github.com/Muxcore-Media/media-custom-formats v0.1.6
	github.com/Muxcore-Media/media-ffprobe v0.1.8
	github.com/Muxcore-Media/media-intro-outro v0.2.0
	github.com/Muxcore-Media/media-library-maintainer v0.1.0
	github.com/Muxcore-Media/media-list-sync v0.1.1
	github.com/Muxcore-Media/media-movies v0.1.9
	github.com/Muxcore-Media/media-music v0.3.0
	github.com/Muxcore-Media/media-rename v0.2.6
	github.com/Muxcore-Media/media-root-folders v0.1.4
	github.com/Muxcore-Media/media-subtitles v0.5.0
	github.com/Muxcore-Media/media-tvshows v0.1.9
	github.com/Muxcore-Media/playback-guard v0.1.0
	github.com/Muxcore-Media/plex v0.1.0
	github.com/Muxcore-Media/userdata-local v0.1.0
	google.golang.org/grpc v1.83.0
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/Muxcore-Media/contracts-media v0.1.0 // indirect
	github.com/Muxcore-Media/core/pkg/contracts v0.5.8 // indirect
	github.com/Muxcore-Media/core/pkg/tenant v0.5.8 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260610212136-7ab31c22f7ad // indirect
)

replace github.com/Muxcore-Media/core => ../core

replace github.com/Muxcore-Media/core/sdk/go/client => ../core/sdk/go/client

replace github.com/Muxcore-Media/userdata-local => ../userdata-local

replace github.com/Muxcore-Media/media-subtitles => ../media-subtitles

replace github.com/Muxcore-Media/jellyfin => ../jellyfin

replace github.com/Muxcore-Media/media-movies => ../media-movies

replace github.com/Muxcore-Media/media-tvshows => ../media-tvshows

replace github.com/Muxcore-Media/media-automation => ../media-automation

replace github.com/Muxcore-Media/media-intro-outro => ../media-intro-outro

replace github.com/Muxcore-Media/media-custom-formats => ../media-custom-formats

replace github.com/Muxcore-Media/media-ffprobe => ../media-ffprobe

replace github.com/Muxcore-Media/media-scanner => ../media-scanner

replace github.com/Muxcore-Media/media-root-folders => ../media-root-folders

replace github.com/Muxcore-Media/media-rename => ../media-rename

replace github.com/Muxcore-Media/plex => ../plex

replace github.com/Muxcore-Media/metadata-tmdb => ../metadata-tmdb

replace github.com/Muxcore-Media/media-list-sync => ../media-list-sync

replace github.com/Muxcore-Media/media-library-maintainer => ../media-library-maintainer

replace github.com/Muxcore-Media/playback-guard => ../playback-guard

replace github.com/Muxcore-Media/metadata-musicbrainz => ../metadata-musicbrainz

replace github.com/Muxcore-Media/media-music => ../media-music

replace github.com/Muxcore-Media/contracts-scanner => ../contracts-scanner

replace github.com/Muxcore-Media/contracts-indexer => ../contracts-indexer

replace github.com/Muxcore-Media/backup-local => ../backup-local

replace github.com/Muxcore-Media/contracts-notification => ../contracts-notification

replace github.com/Muxcore-Media/contracts-automation => ../contracts-automation

replace github.com/Muxcore-Media/contracts-metadata => ../contracts-metadata

replace github.com/Muxcore-Media/contracts-media => ../contracts-media

replace github.com/Muxcore-Media/contracts-media-admin => ../contracts-media-admin

replace github.com/Muxcore-Media/core/pkg/contracts => ../core/pkg/contracts

replace github.com/Muxcore-Media/core/sdk/go/module => ../core/sdk/go/module
