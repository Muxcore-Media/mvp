module github.com/Muxcore-Media/mvp-smoke

go 1.26.4

require (
	github.com/Muxcore-Media/core v0.5.2
	github.com/Muxcore-Media/core/sdk/go/client v0.5.2
	github.com/Muxcore-Media/jellyfin v0.2.3
	github.com/Muxcore-Media/media-automation v0.1.5
	github.com/Muxcore-Media/media-movies v0.1.3
	github.com/Muxcore-Media/media-root-folders v0.1.3
	github.com/Muxcore-Media/media-scanner v0.1.4
	github.com/Muxcore-Media/media-tvshows v0.1.4
	google.golang.org/grpc v1.82.1
)

require (
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/text v0.39.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260610212136-7ab31c22f7ad // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/Muxcore-Media/core => ../core

replace github.com/Muxcore-Media/core/sdk/go/client => ../core/sdk/go/client

replace github.com/Muxcore-Media/core/pkg/contracts => ../core/pkg/contracts
