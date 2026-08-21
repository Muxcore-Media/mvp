// refreshtvshows calls RefreshMetadata for TV series in media-tvshows.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9440", "media-tvshows gRPC address")
	pageSize := flag.Int("page-size", 100, "ListTVShows page size")
	missingPosters := flag.Bool("missing-posters", false, "only refresh series with empty poster_path and tmdb_id")
	flag.Parse()

	conn, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()
	client := tvmgmtv1.NewTvManagementServiceClient(conn)

	var (
		page           int32 = 1
		ok, fail, skip int
	)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		list, err := client.ListTVShows(ctx, &tvmgmtv1.ListTVShowsRequest{
			Page:     page,
			PageSize: int32(*pageSize),
		})
		cancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "ListTVShows page=%d: %v\n", page, err)
			os.Exit(1)
		}
		shows := list.GetSeries()
		if len(shows) == 0 {
			break
		}
		for _, show := range shows {
			id := show.GetId()
			title := show.GetName()
			if *missingPosters {
				if show.GetTmdbId() == 0 || strings.TrimSpace(show.GetPosterPath()) != "" {
					skip++
					continue
				}
			}
			rctx, rcancel := context.WithTimeout(context.Background(), 60*time.Second)
			_, err := client.RefreshMetadata(rctx, &tvmgmtv1.RefreshMetadataRequest{SeriesId: id})
			rcancel()
			if err != nil {
				fail++
				fmt.Printf("FAIL %s %q: %v\n", id, title, err)
				continue
			}
			ok++
			fmt.Printf("OK   %s %q\n", id, title)
		}
		if int32(len(shows)) < int32(*pageSize) || page*int32(*pageSize) >= list.GetTotal() {
			break
		}
		page++
	}
	fmt.Printf("done refreshed=%d skipped=%d failed=%d\n", ok, skip, fail)
	if fail > 0 {
		os.Exit(1)
	}
}
