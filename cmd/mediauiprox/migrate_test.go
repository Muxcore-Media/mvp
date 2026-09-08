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

	scannerv1 "github.com/Muxcore-Media/contracts-scanner/muxcore/scanner/v1"
	formatsv1 "github.com/Muxcore-Media/media-custom-formats/proto/formatsv1"
	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	musicv1 "github.com/Muxcore-Media/media-music/proto/gen/muxcore/music/v1"
	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func fixtureArrServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/qualityprofile", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 1, "name": "HD-1080p"}})
	})
	mux.HandleFunc("/api/v3/movie", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"id": 10, "title": "Fight Club", "year": 1999, "tmdbId": 550,
			"monitored": true, "qualityProfileId": 1, "rootFolderPath": "/movies",
		}})
	})
	mux.HandleFunc("/api/v3/series", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"id": 7, "title": "Breaking Bad", "year": 2008, "tmdbId": 1396, "tvdbId": 81189,
			"monitored": true, "qualityProfileId": 1, "rootFolderPath": "/tv",
		}})
	})
	mux.HandleFunc("/api/v1/qualityprofile", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 3, "name": "Any"}})
	})
	mux.HandleFunc("/api/v1/artist", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"id": 4, "artistName": "Radiohead", "foreignArtistId": "a74b1b7f-71a5-4011-9441-d0b5e4122711",
			"monitored": true, "qualityProfileId": 3, "rootFolderPath": "/music",
		}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

type fixtureMigrateMovies struct {
	mgmntv1.UnimplementedMovieManagementServiceServer
	added     *mgmntv1.AddMovieRequest
	monitored bool
}

func (f *fixtureMigrateMovies) AddMovie(_ context.Context, req *mgmntv1.AddMovieRequest) (*mgmntv1.AddMovieResponse, error) {
	f.added = req
	return &mgmntv1.AddMovieResponse{MovieId: "m1"}, nil
}

func (f *fixtureMigrateMovies) UpdateMovie(_ context.Context, req *mgmntv1.UpdateMovieRequest) (*mgmntv1.UpdateMovieResponse, error) {
	f.monitored = req.GetMonitored()
	return &mgmntv1.UpdateMovieResponse{}, nil
}

type fixtureMigrateTV struct {
	tvmgmtv1.UnimplementedTvManagementServiceServer
	added *tvmgmtv1.AddTVShowRequest
}

func (f *fixtureMigrateTV) AddTVShow(_ context.Context, req *tvmgmtv1.AddTVShowRequest) (*tvmgmtv1.AddTVShowResponse, error) {
	f.added = req
	return &tvmgmtv1.AddTVShowResponse{SeriesId: "s1"}, nil
}

func (f *fixtureMigrateTV) UpdateTVShow(context.Context, *tvmgmtv1.UpdateTVShowRequest) (*tvmgmtv1.UpdateTVShowResponse, error) {
	return &tvmgmtv1.UpdateTVShowResponse{}, nil
}

type fixtureMigrateMusic struct {
	musicv1.UnimplementedMusicManagementServiceServer
	added *musicv1.AddArtistRequest
}

func (f *fixtureMigrateMusic) AddArtist(_ context.Context, req *musicv1.AddArtistRequest) (*musicv1.AddArtistResponse, error) {
	f.added = req
	return &musicv1.AddArtistResponse{Artist: &musicv1.Artist{Id: "a1", Name: req.GetName()}}, nil
}

type fixtureMigrateFormats struct {
	formatsv1.UnimplementedFormatServiceServer
}

func (f *fixtureMigrateFormats) ListProfiles(context.Context, *formatsv1.ListProfilesRequest) (*formatsv1.ListProfilesResponse, error) {
	return &formatsv1.ListProfilesResponse{Profiles: []*formatsv1.QualityProfile{{
		Id: "qp-hd", Name: "HD-1080p",
	}}}, nil
}

type fixtureMigrateScanner struct {
	scannerv1.UnimplementedScannerServiceServer
	scanned bool
}

func (f *fixtureMigrateScanner) ScanLibraryRoots(context.Context, *scannerv1.ScanLibraryRootsRequest) (*scannerv1.ScanLibraryRootsResponse, error) {
	f.scanned = true
	return &scannerv1.ScanLibraryRootsResponse{FilesFound: 2, FilesImported: 1}, nil
}

func privilegedMigrateRequest(t *testing.T, path, body string, movies mgmntv1.MovieManagementServiceServer, tv tvmgmtv1.TvManagementServiceServer, music musicv1.MusicManagementServiceServer, formats formatsv1.FormatServiceServer, scanner scannerv1.ScannerServiceServer) (*server, *http.Request) {
	t.Helper()
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions, arrHTTP: http.DefaultClient}
	if movies != nil || tv != nil || music != nil || formats != nil || scanner != nil {
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		srv := grpc.NewServer()
		if movies != nil {
			mgmntv1.RegisterMovieManagementServiceServer(srv, movies)
		}
		if tv != nil {
			tvmgmtv1.RegisterTvManagementServiceServer(srv, tv)
		}
		if music != nil {
			musicv1.RegisterMusicManagementServiceServer(srv, music)
		}
		if formats != nil {
			formatsv1.RegisterFormatServiceServer(srv, formats)
		}
		if scanner != nil {
			scannerv1.RegisterScannerServiceServer(srv, scanner)
		}
		go func() { _ = srv.Serve(lis) }()
		t.Cleanup(srv.Stop)
		conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		if movies != nil {
			s.movies = mgmntv1.NewMovieManagementServiceClient(conn)
		}
		if tv != nil {
			s.tv = tvmgmtv1.NewTvManagementServiceClient(conn)
		}
		if music != nil {
			s.music = musicv1.NewMusicManagementServiceClient(conn)
		}
		if formats != nil {
			s.formats = formatsv1.NewFormatServiceClient(conn)
		}
		if scanner != nil {
			s.scanner = scannerv1.NewScannerServiceClient(conn)
		}
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	return s, req
}

func TestHandleMigrateForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	w := httptest.NewRecorder()
	s.handleMigrate(w, httptest.NewRequest(http.MethodPost, "/api/migrate", strings.NewReader(`{}`)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleMigrateRequiresCredentials(t *testing.T) {
	s, req := privilegedMigrateRequest(t, "/api/migrate", `{"service":"radarr","dry_run":true}`, nil, nil, nil, nil, nil)
	w := httptest.NewRecorder()
	s.handleMigrate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
}

func TestHandleMigrateInvalidService(t *testing.T) {
	s, req := privilegedMigrateRequest(t, "/api/migrate", `{"service":"bazarr","base_url":"http://x","api_key":"k"}`, nil, nil, nil, nil, nil)
	w := httptest.NewRecorder()
	s.handleMigrate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
}

func TestHandleMigrateDryRunRadarr(t *testing.T) {
	arr := fixtureArrServer(t)
	s, req := privilegedMigrateRequest(t, "/api/migrate", `{"service":"radarr","base_url":"`+arr.URL+`","api_key":"test-key","remap_to":"/data/movies"}`, nil, nil, nil, nil, nil)
	w := httptest.NewRecorder()
	s.handleMigrate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "test-key") {
		t.Fatal("api key leaked")
	}
	var body struct {
		DryRun  bool `json:"dry_run"`
		Fetched int  `json:"fetched"`
		Preview []struct {
			Title          string `json:"title"`
			RootFolderPath string `json:"root_folder_path"`
		} `json:"preview"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.DryRun || body.Fetched != 1 || body.Preview[0].Title != "Fight Club" || body.Preview[0].RootFolderPath != "/data/movies" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleMigrateImportRadarr(t *testing.T) {
	arr := fixtureArrServer(t)
	movies := &fixtureMigrateMovies{}
	scanner := &fixtureMigrateScanner{}
	s, req := privilegedMigrateRequest(t, "/api/migrate", `{"service":"radarr","base_url":"`+arr.URL+`","api_key":"k","dry_run":false}`, movies, nil, nil, &fixtureMigrateFormats{}, scanner)
	w := httptest.NewRecorder()
	s.handleMigrate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if movies.added == nil || movies.added.GetTmdbId() != 550 || movies.added.GetQualityProfileId() != "qp-hd" {
		t.Fatalf("added %#v", movies.added)
	}
	if !movies.monitored || !scanner.scanned {
		t.Fatalf("monitored=%v scanned=%v", movies.monitored, scanner.scanned)
	}
	var body struct {
		Imported int    `json:"imported"`
		ScanNote string `json:"scan_note"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Imported != 1 || !strings.Contains(body.ScanNote, "found=2") {
		t.Fatalf("%#v", body)
	}
}

func TestHandleMigrateImportSonarr(t *testing.T) {
	arr := fixtureArrServer(t)
	tv := &fixtureMigrateTV{}
	s, req := privilegedMigrateRequest(t, "/api/migrate", `{"service":"sonarr","base_url":"`+arr.URL+`","api_key":"k","dry_run":false}`, nil, tv, nil, nil, nil)
	w := httptest.NewRecorder()
	s.handleMigrate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if tv.added == nil || tv.added.GetTmdbId() != 1396 {
		t.Fatalf("added %#v", tv.added)
	}
}

func TestHandleMigrateImportLidarr(t *testing.T) {
	arr := fixtureArrServer(t)
	music := &fixtureMigrateMusic{}
	s, req := privilegedMigrateRequest(t, "/api/migrate", `{"service":"lidarr","base_url":"`+arr.URL+`","api_key":"k","dry_run":false}`, nil, nil, music, nil, nil)
	w := httptest.NewRecorder()
	s.handleMigrate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if music.added == nil || music.added.GetMusicbrainzId() == "" {
		t.Fatalf("added %#v", music.added)
	}
}

func TestHandleMigrateLidarrUnavailable(t *testing.T) {
	s, req := privilegedMigrateRequest(t, "/api/migrate", `{"service":"lidarr","base_url":"http://x","api_key":"k","dry_run":false}`, nil, nil, nil, nil, nil)
	w := httptest.NewRecorder()
	s.handleMigrate(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
}

func TestRemapArrRoot(t *testing.T) {
	if got := remapArrRoot("/movies/Fight Club", "", "/data/movies"); got != "/data/movies" {
		t.Fatalf("replace-all: %q", got)
	}
	if got := remapArrRoot("/old/movies/A", "/old/movies", "/data/movies"); got != "/data/movies/A" {
		t.Fatalf("prefix: %q", got)
	}
}
