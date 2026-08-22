module github.com/Muxcore-Media/mvp-smoke

go 1.26.5

require (
	github.com/Muxcore-Media/core v0.5.8
	github.com/Muxcore-Media/core/sdk/go/client v0.5.2
	github.com/Muxcore-Media/jellyfin v0.2.4
	github.com/Muxcore-Media/media-automation v0.1.7
	github.com/Muxcore-Media/media-list-sync v0.1.1
	github.com/Muxcore-Media/media-movies v0.1.9
	github.com/Muxcore-Media/media-root-folders v0.1.4
	github.com/Muxcore-Media/media-scanner v0.1.7
	github.com/Muxcore-Media/media-subtitles v0.4.1
	github.com/Muxcore-Media/media-tvshows v0.1.9
	github.com/Muxcore-Media/metadata-tmdb v0.1.1
	github.com/Muxcore-Media/userdata-local v0.1.0
	google.golang.org/grpc v1.83.0
)

require (
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260610212136-7ab31c22f7ad // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/Muxcore-Media/userdata-local => ../userdata-local

replace github.com/Muxcore-Media/media-subtitles => ../media-subtitles

replace github.com/Muxcore-Media/jellyfin => ../jellyfin

replace github.com/Muxcore-Media/media-movies => ../media-movies

replace github.com/Muxcore-Media/media-tvshows => ../media-tvshows

replace github.com/Muxcore-Media/media-automation => ../media-automation

replace github.com/Muxcore-Media/media-scanner => ../media-scanner

replace github.com/Muxcore-Media/media-root-folders => ../media-root-folders

replace github.com/Muxcore-Media/metadata-tmdb => ../metadata-tmdb

replace github.com/Muxcore-Media/media-list-sync => ../media-list-sync

replace github.com/Muxcore-Media/metadata-musicbrainz => ../metadata-musicbrainz

replace github.com/Muxcore-Media/media-music => ../media-music
