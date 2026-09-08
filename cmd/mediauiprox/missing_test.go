package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	musicv1 "github.com/Muxcore-Media/media-music/proto/gen/muxcore/music/v1"
	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fixtureMissingMovies struct {
	mgmntv1.UnimplementedMovieManagementServiceServer
}

func (fixtureMissingMovies) ListMissing(_ context.Context, req *mgmntv1.ListMissingRequest) (*mgmntv1.ListMissingResponse, error) {
	return &mgmntv1.ListMissingResponse{
		Items: []*mgmntv1.MissingMovieItem{{
			MovieId: "m-miss", TmdbId: 438631, Title: "Dune", Year: 2021, QualityProfileId: "qp_hd",
		}},
		Total: 1, Page: req.GetPage(), PageSize: req.GetPageSize(),
	}, nil
}

type fixtureMissingTV struct {
	tvmgmtv1.UnimplementedTvManagementServiceServer
	seriesID string
}

func (f *fixtureMissingTV) ListMissing(_ context.Context, req *tvmgmtv1.ListMissingRequest) (*tvmgmtv1.ListMissingResponse, error) {
	f.seriesID = req.GetSeriesId()
	return &tvmgmtv1.ListMissingResponse{
		Items: []*tvmgmtv1.MissingEpisodeItem{{
			EpisodeId: "ep1", SeriesId: "s1", TmdbId: 1396, Title: "Severance", Year: 2022,
			SeasonNumber: 1, EpisodeNumber: 2, AirDate: "2022-02-25",
		}},
		Total: 1, Page: req.GetPage(), PageSize: req.GetPageSize(),
	}, nil
}

func dialMissingMovies(t *testing.T) mgmntv1.MovieManagementServiceClient {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	mgmntv1.RegisterMovieManagementServiceServer(srv, fixtureMissingMovies{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return mgmntv1.NewMovieManagementServiceClient(conn)
}

type fixtureMissingMusic struct {
	musicv1.UnimplementedMusicManagementServiceServer
	artistID string
}

func (f *fixtureMissingMusic) ListMissing(_ context.Context, req *musicv1.ListMissingRequest) (*musicv1.ListMissingResponse, error) {
	f.artistID = req.GetArtistId()
	return &musicv1.ListMissingResponse{
		Items: []*musicv1.MissingAlbumItem{{
			AlbumId: "al1", ArtistId: "ar1", ArtistName: "Radiohead", Title: "OK Computer",
			Year: 1997, MusicbrainzId: "mb-ok", QualityProfileId: "qp_lossy",
		}},
		Total: 1, Page: req.GetPage(), PageSize: req.GetPageSize(),
	}, nil
}

func dialMissingMusic(t *testing.T) (*fixtureMissingMusic, musicv1.MusicManagementServiceClient) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixtureMissingMusic{}
	srv := grpc.NewServer()
	musicv1.RegisterMusicManagementServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return fake, musicv1.NewMusicManagementServiceClient(conn)
}

func dialMissingTV(t *testing.T) (*fixtureMissingTV, tvmgmtv1.TvManagementServiceClient) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixtureMissingTV{}
	srv := grpc.NewServer()
	tvmgmtv1.RegisterTvManagementServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return fake, tvmgmtv1.NewTvManagementServiceClient(conn)
}

func TestHandleLibraryMissingUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handleLibraryMissing(w, httptest.NewRequest(http.MethodGet, "/api/missing", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Available bool `json:"available"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available {
		t.Fatal("expected unavailable")
	}
}

func TestHandleLibraryMissing(t *testing.T) {
	fakeTV, tv := dialMissingTV(t)
	fakeMusic, music := dialMissingMusic(t)
	s := &server{movies: dialMissingMovies(t), tv: tv, music: music}
	w := httptest.NewRecorder()
	s.handleLibraryMissing(w, httptest.NewRequest(http.MethodGet, "/api/missing?series_id=s1&artist_id=ar1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	if fakeTV.seriesID != "s1" {
		t.Fatalf("series_id=%q", fakeTV.seriesID)
	}
	if fakeMusic.artistID != "ar1" {
		t.Fatalf("artist_id=%q", fakeMusic.artistID)
	}
	var body struct {
		Available bool `json:"available"`
		Movies    struct {
			Items []struct {
				Title string `json:"title"`
				Href  string `json:"href"`
			} `json:"items"`
		} `json:"movies"`
		TV struct {
			Items []struct {
				Title         string `json:"title"`
				SeasonNumber  int    `json:"seasonNumber"`
				EpisodeNumber int    `json:"episodeNumber"`
			} `json:"items"`
		} `json:"tv"`
		Music struct {
			Items []struct {
				Title      string `json:"title"`
				ArtistName string `json:"artistName"`
				Href       string `json:"href"`
			} `json:"items"`
		} `json:"music"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.Movies.Items[0].Title != "Dune" || body.Movies.Items[0].Href != "/movies/m-miss" {
		t.Fatalf("movies %#v", body.Movies)
	}
	if body.TV.Items[0].Title != "Severance" || body.TV.Items[0].SeasonNumber != 1 || body.TV.Items[0].EpisodeNumber != 2 {
		t.Fatalf("tv %#v", body.TV)
	}
	if body.Music.Items[0].Title != "OK Computer" || body.Music.Items[0].ArtistName != "Radiohead" || body.Music.Items[0].Href != "/music/ar1" {
		t.Fatalf("music %#v", body.Music)
	}
}

func TestHandleLibraryMissingMoviesOnly(t *testing.T) {
	s := &server{movies: dialMissingMovies(t)}
	w := httptest.NewRecorder()
	s.handleLibraryMissing(w, httptest.NewRequest(http.MethodGet, "/api/missing?type=movie", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Available bool `json:"available"`
		TV        struct {
			Available bool `json:"available"`
		} `json:"tv"`
		Movies struct {
			Total int `json:"total"`
		} `json:"movies"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.TV.Available || body.Movies.Total != 1 {
		t.Fatalf("%#v", body)
	}
}

func TestHandleLibraryMissingLibraryPlus(t *testing.T) {
	books := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/missing" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{{
				"book_id": "b1", "author_id": "a1", "title": "Dune", "author_name": "Herbert", "year": 1965,
			}},
			"total": 1, "page": 1, "page_size": 50,
		})
	}))
	t.Cleanup(books.Close)
	comics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/missing" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{{
				"issue_id": "i1", "series_id": "s1", "title": "Chapter One", "number": "1", "series_name": "Saga", "year": 2012,
			}},
			"total": 1, "page": 1, "page_size": 50,
		})
	}))
	t.Cleanup(comics.Close)
	audiobooks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/missing" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{{
				"audiobook_id": "ab1", "author_id": "a2", "title": "Dune (narrated)", "author_name": "Herbert", "year": 1965,
			}},
			"total": 1, "page": 1, "page_size": 50,
		})
	}))
	t.Cleanup(audiobooks.Close)

	s := &server{
		booksHTTP:      mustURL(books.URL),
		comicsHTTP:     mustURL(comics.URL),
		audiobooksHTTP: mustURL(audiobooks.URL),
	}
	w := httptest.NewRecorder()
	s.handleLibraryMissing(w, httptest.NewRequest(http.MethodGet, "/api/missing", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Books     struct {
			Items []struct {
				Title      string `json:"title"`
				Href       string `json:"href"`
				ArtistName string `json:"artistName"`
			} `json:"items"`
		} `json:"books"`
		Comics struct {
			Items []struct {
				Title       string `json:"title"`
				Href        string `json:"href"`
				IssueNumber string `json:"issueNumber"`
			} `json:"items"`
		} `json:"comics"`
		Audiobooks struct {
			Items []struct {
				Title string `json:"title"`
				Href  string `json:"href"`
			} `json:"items"`
		} `json:"audiobooks"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available {
		t.Fatal("expected available")
	}
	if body.Books.Items[0].Title != "Dune" || body.Books.Items[0].Href != "/books/a1" || body.Books.Items[0].ArtistName != "Herbert" {
		t.Fatalf("books %#v", body.Books)
	}
	if body.Comics.Items[0].Title != "Chapter One" || body.Comics.Items[0].Href != "/comics/s1" || body.Comics.Items[0].IssueNumber != "1" {
		t.Fatalf("comics %#v", body.Comics)
	}
	if body.Audiobooks.Items[0].Title != "Dune (narrated)" || body.Audiobooks.Items[0].Href != "/audiobooks/ab1" {
		t.Fatalf("audiobooks %#v", body.Audiobooks)
	}

	only := httptest.NewRecorder()
	s.handleLibraryMissing(only, httptest.NewRequest(http.MethodGet, "/api/missing?type=book", nil))
	var filtered struct {
		Comics struct {
			Available bool `json:"available"`
		} `json:"comics"`
		Books struct {
			Total int `json:"total"`
		} `json:"books"`
	}
	if err := json.NewDecoder(only.Body).Decode(&filtered); err != nil {
		t.Fatal(err)
	}
	if filtered.Comics.Available || filtered.Books.Total != 1 {
		t.Fatalf("type=book %#v", filtered)
	}
}
