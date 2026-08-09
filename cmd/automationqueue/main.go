package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	automationv1 "github.com/Muxcore-Media/media-automation/proto/automationv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9460", "media-automation gRPC address")
	title := flag.String("title", "Fight Club", "wanted title")
	itemID := flag.String("item-id", "mv_smoke_550", "wanted item id")
	tmdbID := flag.Int("tmdb", 550, "TMDB id")
	year := flag.Int("year", 1999, "year")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	client := automationv1.NewAutomationServiceClient(conn)

	add, err := client.AddToQueue(ctx, &automationv1.AddToQueueRequest{
		ItemType: "movie",
		ItemId:   *itemID,
		TmdbId:   int32(*tmdbID),
		Title:    *title,
		Year:     int32(*year),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "AddToQueue: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("queued id=%s title=%q tmdb=%d\n", add.GetQueueId(), *title, *tmdbID)

	q, err := client.GetQueue(ctx, &automationv1.GetQueueRequest{Page: 1, PageSize: 50, Filter: "movie"})
	if err != nil {
		fmt.Fprintf(os.Stderr, "GetQueue: %v\n", err)
		os.Exit(1)
	}
	found := false
	for _, it := range q.GetItems() {
		if it.GetId() == add.GetQueueId() || it.GetTmdbId() == int32(*tmdbID) {
			fmt.Printf("listed id=%s title=%q tmdb=%d monitored=%v\n",
				it.GetId(), it.GetTitle(), it.GetTmdbId(), it.GetMonitored())
			found = true
			break
		}
	}
	if !found {
		fmt.Fprintf(os.Stderr, "GetQueue: added item not found (total=%d)\n", q.GetTotal())
		os.Exit(1)
	}

	search, err := client.SearchItem(ctx, &automationv1.SearchItemRequest{
		ItemType: "movie",
		Query:    *title,
		TmdbId:   int32(*tmdbID),
		Year:     int32(*year),
		Limit:    5,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SearchItem: %v\n", err)
		os.Exit(1)
	}
	// Soft: no indexer in default MVP → empty matches (not an error).
	fmt.Printf("SearchItem matches=%d (empty OK without indexer)\n", len(search.GetMatches()))
}
