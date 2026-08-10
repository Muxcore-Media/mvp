// jellyfinlive exercises live Jellyfin bridge RPCs (configured Status, SyncLibrary, RefreshLibrary).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	jellyfinv1 "github.com/Muxcore-Media/jellyfin/proto/jellyfinv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9475", "jellyfin gRPC address")
	skipSync := flag.Bool("skip-sync", false, "skip SyncLibrary")
	skipRefresh := flag.Bool("skip-refresh", false, "skip RefreshLibrary")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	conn, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fail("dial: %v", err)
	}
	defer conn.Close()
	client := jellyfinv1.NewJellyfinBridgeClient(conn)

	st, err := client.Status(ctx, &jellyfinv1.StatusRequest{})
	if err != nil {
		fail("Status: %v", err)
	}
	fmt.Printf("status configured=%v base_url=%q links=%d\n",
		st.GetConfigured(), st.GetBaseUrl(), st.GetItemLinks())
	if !st.GetConfigured() {
		fail("bridge not configured — set JELLYFIN_BASE_URL + JELLYFIN_API_KEY on the jellyfin module")
	}
	if st.GetBaseUrl() == "" {
		fail("Status.BaseUrl empty while Configured=true")
	}

	if !*skipRefresh {
		ref, err := client.RefreshLibrary(ctx, &jellyfinv1.RefreshLibraryRequest{})
		if err != nil {
			fail("RefreshLibrary: %v", err)
		}
		fmt.Printf("RefreshLibrary ok=%v\n", ref.GetOk())
	}

	if !*skipSync {
		sync, err := client.SyncLibrary(ctx, &jellyfinv1.SyncLibraryRequest{Direction: "both"})
		if err != nil {
			fail("SyncLibrary: %v", err)
		}
		fmt.Printf("SyncLibrary scanned=%d upserted=%d errors=%d\n",
			sync.GetScanned(), sync.GetUpserted(), len(sync.GetErrors()))
		if len(sync.GetErrors()) > 0 {
			fmt.Printf("SyncLibrary first error: %s\n", sync.GetErrors()[0])
		}
	}

	list, err := client.ListItemLinks(ctx, &jellyfinv1.ListItemLinksRequest{})
	if err != nil {
		fail("ListItemLinks: %v", err)
	}
	fmt.Printf("ListItemLinks count=%d\n", len(list.GetLinks()))
	if len(list.GetLinks()) > 0 {
		link := list.GetLinks()[0]
		play, err := client.PlayURL(ctx, &jellyfinv1.PlayURLRequest{ItemId: link.GetJellyfinId()})
		if err != nil {
			fail("PlayURL: %v", err)
		}
		if play.GetUrl() == "" {
			fail("PlayURL returned empty url")
		}
		fmt.Printf("PlayURL sample=%s\n", play.GetUrl())
	}
	fmt.Println("OK jellyfin live smoke")
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
