package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fixtureCollectionsMovies struct {
	mgmntv1.UnimplementedMovieManagementServiceServer
}

func (f fixtureCollectionsMovies) ListCollections(_ context.Context, _ *mgmntv1.ListCollectionsRequest) (*mgmntv1.ListCollectionsResponse, error) {
	return &mgmntv1.ListCollectionsResponse{
		Collections: []*mgmntv1.CollectionSummary{{
			CollectionId: 42,
			Name:         "Marvel Cinematic Universe",
			MovieCount:   3,
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

func dialMoviesFixture(t *testing.T) mgmntv1.MovieManagementServiceClient {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	mgmntv1.RegisterMovieManagementServiceServer(srv, fixtureCollectionsMovies{})
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
