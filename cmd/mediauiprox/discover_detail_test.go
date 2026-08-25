package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	metadatav1 "github.com/Muxcore-Media/contracts-metadata/muxcore/metadata/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fixtureMetadataDiscover struct {
	metadatav1.UnimplementedMetadataServiceServer
}

func (f fixtureMetadataDiscover) GetMovieDetails(_ context.Context, req *metadatav1.GetMovieDetailsRequest) (*metadatav1.GetMovieDetailsResponse, error) {
	return &metadatav1.GetMovieDetailsResponse{
		Id:          req.GetTmdbId(),
		Title:       "Fight Club",
		ReleaseDate: "1999-10-15",
		Overview:    "soap",
		VoteAverage: 8.4,
		Genres:      []*metadatav1.Genre{{Name: "Drama"}},
	}, nil
}

func (f fixtureMetadataDiscover) GetTVDetails(_ context.Context, req *metadatav1.GetTVDetailsRequest) (*metadatav1.GetTVDetailsResponse, error) {
	return &metadatav1.GetTVDetailsResponse{
		Id:           req.GetTmdbId(),
		Name:         "Breaking Bad",
		FirstAirDate: "2008-01-20",
		Overview:     "chemistry teacher",
		VoteAverage:  9.5,
		Genres:       []*metadatav1.Genre{{Name: "Crime"}},
		Status:       "Ended",
	}, nil
}

func dialMetadataFixture(t *testing.T) metadatav1.MetadataServiceClient {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	metadatav1.RegisterMetadataServiceServer(srv, fixtureMetadataDiscover{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return metadatav1.NewMetadataServiceClient(conn)
}

func TestHandleDiscoverMovieDetail(t *testing.T) {
	s := &server{metadata: dialMetadataFixture(t)}
	w := httptest.NewRecorder()
	s.handleDiscover(w, httptest.NewRequest(http.MethodGet, "/api/discover/movie/550", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["title"] != "Fight Club" || body["mediaType"] != "movie" {
		t.Fatalf("body=%v", body)
	}
}

func TestHandleDiscoverTVDetail(t *testing.T) {
	s := &server{metadata: dialMetadataFixture(t)}
	w := httptest.NewRecorder()
	s.handleDiscover(w, httptest.NewRequest(http.MethodGet, "/api/discover/tv/1396", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["title"] != "Breaking Bad" || body["mediaType"] != "tv" {
		t.Fatalf("body=%v", body)
	}
}

func TestHandleDiscoverUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handleDiscover(w, httptest.NewRequest(http.MethodGet, "/api/discover/movie/550", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleTrackLyricsProxiesMusicHTTP(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tracks/track-1/lyrics" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"lyrics":"hello world"}`))
	}))
	defer up.Close()

	u, _ := url.Parse(up.URL)
	s := &server{musicHTTP: u}
	w := httptest.NewRecorder()
	s.handleTrackLyrics(w, httptest.NewRequest(http.MethodGet, "/api/music/tracks/track-1/lyrics", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["lyrics"] != "hello world" {
		t.Fatalf("body=%v", body)
	}
}
