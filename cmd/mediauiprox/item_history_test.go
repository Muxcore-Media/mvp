package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	mediaadminv1 "github.com/Muxcore-Media/contracts-media-admin/gen/muxcore/media/admin/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fixtureItemHistory struct {
	mediaadminv1.UnimplementedMediaAdminServiceServer
	itemID string
	event  mediaadminv1.HistoryEventType
}

func (f *fixtureItemHistory) ListHistory(_ context.Context, req *mediaadminv1.ListHistoryRequest) (*mediaadminv1.ListHistoryResponse, error) {
	f.itemID = req.GetItemId()
	f.event = req.GetEventType()
	return &mediaadminv1.ListHistoryResponse{
		Total: 1, Page: 1, PageSize: 50,
		Records: []*mediaadminv1.HistoryRecord{{
			Id: "mh1", EventType: mediaadminv1.HistoryEventType_HISTORY_EVENT_TYPE_GRAB,
			ItemId: req.GetItemId(), Title: "Fight Club", SourceTitle: "Fight.Club.1999.1080p",
			Indexer: "Knaben", CreatedAt: "2026-09-08T10:00:00Z",
		}},
	}, nil
}

func dialItemHistory(t *testing.T) (*fixtureItemHistory, mediaadminv1.MediaAdminServiceClient) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixtureItemHistory{}
	srv := grpc.NewServer()
	mediaadminv1.RegisterMediaAdminServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return fake, mediaadminv1.NewMediaAdminServiceClient(conn)
}

func TestHandleListMovieHistoryUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/movies/m1/history", nil)
	req.SetPathValue("id", "m1")
	s.handleListMovieHistory(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["available"] != false {
		t.Fatalf("body=%v", body)
	}
}

func TestHandleListMovieHistory(t *testing.T) {
	fake, client := dialItemHistory(t)
	s := &server{moviesAdmin: client}
	req := httptest.NewRequest(http.MethodGet, "/api/movies/m1/history?event=grab", nil)
	req.SetPathValue("id", "m1")
	w := httptest.NewRecorder()
	s.handleListMovieHistory(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.itemID != "m1" || fake.event != mediaadminv1.HistoryEventType_HISTORY_EVENT_TYPE_GRAB {
		t.Fatalf("item=%q event=%v", fake.itemID, fake.event)
	}
	var body struct {
		Available bool `json:"available"`
		Items     []struct {
			EventType   string `json:"event_type"`
			SourceTitle string `json:"source_title"`
		} `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || len(body.Items) != 1 || body.Items[0].EventType != "grab" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleListTVHistory(t *testing.T) {
	fake, client := dialItemHistory(t)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("pat", "pat", "", []string{"user"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{tvAdmin: client, sessions: sessions}
	req := httptest.NewRequest(http.MethodGet, "/api/tv/s1/history", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	req.SetPathValue("id", "s1")
	w := httptest.NewRecorder()
	s.handleListTVHistory(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.itemID != "s1" {
		t.Fatalf("item=%q", fake.itemID)
	}
}
