package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fixtureCollectionsMovies struct {
	mgmntv1.UnimplementedMovieManagementServiceServer
	monitored bool
	synced    bool
}

func (f fixtureCollectionsMovies) ListCollections(_ context.Context, _ *mgmntv1.ListCollectionsRequest) (*mgmntv1.ListCollectionsResponse, error) {
	return &mgmntv1.ListCollectionsResponse{
		Collections: []*mgmntv1.CollectionSummary{{
			CollectionId: 42,
			Name:         "Marvel Cinematic Universe",
			MovieCount:   3,
			Monitored:    true,
		}},
	}, nil
}

func (f fixtureCollectionsMovies) GetCollectionMovies(_ context.Context, req *mgmntv1.GetCollectionMoviesRequest) (*mgmntv1.GetCollectionMoviesResponse, error) {
	return &mgmntv1.GetCollectionMoviesResponse{
		CollectionId: req.GetCollectionId(),
		Name:         "Marvel Cinematic Universe",
		Movies: []*mgmntv1.MovieItem{{
			Id:      "iron-man",
			Title:   "Iron Man",
			Year:    2008,
			HasFile: true,
			Genres:  []string{"Action"},
		}},
	}, nil
}

func (f *fixtureCollectionsMovies) GetCollectionPrefs(_ context.Context, req *mgmntv1.GetCollectionPrefsRequest) (*mgmntv1.GetCollectionPrefsResponse, error) {
	return &mgmntv1.GetCollectionPrefsResponse{Prefs: &mgmntv1.CollectionPrefs{
		CollectionId: req.GetCollectionId(), Name: "Marvel Cinematic Universe", Monitored: f.monitored, SearchOnAdd: true,
	}}, nil
}

func (f *fixtureCollectionsMovies) SetCollectionMonitored(_ context.Context, req *mgmntv1.SetCollectionMonitoredRequest) (*mgmntv1.SetCollectionMonitoredResponse, error) {
	f.monitored = req.GetMonitored()
	return &mgmntv1.SetCollectionMonitoredResponse{Prefs: &mgmntv1.CollectionPrefs{
		CollectionId: req.GetCollectionId(), Monitored: f.monitored, SearchOnAdd: true,
	}}, nil
}

func (f *fixtureCollectionsMovies) SyncCollection(_ context.Context, _ *mgmntv1.SyncCollectionRequest) (*mgmntv1.SyncCollectionResponse, error) {
	f.synced = true
	return &mgmntv1.SyncCollectionResponse{Added: 2, AlreadyPresent: 1}, nil
}

func dialMoviesFixture(t *testing.T) mgmntv1.MovieManagementServiceClient {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	mgmntv1.RegisterMovieManagementServiceServer(srv, &fixtureCollectionsMovies{monitored: true})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return mgmntv1.NewMovieManagementServiceClient(conn)
}

func TestHandleListCollections(t *testing.T) {
	s := &server{movies: dialMoviesFixture(t)}
	w := httptest.NewRecorder()
	s.handleListCollections(w, httptest.NewRequest(http.MethodGet, "/api/collections", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var body struct {
		Items []struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			MovieCount int    `json:"movie_count"`
		} `json:"items"`
		Source string `json:"source"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0].ID != "42" || body.Items[0].Name != "Marvel Cinematic Universe" {
		t.Fatalf("items=%v", body.Items)
	}
	if body.Source != "media-movies" {
		t.Fatalf("source=%s", body.Source)
	}
}

func TestHandleCollectionByID(t *testing.T) {
	s := &server{movies: dialMoviesFixture(t)}
	w := httptest.NewRecorder()
	s.handleCollectionByID(w, httptest.NewRequest(http.MethodGet, "/api/collections/42", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var body struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Total  int    `json:"total"`
		Movies []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"movies"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.ID != "42" || body.Name != "Marvel Cinematic Universe" || body.Total != 1 {
		t.Fatalf("body=%+v", body)
	}
	if len(body.Movies) != 1 || body.Movies[0].Title != "Iron Man" {
		t.Fatalf("movies=%v", body.Movies)
	}
}

func TestHandleCollectionByIDInvalid(t *testing.T) {
	s := &server{movies: dialMoviesFixture(t)}
	w := httptest.NewRecorder()
	s.handleCollectionByID(w, httptest.NewRequest(http.MethodGet, "/api/collections/not-a-number", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleSetCollectionMonitoredAndSync(t *testing.T) {
	fake := &fixtureCollectionsMovies{}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	mgmntv1.RegisterMovieManagementServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin-1", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{movies: mgmntv1.NewMovieManagementServiceClient(conn), sessions: sessions}

	req := httptest.NewRequest(http.MethodPatch, "/api/collections/42", strings.NewReader(`{"monitored":true}`))
	req.SetPathValue("id", "42")
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleSetCollectionMonitored(w, req)
	if w.Code != http.StatusOK || !fake.monitored {
		t.Fatalf("monitor %d %s mon=%v", w.Code, w.Body.String(), fake.monitored)
	}

	syncReq := httptest.NewRequest(http.MethodPost, "/api/collections/42/sync", strings.NewReader(`{"add_missing":true}`))
	syncReq.SetPathValue("id", "42")
	syncReq.AddCookie(&http.Cookie{Name: "session", Value: tok})
	sw := httptest.NewRecorder()
	s.handleSyncCollection(sw, syncReq)
	if sw.Code != http.StatusOK || !fake.synced {
		t.Fatalf("sync %d %s synced=%v", sw.Code, sw.Body.String(), fake.synced)
	}
	var body struct {
		Added int `json:"added"`
	}
	if err := json.NewDecoder(sw.Body).Decode(&body); err != nil || body.Added != 2 {
		t.Fatalf("sync body %+v %v", body, err)
	}
}
