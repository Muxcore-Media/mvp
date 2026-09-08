package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	renamev1 "github.com/Muxcore-Media/media-rename/proto/renamev1"
	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fixtureRename struct {
	renamev1.UnimplementedRenameServiceServer
	previewed string
	executed  string
}

func (f *fixtureRename) Preview(_ context.Context, req *renamev1.PreviewRequest) (*renamev1.PreviewResponse, error) {
	f.previewed = req.GetFilePath()
	return &renamev1.PreviewResponse{
		CurrentPath: req.GetFilePath(),
		NewPath:     "/data/movies/Fight Club (1999)/Fight Club (1999) [1080p].mkv",
		NewFilename: "Fight Club (1999) [1080p].mkv",
	}, nil
}

func (f *fixtureRename) Execute(_ context.Context, req *renamev1.ExecuteRequest) (*renamev1.ExecuteResponse, error) {
	f.executed = req.GetFilePath()
	return &renamev1.ExecuteResponse{
		OldPath: req.GetFilePath(),
		NewPath: "/data/movies/Fight Club (1999)/Fight Club (1999) [1080p].mkv",
		Success: true,
	}, nil
}

type fixtureRenameMovies struct {
	mgmntv1.UnimplementedMovieManagementServiceServer
	removedFile string
	addedPath   string
}

func (f *fixtureRenameMovies) GetMovie(_ context.Context, req *mgmntv1.GetMovieRequest) (*mgmntv1.GetMovieResponse, error) {
	return &mgmntv1.GetMovieResponse{Movie: &mgmntv1.MovieItem{
		Id: req.GetMovieId(), Title: "Fight Club", Year: 1999, HasFile: true,
		RootFolderPath: "/data/movies", TmdbId: 550,
	}}, nil
}

func (f *fixtureRenameMovies) ListFiles(_ context.Context, req *mgmntv1.ListFilesRequest) (*mgmntv1.ListFilesResponse, error) {
	return &mgmntv1.ListFilesResponse{Files: []*mgmntv1.MovieFile{{
		Id: "f1", MovieId: req.GetMovieId(), FilePath: "/data/movies/Fight.Club.1999.1080p.mkv", Quality: "1080p",
	}}}, nil
}

func (f *fixtureRenameMovies) RemoveFile(_ context.Context, req *mgmntv1.RemoveFileRequest) (*mgmntv1.RemoveFileResponse, error) {
	f.removedFile = req.GetFileId()
	return &mgmntv1.RemoveFileResponse{}, nil
}

func (f *fixtureRenameMovies) AddFile(_ context.Context, req *mgmntv1.AddFileRequest) (*mgmntv1.AddFileResponse, error) {
	f.addedPath = req.GetFilePath()
	return &mgmntv1.AddFileResponse{FileId: "f2"}, nil
}

type fixtureRenameTV struct {
	tvmgmtv1.UnimplementedTvManagementServiceServer
	removedEp string
	addedPath string
}

func (f *fixtureRenameTV) GetTVShow(_ context.Context, req *tvmgmtv1.GetTVShowRequest) (*tvmgmtv1.GetTVShowResponse, error) {
	return &tvmgmtv1.GetTVShowResponse{Series: &tvmgmtv1.TVSeries{
		Id: req.GetSeriesId(), Name: "Breaking Bad", Year: 2008,
		Seasons: []*tvmgmtv1.TVSeason{{
			Id: "s1", SeasonNumber: 1,
			Episodes: []*tvmgmtv1.TVEpisode{{
				Id: "e1", SeasonNumber: 1, EpisodeNumber: 1, Name: "Pilot", HasFile: true,
			}},
		}},
	}}, nil
}

func (f *fixtureRenameTV) RemoveEpisodeFile(_ context.Context, req *tvmgmtv1.RemoveEpisodeFileRequest) (*tvmgmtv1.RemoveEpisodeFileResponse, error) {
	f.removedEp = req.GetEpisodeId()
	return &tvmgmtv1.RemoveEpisodeFileResponse{}, nil
}

func (f *fixtureRenameTV) AddEpisodeFile(_ context.Context, req *tvmgmtv1.AddEpisodeFileRequest) (*tvmgmtv1.AddEpisodeFileResponse, error) {
	f.addedPath = req.GetFilePath()
	return &tvmgmtv1.AddEpisodeFileResponse{FileId: "ef2"}, nil
}

func dialRename(t *testing.T) (*fixtureRename, renamev1.RenameServiceClient) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixtureRename{}
	srv := grpc.NewServer()
	renamev1.RegisterRenameServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return fake, renamev1.NewRenameServiceClient(conn)
}

func dialRenameMovies(t *testing.T) (*fixtureRenameMovies, mgmntv1.MovieManagementServiceClient) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixtureRenameMovies{}
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

func dialRenameTV(t *testing.T) (*fixtureRenameTV, tvmgmtv1.TvManagementServiceClient) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixtureRenameTV{}
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

func TestHandleRenamePreviewUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handleRenamePreview(w, httptest.NewRequest(http.MethodGet, "/api/rename/preview?movie_id=m1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Available bool  `json:"available"`
		Items     []any `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available || body.Items == nil {
		t.Fatalf("%#v", body)
	}
}

func TestHandleRenamePreviewMovie(t *testing.T) {
	_, renameClient := dialRename(t)
	_, movies := dialRenameMovies(t)
	s := &server{rename: renameClient, movies: movies}
	w := httptest.NewRecorder()
	s.handleRenamePreview(w, httptest.NewRequest(http.MethodGet, "/api/rename/preview?movie_id=m1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Items     []struct {
			CurrentPath string `json:"current_path"`
			NewPath     string `json:"new_path"`
			Changed     bool   `json:"changed"`
		} `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || len(body.Items) != 1 || !body.Items[0].Changed {
		t.Fatalf("%#v", body)
	}
	if body.Items[0].CurrentPath != "/data/movies/Fight.Club.1999.1080p.mkv" {
		t.Fatalf("current %q", body.Items[0].CurrentPath)
	}
}

func TestHandleRenameExecuteMovie(t *testing.T) {
	fakeRename, renameClient := dialRename(t)
	fakeMovies, movies := dialRenameMovies(t)
	s := &server{rename: renameClient, movies: movies}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/rename", bytes.NewBufferString(`{"movie_id":"m1"}`))
	s.handleRenameExecute(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Renamed int `json:"renamed"`
		Errors  int `json:"errors"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Renamed != 1 || body.Errors != 0 {
		t.Fatalf("%#v", body)
	}
	if fakeRename.executed == "" || fakeMovies.removedFile != "f1" || fakeMovies.addedPath == "" {
		t.Fatalf("rename=%q remove=%q add=%q", fakeRename.executed, fakeMovies.removedFile, fakeMovies.addedPath)
	}
}

func TestHandleRenamePreviewTV(t *testing.T) {
	_, renameClient := dialRename(t)
	_, tv := dialRenameTV(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/episodes/e1/file" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"file_id": "ef1", "file_path": "/data/tv/Breaking.Bad.S01E01.mkv", "quality": "HDTV-720p",
		})
	}))
	t.Cleanup(upstream.Close)
	s := &server{rename: renameClient, tv: tv, tvHTTP: mustURL(upstream.URL)}
	w := httptest.NewRecorder()
	s.handleRenamePreview(w, httptest.NewRequest(http.MethodGet, "/api/rename/preview?tv_id=s1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Items     []struct {
			EpisodeID string `json:"episode_id"`
			Changed   bool   `json:"changed"`
		} `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || len(body.Items) != 1 || body.Items[0].EpisodeID != "e1" || !body.Items[0].Changed {
		t.Fatalf("%#v", body)
	}
}
