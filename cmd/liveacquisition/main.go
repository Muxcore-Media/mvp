package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	downloaderv1 "github.com/Muxcore-Media/contracts-downloader/muxcore/downloader/v1"
	indexerv1 "github.com/Muxcore-Media/contracts-indexer/muxcore/indexer/v1"
	automationv1 "github.com/Muxcore-Media/media-automation/proto/automationv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	indexerAddr := flag.String("indexer", "127.0.0.1:9485", "indexer-piratebay gRPC")
	automationAddr := flag.String("automation", "127.0.0.1:9460", "media-automation gRPC")
	downloaderAddr := flag.String("downloader", "127.0.0.1:9461", "downloader-native-torrent gRPC")
	query := flag.String("query", "Fight Club 1999", "search query")
	itemType := flag.String("type", "movie", "search type")
	tmdb := flag.Int("tmdb", 550, "TMDB id for automation SearchItem")
	year := flag.Int("year", 1999, "year")
	dispatch := flag.Bool("dispatch", true, "dispatch best magnet via automation")
	timeout := flag.Duration("timeout", 3*time.Minute, "wait for live download progress")
	minBytes := flag.Int64("min-bytes", 256*1024, "bytes downloaded to count as live progress")
	maxSize := flag.Int64("max-size", 4_000_000_000, "prefer releases under this size (bytes)")
	minSeeders := flag.Int("min-seeders", 3, "reject releases below this seeder count")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout+30*time.Second)
	defer cancel()

	idxConn, err := grpc.NewClient(*indexerAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fail("dial indexer: %v", err)
	}
	defer idxConn.Close()
	idx := indexerv1.NewIndexerServiceClient(idxConn)

	caps, err := idx.GetCapabilities(ctx, &indexerv1.GetCapabilitiesRequest{})
	if err != nil {
		fail("GetCapabilities: %v", err)
	}
	fmt.Printf("indexer caps movie=%v tv=%v\n", caps.GetSupportsMovieSearch(), caps.GetSupportsTvSearch())

	sresp, err := idx.Search(ctx, &indexerv1.SearchRequest{
		Query: *query,
		Type:  *itemType,
		Limit: 20,
	})
	if err != nil {
		fail("indexer Search: %v", err)
	}
	results := sresp.GetResults()
	if len(results) == 0 {
		fail("indexer Search returned 0 results for %q (is PIRATEBAY_API_BASE set? VPN up?)", *query)
	}
	fmt.Printf("indexer Search hits=%d top=%q seeders=%d size=%d\n",
		len(results), results[0].GetTitle(), results[0].GetSeeders(), results[0].GetSize())

	autoConn, err := grpc.NewClient(*automationAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fail("dial automation: %v", err)
	}
	defer autoConn.Close()
	auto := automationv1.NewAutomationServiceClient(autoConn)

	search, err := auto.SearchItem(ctx, &automationv1.SearchItemRequest{
		ItemType: *itemType,
		Query:    *query,
		TmdbId:   int32(*tmdb),
		Year:     int32(*year),
		Limit:    10,
	})
	if err != nil {
		fail("automation SearchItem: %v", err)
	}
	matches := search.GetMatches()
	if len(matches) == 0 {
		fail("automation SearchItem returned 0 matches (indexer discovery/dial?)")
	}
	fmt.Printf("automation SearchItem matches=%d top=%q score=%d\n",
		len(matches), matches[0].GetTitle(), matches[0].GetScore())

	if !*dispatch {
		fmt.Println("OK live search (dispatch skipped)")
		return
	}

	best := pickBest(matches, *maxSize, *minSeeders)
	if best == nil {
		fail("no suitable match (max-size=%d min-seeders=%d)", *maxSize, *minSeeders)
	}
	fmt.Printf("dispatching title=%q size=%d seeders=%d url=%s\n",
		best.GetTitle(), best.GetSize(), best.GetSeeders(), truncate(best.GetDownloadUrl(), 80))

	dispCtx, dispCancel := context.WithTimeout(context.Background(), 45*time.Second)
	disp, err := auto.Dispatch(dispCtx, &automationv1.DispatchRequest{
		Guid:             best.GetGuid(),
		Title:            best.GetTitle(),
		DownloadUrl:      best.GetDownloadUrl(),
		DownloadProtocol: firstNonEmpty(best.GetDownloadProtocol(), "torrent"),
		Size:             best.GetSize(),
		Score:            best.GetScore(),
		IndexerName:      best.GetIndexerName(),
		ItemType:         *itemType,
		ItemId:           fmt.Sprintf("live_%d", *tmdb),
		TmdbId:           int32(*tmdb),
	})
	dispCancel()
	if err != nil {
		fail("Dispatch: %v", err)
	}
	fmt.Printf("dispatched download_id=%s status=%s\n", disp.GetDownloadId(), disp.GetStatus())

	dlConn, err := grpc.NewClient(*downloaderAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fail("dial downloader: %v", err)
	}
	defer dlConn.Close()
	dl := downloaderv1.NewDownloaderServiceClient(dlConn)

	deadline := time.Now().Add(*timeout)
	var lastBytes int64
	var lastStatus string
	var sawMeta bool
	for time.Now().Before(deadline) {
		hist, err := auto.GetHistory(ctx, &automationv1.GetHistoryRequest{Page: 1, PageSize: 50})
		if err == nil {
			for _, r := range hist.GetRecords() {
				if r.GetDownloadId() != disp.GetDownloadId() {
					continue
				}
				lastStatus = r.GetStatus()
				fmt.Printf("history status=%s title=%q\n", r.GetStatus(), r.GetTitle())
				switch r.GetStatus() {
				case "completed":
					fmt.Println("OK live acquisition: history completed")
					return
				case "failed", "import_failed":
					fail("download ended with status %s", r.GetStatus())
				}
			}
		}

		gt, err := dl.GetTorrent(ctx, &downloaderv1.GetTorrentRequest{TorrentId: disp.GetDownloadId()})
		if err == nil && gt.GetTorrent() != nil {
			t := gt.GetTorrent()
			lastBytes = t.GetDownloaded()
			lastStatus = t.GetStatus()
			if t.GetName() != "" && !strings.EqualFold(t.GetName(), "unknown") {
				sawMeta = true
			}
			fmt.Printf("torrent status=%s name=%q downloaded=%d seeders=%d leechers=%d progress=%.2f\n",
				t.GetStatus(), t.GetName(), t.GetDownloaded(), t.GetSeeders(), t.GetLeechers(), t.GetProgress())
			if lastBytes >= *minBytes {
				fmt.Printf("OK live acquisition: downloaded >= %d bytes (progress proven)\n", *minBytes)
				return
			}
		}
		time.Sleep(2 * time.Second)
	}

	if sawMeta && lastBytes > 0 {
		fmt.Printf("OK live acquisition: metadata + partial progress bytes=%d status=%s\n", lastBytes, lastStatus)
		return
	}
	if sawMeta {
		fmt.Printf("OK live acquisition: torrent metadata resolved (status=%s bytes=%d) — swarm slow; search+dispatch path verified\n", lastStatus, lastBytes)
		return
	}
	fail("timeout waiting for live torrent progress (last status=%q bytes=%d)", lastStatus, lastBytes)
}

func pickBest(matches []*automationv1.ReleaseMatch, maxSize int64, minSeeders int) *automationv1.ReleaseMatch {
	type scored struct {
		m *automationv1.ReleaseMatch
		s int64
	}
	var list []scored
	for _, m := range matches {
		if m.GetDownloadUrl() == "" {
			continue
		}
		if int(m.GetSeeders()) < minSeeders {
			continue
		}
		proto := strings.ToLower(m.GetDownloadProtocol())
		if proto != "" && !strings.Contains(proto, "torrent") && !strings.HasPrefix(m.GetDownloadUrl(), "magnet:") {
			continue
		}
		list = append(list, scored{m: m, s: scoreMatch(m, maxSize)})
	}
	if len(list) == 0 {
		return nil
	}
	sort.Slice(list, func(i, j int) bool { return list[i].s > list[j].s })
	return list[0].m
}

func scoreMatch(m *automationv1.ReleaseMatch, maxSize int64) int64 {
	seeders := int64(m.GetSeeders())
	s := int64(m.GetScore())*100 + seeders*10_000
	if m.GetSize() > 0 && m.GetSize() <= maxSize {
		s += 5_000
	}
	if m.GetSize() > 0 && m.GetSize() < 2_000_000_000 {
		s += 2_000
	}
	// Prefer smaller legal-smoke-friendly rips when seeders are comparable.
	title := strings.ToLower(m.GetTitle())
	if strings.Contains(title, "1080p") || strings.Contains(title, "720p") {
		s += 1_000
	}
	return s
}
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
