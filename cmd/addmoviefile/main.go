package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9420", "media-movies gRPC address")
	movieID := flag.String("movie-id", "", "movie id (optional; default: first Fight Club / tmdb match)")
	tmdb := flag.Int("tmdb", 550, "TMDB id used to find movie when -movie-id empty")
	file := flag.String("file", "", "absolute path to media file (required)")
	quality := flag.String("quality", "BDRip", "quality label")
	flag.Parse()
	if strings.TrimSpace(*file) == "" {
		fmt.Fprintln(os.Stderr, "-file is required")
		os.Exit(1)
	}
	abs, err := filepath.Abs(*file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "abs: %v\n", err)
		os.Exit(1)
	}
	st, err := os.Stat(abs)
	if err != nil || st.IsDir() {
		fmt.Fprintf(os.Stderr, "file missing: %s (%v)\n", abs, err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()
	client := mgmntv1.NewMovieManagementServiceClient(conn)

	id := strings.TrimSpace(*movieID)
	if id == "" {
		list, err := client.ListMovies(ctx, &mgmntv1.ListMoviesRequest{Page: 1, PageSize: 100})
		if err != nil {
			fmt.Fprintf(os.Stderr, "ListMovies: %v\n", err)
			os.Exit(1)
		}
		for _, m := range list.GetMovies() {
			if int(m.GetTmdbId()) == *tmdb {
				id = m.GetId()
				break
			}
		}
		if id == "" {
			fmt.Fprintf(os.Stderr, "no movie with tmdb=%d\n", *tmdb)
			os.Exit(1)
		}
	}

	ext := strings.TrimPrefix(filepath.Ext(abs), ".")
	if ext == "" {
		ext = "mp4"
	}
	add, err := client.AddFile(ctx, &mgmntv1.AddFileRequest{
		MovieId:   id,
		FilePath:  abs,
		Quality:   *quality,
		SizeBytes: st.Size(),
		Container: ext,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "AddFile: %v\n", err)
		os.Exit(1)
	}

	got, err := client.GetMovie(ctx, &mgmntv1.GetMovieRequest{MovieId: id})
	if err != nil {
		fmt.Fprintf(os.Stderr, "GetMovie: %v\n", err)
		os.Exit(1)
	}
	m := got.GetMovie()
	fmt.Printf("linked file_id=%s movie_id=%s has_file=%v path=%s size=%d\n",
		add.GetFileId(), id, m.GetHasFile(), abs, st.Size())
}
