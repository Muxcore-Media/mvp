package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fixtureMoviesAPI struct {
	mgmntv1.UnimplementedMovieManagementServiceServer
}

func (f fixtureMoviesAPI) ListMovies(_ context.Context, req *mgmntv1.ListMoviesRequest) (*mgmntv1.ListMoviesResponse, error) {
	return &mgmntv1.ListMoviesResponse{
		Movies: []*mgmntv1.MovieItem{{
			Id:          "m1",
			Title:       "Inception",
			Year:        2010,
			HasFile:     true,
			Genres:      []string{"Sci-Fi"},
			VoteAverage: 8.8,
		}},
		Total:    1,
		Page:     req.GetPage(),
		PageSize: req.GetPageSize(),
	}, nil
}

func (f fixtureMoviesAPI) GetMovie(_ context.Context, req *mgmntv1.GetMovieRequest) (*mgmntv1.GetMovieResponse, error) {
	return &mgmntv1.GetMovieResponse{
		Movie: &mgmntv1.MovieItem{
			Id:       req.GetMovieId(),
			Title:    "Inception",
			Year:     2010,
			Overview: "Dreams",
			HasFile:  true,
		},
	}, nil
}

func dialMoviesAPIFixture(t *testing.T) mgmntv1.MovieManagementServiceClient {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	mgmntv1.RegisterMovieManagementServiceServer(srv, fixtureMoviesAPI{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return mgmntv1.NewMovieManagementServiceClient(conn)
}

type fixtureTVAPI struct {
	tvmgmtv1.UnimplementedTvManagementServiceServer
}

func (f fixtureTVAPI) ListTVShows(_ context.Context, req *tvmgmtv1.ListTVShowsRequest) (*tvmgmtv1.ListTVShowsResponse, error) {
	return &tvmgmtv1.ListTVShowsResponse{
		Series: []*tvmgmtv1.TVSeries{{
			Id:     "s1",
			Name:   "Severance",
			Year:   2022,
			Genres: []string{"Sci-Fi"},
			Seasons: []*tvmgmtv1.TVSeason{{
				Id:           "season-1",
				SeasonNumber: 1,
				Episodes: []*tvmgmtv1.TVEpisode{{
					Id:            "e1",
					SeasonNumber:  1,
					EpisodeNumber: 1,
					Name:          "Good News About Hell",
					HasFile:       true,
				}},
			}},
		}},
		Total:    1,
		Page:     req.GetPage(),
		PageSize: req.GetPageSize(),
	}, nil
}

func (f fixtureTVAPI) GetTVShow(_ context.Context, req *tvmgmtv1.GetTVShowRequest) (*tvmgmtv1.GetTVShowResponse, error) {
	return &tvmgmtv1.GetTVShowResponse{
		Series: &tvmgmtv1.TVSeries{
			Id:       req.GetSeriesId(),
			Name:     "Severance",
			Year:     2022,
			Overview: "Work-life severance",
			Seasons: []*tvmgmtv1.TVSeason{{
				Id:           "season-1",
				SeasonNumber: 1,
				Episodes: []*tvmgmtv1.TVEpisode{{
					Id:            "e1",
					SeasonNumber:  1,
					EpisodeNumber: 1,
					Name:          "Good News About Hell",
					HasFile:       true,
				}},
			}},
		},
	}, nil
}

func dialTVAPIFixture(t *testing.T) tvmgmtv1.TvManagementServiceClient {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	tvmgmtv1.RegisterTvManagementServiceServer(srv, fixtureTVAPI{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return tvmgmtv1.NewTvManagementServiceClient(conn)
}

func TestHandleListMovies(t *testing.T) {
	s := &server{movies: dialMoviesAPIFixture(t)}
	w := httptest.NewRecorder()
	s.handleListMovies(w, httptest.NewRequest(http.MethodGet, "/api/movies?page=1&page_size=24", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var body struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0]["title"] != "Inception" {
		t.Fatalf("items=%v", body.Items)
	}
}

func TestHandleMovieByID(t *testing.T) {
	s := &server{movies: dialMoviesAPIFixture(t)}
	w := httptest.NewRecorder()
	s.handleMovieByID(w, httptest.NewRequest(http.MethodGet, "/api/movies/m1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var body struct {
		Movie map[string]any `json:"movie"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Movie["title"] != "Inception" {
		t.Fatalf("movie=%v", body.Movie)
	}
}

func TestHandleListTV(t *testing.T) {
	s := &server{tv: dialTVAPIFixture(t)}
	w := httptest.NewRecorder()
	s.handleListTV(w, httptest.NewRequest(http.MethodGet, "/api/tv?page=1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0]["title"] != "Severance" {
		t.Fatalf("items=%v", body.Items)
	}
	if body.Items[0]["has_file"] != true {
		t.Fatalf("expected has_file true, got %v", body.Items[0]["has_file"])
	}
}

func TestHandleTVByID(t *testing.T) {
	s := &server{tv: dialTVAPIFixture(t)}
	w := httptest.NewRecorder()
	s.handleTVByID(w, httptest.NewRequest(http.MethodGet, "/api/tv/s1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var body struct {
		Show map[string]any `json:"show"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Show["title"] != "Severance" {
		t.Fatalf("show=%v", body.Show)
	}
}
