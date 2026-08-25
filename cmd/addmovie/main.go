package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	rootsv1 "github.com/Muxcore-Media/media-root-folders/proto/rootsv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9420", "media-movies gRPC address")
	rootsAddr := flag.String("roots-addr", "127.0.0.1:9540", "media-root-folders gRPC address")
	tmdb := flag.Int("tmdb", 550, "TMDB id")
	title := flag.String("title", "Fight Club", "title")
	year := flag.Int("year", 1999, "year")
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
		fmt.Fprintf(os.Stderr, "dial movies: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = conn.Close() }()
	client := mgmntv1.NewMovieManagementServiceClient(conn)

	add, err := client.AddMovie(ctx, &mgmntv1.AddMovieRequest{
		TmdbId:         int32(*tmdb),
		Title:          *title,
		Year:           int32(*year),
		RootFolderPath: *root,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "AddMovie: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("added movie_id=%s\n", add.GetMovieId())

	list, err := client.ListMovies(ctx, &mgmntv1.ListMoviesRequest{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ListMovies: %v\n", err)
		os.Exit(1)
	}
	found := false
	for _, m := range list.GetMovies() {
		if m.GetId() == add.GetMovieId() || m.GetTmdbId() == int32(*tmdb) {
			found = true
			fmt.Printf("listed id=%s title=%s year=%d\n", m.GetId(), m.GetTitle(), m.GetYear())
			break
		}
	}
	if !found {
		fmt.Fprintln(os.Stderr, "AddMovie succeeded but movie missing from ListMovies")
		os.Exit(1)
	}
}

func ensureRoot(ctx context.Context, addr, path string) error {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("dial roots: %w", err)
	}
	defer func() { _ = conn.Close() }()
	client := rootsv1.NewRootFolderServiceClient(conn)

	list, err := client.ListRoots(ctx, &rootsv1.ListRootsRequest{MediaKind: "movies"})
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
		Name:      "MVP library",
		MediaKind: "movies",
	})
	if err != nil {
		return fmt.Errorf("CreateRoot: %w", err)
	}
	fmt.Printf("registered root id=%s path=%s\n", resp.GetRoot().GetId(), resp.GetRoot().GetPath())
	return nil
}
