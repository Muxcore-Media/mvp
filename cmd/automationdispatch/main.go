package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	automationv1 "github.com/Muxcore-Media/media-automation/proto/automationv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9460", "media-automation gRPC address")
	library := flag.String("library", "", "library root to verify import (optional)")
	title := flag.String("title", "Fight Club", "dispatch title")
	dn := flag.String("dn", "Fight.Club.1999.1080p.Fixture", "magnet display name (fixture release folder)")
	timeout := flag.Duration("timeout", 45*time.Second, "wait for history completed")
	flag.Parse()

	magnet := fmt.Sprintf(
		"magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567&dn=%s",
		*dn,
	)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout+10*time.Second)
	defer cancel()
	conn, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()
	client := automationv1.NewAutomationServiceClient(conn)

	disp, err := client.Dispatch(ctx, &automationv1.DispatchRequest{
		Guid:             fmt.Sprintf("fixture-%d", time.Now().UnixNano()),
		Title:            *title,
		DownloadUrl:      magnet,
		DownloadProtocol: "torrent",
		Size:             8192,
		Score:            100,
		IndexerName:      "fixture",
		ItemType:         "movie",
		ItemId:           "mv_smoke_550",
		TmdbId:           550,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Dispatch: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("dispatched download_id=%s status=%s\n", disp.GetDownloadId(), disp.GetStatus())

	deadline := time.Now().Add(*timeout)
	var lastStatus string
	for time.Now().Before(deadline) {
		hist, err := client.GetHistory(ctx, &automationv1.GetHistoryRequest{Page: 1, PageSize: 50})
		if err != nil {
			fmt.Fprintf(os.Stderr, "GetHistory: %v\n", err)
			os.Exit(1)
		}
		for _, r := range hist.GetRecords() {
			if r.GetDownloadId() != disp.GetDownloadId() {
				continue
			}
			lastStatus = r.GetStatus()
			fmt.Printf("history id=%s status=%s title=%q\n", r.GetId(), r.GetStatus(), r.GetTitle())
			switch r.GetStatus() {
			case "completed":
				if *library != "" {
					if err := assertLibraryHasFight(*library); err != nil {
						fmt.Fprintf(os.Stderr, "%v\n", err)
						os.Exit(1)
					}
				}
				fmt.Println("OK dispatch → download.completed → import")
				return
			case "import_failed", "failed":
				fmt.Fprintf(os.Stderr, "download ended with status %s\n", r.GetStatus())
				os.Exit(1)
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	fmt.Fprintf(os.Stderr, "timeout waiting for completed (last status=%q)\n", lastStatus)
	os.Exit(1)
}

func assertLibraryHasFight(library string) error {
	var matches []string
	root, err := filepath.Abs(library)
	if err != nil {
		return err
	}
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if strings.Contains(strings.ToLower(info.Name()), "fight") {
			matches = append(matches, path)
		}
		return nil
	})
	if len(matches) == 0 {
		return fmt.Errorf("no Fight Club file under library %s after import", root)
	}
	fmt.Printf("library file %s\n", matches[0])
	return nil
}
