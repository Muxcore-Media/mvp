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
	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fixtureMovieTitles struct {
	mgmntv1.UnimplementedMovieManagementServiceServer
	added   string
	deleted string
}

func (f *fixtureMovieTitles) ListAlternateTitles(context.Context, *mgmntv1.ListAlternateTitlesRequest) (*mgmntv1.ListAlternateTitlesResponse, error) {
	return &mgmntv1.ListAlternateTitlesResponse{Titles: []*mgmntv1.AlternateTitle{
		{Id: "mt1", Title: "Fight Club", CleanTitle: "fight club", Source: "primary"},
		{Id: "mt2", Title: "The Fight Club", CleanTitle: "fight club", Source: "user"},
	}}, nil
}

func (f *fixtureMovieTitles) AddAlternateTitle(_ context.Context, req *mgmntv1.AddAlternateTitleRequest) (*mgmntv1.AddAlternateTitleResponse, error) {
	f.added = req.GetTitle()
	return &mgmntv1.AddAlternateTitleResponse{Title: &mgmntv1.AlternateTitle{
		Id: "mt-new", Title: req.GetTitle(), CleanTitle: "club de la lucha", Source: "user",
	}}, nil
}

func (f *fixtureMovieTitles) RemoveAlternateTitle(_ context.Context, req *mgmntv1.RemoveAlternateTitleRequest) (*mgmntv1.RemoveAlternateTitleResponse, error) {
	f.deleted = req.GetTitleId()
	return &mgmntv1.RemoveAlternateTitleResponse{}, nil
}

type fixtureTVTitles struct {
	tvmgmtv1.UnimplementedTvManagementServiceServer
	added string
}

func (f *fixtureTVTitles) ListAlternateTitles(context.Context, *tvmgmtv1.ListAlternateTitlesRequest) (*tvmgmtv1.ListAlternateTitlesResponse, error) {
	return &tvmgmtv1.ListAlternateTitlesResponse{Titles: []*tvmgmtv1.AlternateTitle{
		{Id: "tt1", Title: "The Office", Source: "primary"},
	}}, nil
}

func (f *fixtureTVTitles) AddAlternateTitle(_ context.Context, req *tvmgmtv1.AddAlternateTitleRequest) (*tvmgmtv1.AddAlternateTitleResponse, error) {
	f.added = req.GetTitle()
	return &tvmgmtv1.AddAlternateTitleResponse{Title: &tvmgmtv1.AlternateTitle{
		Id: "tt-new", Title: req.GetTitle(), Source: "user",
	}}, nil
}

func privilegedMovieTitlesRequest(t *testing.T, method, path, body string) (*server, *http.Request, *fixtureMovieTitles) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixtureMovieTitles{}
	srv := grpc.NewServer()
	mgmntv1.RegisterMovieManagementServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{movies: mgmntv1.NewMovieManagementServiceClient(conn), sessions: sessions}
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	return s, req, fake
}

func TestHandleListMovieTitles(t *testing.T) {
	s, req, _ := privilegedMovieTitlesRequest(t, http.MethodGet, "/api/movies/m1/titles", "")
	req.SetPathValue("id", "m1")
	w := httptest.NewRecorder()
	s.handleListMovieTitles(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Titles    []struct {
			Title string `json:"title"`
			User  bool   `json:"user"`
		} `json:"titles"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || len(body.Titles) != 2 || !body.Titles[1].User {
		t.Fatalf("%#v", body)
	}
}

func TestHandleAddMovieTitle(t *testing.T) {
	s, req, fake := privilegedMovieTitlesRequest(t, http.MethodPost, "/api/movies/m1/titles", `{"title":"El club de la lucha"}`)
	req.SetPathValue("id", "m1")
	w := httptest.NewRecorder()
	s.handleAddMovieTitle(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.added != "El club de la lucha" {
		t.Fatalf("added %q", fake.added)
	}
}

func TestHandleDeleteMovieTitle(t *testing.T) {
	s, req, fake := privilegedMovieTitlesRequest(t, http.MethodDelete, "/api/movies/m1/titles/mt2", "")
	req.SetPathValue("id", "m1")
	req.SetPathValue("titleId", "mt2")
	w := httptest.NewRecorder()
	s.handleDeleteMovieTitle(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.deleted != "mt2" {
		t.Fatalf("deleted %q", fake.deleted)
	}
}

func TestHandleAddTVTitle(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixtureTVTitles{}
	srv := grpc.NewServer()
	tvmgmtv1.RegisterTvManagementServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{tv: tvmgmtv1.NewTvManagementServiceClient(conn), sessions: sessions}
	req := httptest.NewRequest(http.MethodPost, "/api/tv/s1/titles", strings.NewReader(`{"title":"The Office UK"}`))
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	req.SetPathValue("id", "s1")
	w := httptest.NewRecorder()
	s.handleAddTVTitle(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.added != "The Office UK" {
		t.Fatalf("added %q", fake.added)
	}
}

func TestHandleListMovieTitlesUnavailable(t *testing.T) {
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions}
	req := httptest.NewRequest(http.MethodGet, "/api/movies/m1/titles", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	req.SetPathValue("id", "m1")
	w := httptest.NewRecorder()
	s.handleListMovieTitles(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Available bool  `json:"available"`
		Titles    []any `json:"titles"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available || body.Titles == nil {
		t.Fatalf("%#v", body)
	}
}
