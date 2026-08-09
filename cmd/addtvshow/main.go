package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	rootsv1 "github.com/Muxcore-Media/media-root-folders/proto/rootsv1"
	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9440", "media-tvshows gRPC address")
	rootsAddr := flag.String("roots-addr", "127.0.0.1:9540", "media-root-folders gRPC address")
	tmdb := flag.Int("tmdb", 1396, "TMDB id")
	name := flag.String("name", "Breaking Bad", "series name")
	year := flag.Int("year", 2008, "year")
	root := flag.String("root", "", "optional root folder path (registered via media-root-folders when set)")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if *root != "" {
		if err := ensureRoot(ctx, *rootsAddr, *root); err != nil {
			fmt.Fprintf(os.Stderr, "ensure root: %v\n", err)
			os.Exit(1)
		}
	}

	conn, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial tvshows: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()
	client := tvmgmtv1.NewTvManagementServiceClient(conn)

	add, err := client.AddTVShow(ctx, &tvmgmtv1.AddTVShowRequest{
		TmdbId:         int32(*tmdb),
		Name:           *name,
		Year:           int32(*year),
		RootFolderPath: *root,
		SeriesType:     "standard",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "AddTVShow: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("added series_id=%s\n", add.GetSeriesId())

	list, err := client.ListTVShows(ctx, &tvmgmtv1.ListTVShowsRequest{PageSize: 100})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ListTVShows: %v\n", err)
		os.Exit(1)
	}
	found := false
	for _, s := range list.GetSeries() {
		if s.GetId() == add.GetSeriesId() || s.GetTmdbId() == int32(*tmdb) {
			found = true
			fmt.Printf("listed id=%s name=%s year=%d\n", s.GetId(), s.GetName(), s.GetYear())
			break
		}
	}
	if !found {
		fmt.Fprintln(os.Stderr, "AddTVShow succeeded but series missing from ListTVShows")
		os.Exit(1)
	}
}

func ensureRoot(ctx context.Context, addr, path string) error {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("dial roots: %w", err)
	}
	defer conn.Close()
	client := rootsv1.NewRootFolderServiceClient(conn)

	list, err := client.ListRoots(ctx, &rootsv1.ListRootsRequest{MediaKind: "tv"})
	if err != nil {
		return fmt.Errorf("ListRoots: %w", err)
	}
	for _, r := range list.GetRoots() {
		if strings.TrimRight(r.GetPath(), "/") == strings.TrimRight(path, "/") {
			fmt.Printf("root already registered path=%s\n", r.GetPath())
			return nil
		}
	}

	resp, err := client.CreateRoot(ctx, &rootsv1.CreateRootRequest{
		Path:      path,
		Name:      "MVP TV library",
		MediaKind: "tv",
	})
	if err != nil {
		return fmt.Errorf("CreateRoot: %w", err)
	}
	fmt.Printf("registered root id=%s path=%s\n", resp.GetRoot().GetId(), resp.GetRoot().GetPath())
	return nil
}
