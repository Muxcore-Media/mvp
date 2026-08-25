package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	jellyfinv1 "github.com/Muxcore-Media/jellyfin/proto/jellyfinv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9475", "jellyfin gRPC address")
	webhookURL := flag.String("webhook-url", "http://127.0.0.1:8475/webhook", "HTTP webhook URL")
	muxID := flag.String("muxcore-id", "mv_smoke_550", "MuxCore item id for fixture link")
	title := flag.String("title", "Fight Club", "link title")
	path := flag.String("path", "/library/Movies/Fight Club (1999)/Fight Club.mkv", "media path")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = conn.Close() }()
	client := jellyfinv1.NewJellyfinBridgeClient(conn)

	up, err := client.UpsertItemLink(ctx, &jellyfinv1.UpsertItemLinkRequest{
		Link: &jellyfinv1.ItemLink{
			MuxcoreId:   *muxID,
			JellyfinId:  "jf-smoke-fixture",
			Path:        *path,
			MediaKind:   "movie",
			Title:       *title,
			ProviderIds: map[string]string{"Tmdb": "550"},
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "UpsertItemLink: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("upserted mux=%s jf=%s title=%q\n",
		up.GetLink().GetMuxcoreId(), up.GetLink().GetJellyfinId(), up.GetLink().GetTitle())

	list, err := client.ListItemLinks(ctx, &jellyfinv1.ListItemLinksRequest{MediaKind: "movie"})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ListItemLinks: %v\n", err)
		os.Exit(1)
	}
	found := false
	for _, link := range list.GetLinks() {
		if link.GetMuxcoreId() == *muxID {
			found = true
			fmt.Printf("listed mux=%s jf=%s path=%q\n", link.GetMuxcoreId(), link.GetJellyfinId(), link.GetPath())
			break
		}
	}
	if !found {
		fmt.Fprintf(os.Stderr, "ListItemLinks: fixture link %q not found (n=%d)\n", *muxID, len(list.GetLinks()))
		os.Exit(1)
	}

	st, err := client.Status(ctx, &jellyfinv1.StatusRequest{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Status: %v\n", err)
		os.Exit(1)
	}
	if st.GetItemLinks() < 1 {
		fmt.Fprintf(os.Stderr, "Status.item_links=%d, want >=1\n", st.GetItemLinks())
		os.Exit(1)
	}
	fmt.Printf("status configured=%v item_links=%d\n", st.GetConfigured(), st.GetItemLinks())

	sync, err := client.SyncLibrary(ctx, &jellyfinv1.SyncLibraryRequest{Direction: "both", DryRun: true})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SyncLibrary: %v\n", err)
		os.Exit(1)
	}
	// Soft when unconfigured: scanned=0 + skip note; live would scan >0.
	fmt.Printf("SyncLibrary soft scanned=%d matched=%d errors=%v\n",
		sync.GetScanned(), sync.GetMatched(), sync.GetErrors())

	body := []byte(`{
  "NotificationType": "PlaybackStart",
  "ItemId": "jf-smoke-fixture",
  "Name": "Fight Club",
  "UserId": "mvp-user",
  "NotificationUsername": "mvp",
  "SessionId": "sess-smoke",
  "PlaybackPositionTicks": 0,
  "RunTimeTicks": 6000000000
}`)
	resp, err := http.Post(*webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "POST /webhook: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "POST /webhook HTTP %d: %s\n", resp.StatusCode, respBody)
		os.Exit(1)
	}
	fmt.Printf("webhook PlaybackStart HTTP %d %s\n", resp.StatusCode, bytes.TrimSpace(respBody))
	fmt.Println("OK jellyfin soft link + webhook")
}
