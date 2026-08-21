// refreshmovies calls RefreshMetadata for every movie in media-movies.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9420", "media-movies gRPC address")
	pageSize := flag.Int("page-size", 100, "ListMovies page size")
	missingPosters := flag.Bool("missing-posters", false, "only refresh movies with empty poster_path and tmdb_id")
	flag.Parse()

	conn, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()
	client := mgmntv1.NewMovieManagementServiceClient(conn)

	var (
		page           int32 = 1
		ok, fail, skip int
	)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		list, err := client.ListMovies(ctx, &mgmntv1.ListMoviesRequest{
			Page:     page,
			PageSize: int32(*pageSize),
		})
		cancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "ListMovies page=%d: %v\n", page, err)
			os.Exit(1)
		}
		movies := list.GetMovies()
		if len(movies) == 0 {
			break
		}
		for _, mov := range movies {
			id := mov.GetId()
			title := mov.GetTitle()
			if *missingPosters {
				if mov.GetTmdbId() == 0 || strings.TrimSpace(mov.GetPosterPath()) != "" {
					skip++
					continue
				}
			}
			rctx, rcancel := context.WithTimeout(context.Background(), 45*time.Second)
			_, err := client.RefreshMetadata(rctx, &mgmntv1.RefreshMetadataRequest{MovieId: id})
			rcancel()
			if err != nil {
				fail++
				fmt.Printf("FAIL %s %q: %v\n", id, title, err)
				continue
			}
			ok++
			fmt.Printf("OK   %s %q\n", id, title)
		}
		if int32(len(movies)) < int32(*pageSize) || page*int32(*pageSize) >= list.GetTotal() {
			break
		}
		page++
	}
	fmt.Printf("done refreshed=%d skipped=%d failed=%d\n", ok, skip, fail)
	if fail > 0 {
		os.Exit(1)
	}
}
