package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fixtureMovieLibrary struct {
	mgmntv1.UnimplementedMovieManagementServiceServer
	removedID     string
	deleteFiles   bool
	refreshedID   string
	fileIDs       []string
	removedFileID string
}

func (f *fixtureMovieLibrary) RemoveMovie(_ context.Context, req *mgmntv1.RemoveMovieRequest) (*mgmntv1.RemoveMovieResponse, error) {
	f.removedID = req.GetMovieId()
	f.deleteFiles = req.GetDeleteFiles()
	return &mgmntv1.RemoveMovieResponse{}, nil
}

func (f *fixtureMovieLibrary) RefreshMetadata(_ context.Context, req *mgmntv1.RefreshMetadataRequest) (*mgmntv1.RefreshMetadataResponse, error) {
	f.refreshedID = req.GetMovieId()
	return &mgmntv1.RefreshMetadataResponse{}, nil
}

func (f *fixtureMovieLibrary) ListFiles(_ context.Context, req *mgmntv1.ListFilesRequest) (*mgmntv1.ListFilesResponse, error) {
	files := []*mgmntv1.MovieFile{}
	for _, id := range f.fileIDs {
		files = append(files, &mgmntv1.MovieFile{
			Id:        id,
			MovieId:   req.GetMovieId(),
			FilePath:  "/data/movies/Fight.Club.1999.mkv",
			Quality:   "Bluray-1080p",
			SizeBytes: 12_000_000_000,
			Container: "mkv",
		})
	}
	return &mgmntv1.ListFilesResponse{Files: files}, nil
}

func (f *fixtureMovieLibrary) RemoveFile(_ context.Context, req *mgmntv1.RemoveFileRequest) (*mgmntv1.RemoveFileResponse, error) {
	f.removedFileID = req.GetFileId()
	f.deleteFiles = req.GetDeleteFiles()
	return &mgmntv1.RemoveFileResponse{}, nil
}

type fixtureTVLibrary struct {
	tvmgmtv1.UnimplementedTvManagementServiceServer
	removedID   string
	deleteFiles bool
	refreshedID string
	episodeID   string
}

func (f *fixtureTVLibrary) RemoveTVShow(_ context.Context, req *tvmgmtv1.RemoveTVShowRequest) (*tvmgmtv1.RemoveTVShowResponse, error) {
	f.removedID = req.GetSeriesId()
	f.deleteFiles = req.GetDeleteFiles()
	return &tvmgmtv1.RemoveTVShowResponse{}, nil
}

func (f *fixtureTVLibrary) RefreshMetadata(_ context.Context, req *tvmgmtv1.RefreshMetadataRequest) (*tvmgmtv1.RefreshMetadataResponse, error) {
	f.refreshedID = req.GetSeriesId()
	return &tvmgmtv1.RefreshMetadataResponse{}, nil
}

func (f *fixtureTVLibrary) RemoveEpisodeFile(_ context.Context, req *tvmgmtv1.RemoveEpisodeFileRequest) (*tvmgmtv1.RemoveEpisodeFileResponse, error) {
	f.episodeID = req.GetEpisodeId()
	f.deleteFiles = req.GetDeleteFiles()
	return &tvmgmtv1.RemoveEpisodeFileResponse{}, nil
}

func dialMovieLibrary(t *testing.T) (*fixtureMovieLibrary, mgmntv1.MovieManagementServiceClient) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixtureMovieLibrary{}
	srv := grpc.NewServer()
	mgmntv1.RegisterMovieManagementServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return fake, mgmntv1.NewMovieManagementServiceClient(conn)
}

func dialTVLibrary(t *testing.T) (*fixtureTVLibrary, tvmgmtv1.TvManagementServiceClient) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixtureTVLibrary{}
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

func TestHandleDeleteMovie(t *testing.T) {
	fake, client := dialMovieLibrary(t)
	s := &server{movies: client}
	req := httptest.NewRequest(http.MethodDelete, "/api/movies/m1?delete_files=1", nil)
	req.SetPathValue("id", "m1")
	w := httptest.NewRecorder()
	s.handleDeleteMovie(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if fake.removedID != "m1" || !fake.deleteFiles {
		t.Fatalf("removed=%q delete=%v", fake.removedID, fake.deleteFiles)
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["removed"] != true || out["delete_files"] != true {
		t.Fatalf("%+v", out)
	}
}

func TestHandleDeleteTVKeepFiles(t *testing.T) {
	fake, client := dialTVLibrary(t)
	s := &server{tv: client}
	req := httptest.NewRequest(http.MethodDelete, "/api/tv/s1", nil)
	req.SetPathValue("id", "s1")
	w := httptest.NewRecorder()
	s.handleDeleteTV(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if fake.removedID != "s1" || fake.deleteFiles {
		t.Fatalf("removed=%q delete=%v", fake.removedID, fake.deleteFiles)
	}
}

func TestHandleDeleteMovieFile(t *testing.T) {
	fake, client := dialMovieLibrary(t)
	fake.fileIDs = []string{"f1"}
	s := &server{movies: client}
	req := httptest.NewRequest(http.MethodDelete, "/api/movies/m1/file?delete_files=1", nil)
	req.SetPathValue("id", "m1")
	w := httptest.NewRecorder()
	s.handleDeleteMovieFile(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if fake.removedFileID != "f1" || !fake.deleteFiles {
		t.Fatalf("file=%q delete=%v", fake.removedFileID, fake.deleteFiles)
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["removed"] != true || out["files"] != float64(1) {
		t.Fatalf("%+v", out)
	}
}

func TestHandleDeleteEpisodeFile(t *testing.T) {
	fake, client := dialTVLibrary(t)
	s := &server{tv: client}
	req := httptest.NewRequest(http.MethodDelete, "/api/episodes/e1/file?delete_files=true", nil)
	req.SetPathValue("id", "e1")
	w := httptest.NewRecorder()
	s.handleDeleteEpisodeFile(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if fake.episodeID != "e1" || !fake.deleteFiles {
		t.Fatalf("episode=%q delete=%v", fake.episodeID, fake.deleteFiles)
	}
}

func TestHandleRefreshMovieAndTV(t *testing.T) {
	mf, mc := dialMovieLibrary(t)
	tf, tc := dialTVLibrary(t)
	s := &server{movies: mc, tv: tc}

	req := httptest.NewRequest(http.MethodPost, "/api/movies/m1/refresh", nil)
	req.SetPathValue("id", "m1")
	w := httptest.NewRecorder()
	s.handleRefreshMovie(w, req)
	if w.Code != http.StatusOK || mf.refreshedID != "m1" {
		t.Fatalf("movie status=%d id=%q body=%s", w.Code, mf.refreshedID, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/tv/s1/refresh", nil)
	req.SetPathValue("id", "s1")
	w = httptest.NewRecorder()
	s.handleRefreshTV(w, req)
	if w.Code != http.StatusOK || tf.refreshedID != "s1" {
		t.Fatalf("tv status=%d id=%q body=%s", w.Code, tf.refreshedID, w.Body.String())
	}
}

func TestHandleDeleteMovieUnavailable(t *testing.T) {
	s := &server{}
	req := httptest.NewRequest(http.MethodDelete, "/api/movies/m1", nil)
	req.SetPathValue("id", "m1")
	w := httptest.NewRecorder()
	s.handleDeleteMovie(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestHandleDeleteMovieMissingID(t *testing.T) {
	s := &server{}
	req := httptest.NewRequest(http.MethodDelete, "/api/movies/", nil)
	w := httptest.NewRecorder()
	s.handleDeleteMovie(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestHandleDeleteLibraryPlus(t *testing.T) {
	var gotPath string
	var gotQuery string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		if r.Method != http.MethodDelete {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"removed": true, "delete_files": true})
	}))
	t.Cleanup(up.Close)

	s := &server{
		booksHTTP:      mustURL(up.URL),
		comicsHTTP:     mustURL(up.URL),
		audiobooksHTTP: mustURL(up.URL),
	}
	mux := http.NewServeMux()
	s.registerLibraryRoutes(mux)

	cases := []struct {
		path     string
		upstream string
	}{
		{"/api/books/a1?delete_files=1", "/api/authors/a1"},
		{"/api/books/works/b1", "/api/books/b1"},
		{"/api/comics/s1", "/api/series/s1"},
		{"/api/comics/issues/i1", "/api/issues/i1"},
		{"/api/audiobooks/ab1?delete_files=1", "/api/audiobooks/ab1"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodDelete, tc.path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status %d body=%s", tc.path, w.Code, w.Body.String())
		}
		if gotPath != tc.upstream {
			t.Fatalf("%s upstream path %q want %q", tc.path, gotPath, tc.upstream)
		}
		var body struct {
			Removed     bool `json:"removed"`
			DeleteFiles bool `json:"delete_files"`
		}
		if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !body.Removed {
			t.Fatalf("%s expected removed", tc.path)
		}
		if strings.Contains(tc.path, "delete_files=1") && !strings.Contains(gotQuery, "delete_files=1") {
			t.Fatalf("%s query %q missing delete_files", tc.path, gotQuery)
		}
	}
}

func TestHandleDeleteBookAuthorUnavailable(t *testing.T) {
	s := &server{}
	req := httptest.NewRequest(http.MethodDelete, "/api/books/a1", nil)
	req.SetPathValue("id", "a1")
	w := httptest.NewRecorder()
	s.handleDeleteBookAuthor(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", w.Code)
	}
}
