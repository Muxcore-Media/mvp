package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	subtv1 "github.com/Muxcore-Media/media-subtitles/proto/subtv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fixtureItemSubtitles struct {
	subtv1.UnimplementedSubtitleServiceServer
	listedID    string
	deleted     string
	uploaded    string
	uploadedLang string
	media       []*subtv1.SubtitleMediaItem
}

func (f *fixtureItemSubtitles) ListSubtitles(_ context.Context, req *subtv1.ListSubtitlesRequest) (*subtv1.ListSubtitlesResponse, error) {
	f.listedID = req.GetMediaFileId()
	return &subtv1.ListSubtitlesResponse{
		Total: 1,
		Subtitles: []*subtv1.SubtitleFile{{
			Id: "sub1", MediaFileId: req.GetMediaFileId(), Language: "eng",
			Format: ".srt", Source: "sidecar", FilePath: "/data/movies/Fight.Club.1999.eng.srt",
			SizeBytes: 12000,
		}},
	}, nil
}

func (f *fixtureItemSubtitles) Delete(_ context.Context, req *subtv1.DeleteRequest) (*subtv1.DeleteResponse, error) {
	f.deleted = req.GetId()
	return &subtv1.DeleteResponse{}, nil
}

func (f *fixtureItemSubtitles) Upload(stream subtv1.SubtitleService_UploadServer) error {
	var mediaFileID, language string
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		switch d := msg.GetData().(type) {
		case *subtv1.UploadRequest_Metadata:
			var meta struct {
				MediaFileID string `json:"media_file_id"`
				Language    string `json:"language"`
			}
			_ = json.Unmarshal([]byte(d.Metadata), &meta)
			mediaFileID = meta.MediaFileID
			language = meta.Language
		}
	}
	f.uploaded = mediaFileID
	f.uploadedLang = language
	return stream.SendAndClose(&subtv1.UploadResponse{
		Subtitle: &subtv1.SubtitleFile{
			Id: "sub-new", MediaFileId: mediaFileID, Language: language, Source: "upload", Format: ".srt",
		},
	})
}

func (f *fixtureItemSubtitles) ListMedia(_ context.Context, req *subtv1.ListMediaRequest) (*subtv1.ListMediaResponse, error) {
	if req.GetSeriesId() == "" {
		return &subtv1.ListMediaResponse{}, nil
	}
	return &subtv1.ListMediaResponse{Items: f.media, Total: int32(len(f.media))}, nil
}

func (f *fixtureItemSubtitles) GetMedia(_ context.Context, req *subtv1.GetMediaRequest) (*subtv1.GetMediaResponse, error) {
	return &subtv1.GetMediaResponse{Item: &subtv1.SubtitleMediaItem{
		Id: req.GetId(), Title: "Fight Club", MediaFileId: "mf1", MediaType: "movie",
	}}, nil
}

type fixtureSubtitleMovies struct {
	mgmntv1.UnimplementedMovieManagementServiceServer
	movieID string
}

func (f *fixtureSubtitleMovies) ListFiles(_ context.Context, req *mgmntv1.ListFilesRequest) (*mgmntv1.ListFilesResponse, error) {
	f.movieID = req.GetMovieId()
	return &mgmntv1.ListFilesResponse{Files: []*mgmntv1.MovieFile{{
		Id: "mf1", MovieId: req.GetMovieId(), FilePath: "/data/movies/Fight.Club.1999.mkv",
	}}}, nil
}

func dialItemSubtitles(t *testing.T) (*fixtureItemSubtitles, *fixtureSubtitleMovies, *server) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	subs := &fixtureItemSubtitles{
		media: []*subtv1.SubtitleMediaItem{{
			Id: "ep1", Title: "Pilot", SeriesId: "s1", SeriesName: "Severance",
			MediaFileId: "ef1", Season: 1, Episode: 1, MediaType: "episode",
		}},
	}
	movies := &fixtureSubtitleMovies{}
	srv := grpc.NewServer()
	subtv1.RegisterSubtitleServiceServer(srv, subs)
	mgmntv1.RegisterMovieManagementServiceServer(srv, movies)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return subs, movies, &server{
		subtitles: subtv1.NewSubtitleServiceClient(conn),
		movies:    mgmntv1.NewMovieManagementServiceClient(conn),
		sessions:  newSessionStore(time.Hour),
	}
}

func privilegedItemSubtitles(t *testing.T, s *server, method, path, body string) *http.Request {
	t.Helper()
	tok, err := s.sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	return req
}

func TestHandleListMovieSubtitlesUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/movies/m1/subtitles", nil)
	req.SetPathValue("id", "m1")
	s.handleListMovieSubtitles(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["available"] != false {
		t.Fatalf("%v", body)
	}
}

func TestHandleListMovieSubtitles(t *testing.T) {
	fake, movies, s := dialItemSubtitles(t)
	req := httptest.NewRequest(http.MethodGet, "/api/movies/m1/subtitles", nil)
	req.SetPathValue("id", "m1")
	w := httptest.NewRecorder()
	s.handleListMovieSubtitles(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Items     []struct {
			ID       string `json:"id"`
			Language string `json:"language"`
			Filename string `json:"filename"`
		} `json:"items"`
		Files []struct {
			ID string `json:"id"`
		} `json:"files"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || fake.listedID != "mf1" || movies.movieID != "m1" {
		t.Fatalf("listed=%q movie=%q %#v", fake.listedID, movies.movieID, body)
	}
	if len(body.Items) != 1 || body.Items[0].ID != "sub1" || body.Items[0].Language != "eng" || body.Items[0].Filename != "Fight.Club.1999.eng.srt" {
		t.Fatalf("%#v", body.Items)
	}
	if len(body.Files) != 1 || body.Files[0].ID != "mf1" {
		t.Fatalf("files=%#v", body.Files)
	}
}

func TestHandleListTVSubtitles(t *testing.T) {
	fake, _, s := dialItemSubtitles(t)
	req := httptest.NewRequest(http.MethodGet, "/api/tv/s1/subtitles", nil)
	req.SetPathValue("id", "s1")
	w := httptest.NewRecorder()
	s.handleListTVSubtitles(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Items     []struct {
			ID string `json:"id"`
		} `json:"items"`
		Files []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"files"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || fake.listedID != "ef1" || len(body.Items) != 1 || body.Files[0].Title != "Pilot" {
		t.Fatalf("listed=%q %#v", fake.listedID, body)
	}
}

func TestHandleUploadAndDeleteItemSubtitle(t *testing.T) {
	fake, _, s := dialItemSubtitles(t)
	req := privilegedItemSubtitles(t, s, http.MethodPost, "/api/movies/m1/subtitles", `{"language":"spa","data":"MQogMDA6MDA6MDEsMDAwIC0tPiAwMDowMDowMiwwMDAKSG9sYQo="}`)
	req.SetPathValue("id", "m1")
	w := httptest.NewRecorder()
	s.handleUploadMovieSubtitles(w, req)
	if w.Code != http.StatusOK || fake.uploaded != "mf1" || fake.uploadedLang != "spa" {
		t.Fatalf("upload %d file=%q lang=%q %s", w.Code, fake.uploaded, fake.uploadedLang, w.Body.String())
	}
	req = privilegedItemSubtitles(t, s, http.MethodDelete, "/api/subtitles/files/sub1", "")
	req.SetPathValue("id", "sub1")
	w = httptest.NewRecorder()
	s.handleDeleteItemSubtitle(w, req)
	if w.Code != http.StatusOK || fake.deleted != "sub1" {
		t.Fatalf("delete %d id=%q %s", w.Code, fake.deleted, w.Body.String())
	}
}

func TestHandleDeleteItemSubtitleForbidden(t *testing.T) {
	_, _, s := dialItemSubtitles(t)
	req := httptest.NewRequest(http.MethodDelete, "/api/subtitles/files/sub1", nil)
	req.SetPathValue("id", "sub1")
	w := httptest.NewRecorder()
	s.handleDeleteItemSubtitle(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}
