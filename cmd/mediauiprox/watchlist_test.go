package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	listsyncv1 "github.com/Muxcore-Media/media-list-sync/proto/listsyncv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestWatchlistItemFilter(t *testing.T) {
	items := []*listsyncv1.SyncItem{
		{TmdbId: 550, Title: "Fight Club", Year: 1999, MediaType: "movie", Action: "watchlist"},
		{TmdbId: 0, Title: "No TMDB", MediaType: "movie", Action: "watchlist"},
		{TmdbId: 1396, Title: "Breaking Bad", MediaType: "tv", Action: "collection"},
	}
	var out []watchlistItem
	for _, it := range items {
		if it == nil || it.GetAction() != "watchlist" || it.GetTmdbId() <= 0 {
			continue
		}
		kind := it.GetMediaType()
		if kind != "movie" && kind != "tv" {
			continue
		}
		out = append(out, watchlistItem{ID: it.GetTmdbId(), Title: it.GetTitle(), Year: it.GetYear(), MediaType: kind})
	}
	if len(out) != 1 || out[0].Title != "Fight Club" {
		t.Fatalf("%+v", out)
	}
}

type fixtureListSync struct {
	listsyncv1.UnimplementedListSyncServiceServer
}

func (f fixtureListSync) GetItems(_ context.Context, req *listsyncv1.GetItemsRequest) (*listsyncv1.GetItemsResponse, error) {
	return &listsyncv1.GetItemsResponse{
		Items: []*listsyncv1.SyncItem{
			{TmdbId: 550, Title: "Fight Club", Year: 1999, MediaType: "movie", Action: "watchlist"},
			{TmdbId: 1396, Title: "Breaking Bad", Year: 2008, MediaType: "tv", Action: "collection"},
			{TmdbId: 0, Title: "No TMDB", MediaType: "movie", Action: "watchlist"},
		},
		Total:    3,
		Page:     req.GetPage(),
		PageSize: req.GetPageSize(),
	}, nil
}

func dialListSyncFixture(t *testing.T) listsyncv1.ListSyncServiceClient {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	listsyncv1.RegisterListSyncServiceServer(srv, fixtureListSync{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return listsyncv1.NewListSyncServiceClient(conn)
}

func TestHandleWatchlistUnavailable(t *testing.T) {
	s := &server{}
	rec := httptest.NewRecorder()
	s.handleWatchlist(rec, httptest.NewRequest(http.MethodGet, "/api/watchlist", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestHandleWatchlistMethodNotAllowed(t *testing.T) {
	s := &server{listSync: dialListSyncFixture(t)}
	rec := httptest.NewRecorder()
	s.handleWatchlist(rec, httptest.NewRequest(http.MethodPost, "/api/watchlist", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestHandleWatchlistFiltersItems(t *testing.T) {
	s := &server{listSync: dialListSyncFixture(t)}
	rec := httptest.NewRecorder()
	s.handleWatchlist(rec, httptest.NewRequest(http.MethodGet, "/api/watchlist?page=1&page_size=10", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []watchlistItem `json:"items"`
		Total int             `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0].Title != "Fight Club" {
		t.Fatalf("items=%v", body.Items)
	}
	if body.Total != 1 {
		t.Fatalf("total=%d", body.Total)
	}
}
