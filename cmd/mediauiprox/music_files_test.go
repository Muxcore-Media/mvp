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

	musicv1 "github.com/Muxcore-Media/media-music/proto/gen/muxcore/music/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fixtureMusicFiles struct {
	musicv1.UnimplementedMusicManagementServiceServer
	artistID      string
	albumID       string
	fileIDs       []string
	removedFileID string
	deleteFiles   bool
	importedPath  string
}

func (f *fixtureMusicFiles) ListTrackFiles(_ context.Context, req *musicv1.ListTrackFilesRequest) (*musicv1.ListTrackFilesResponse, error) {
	f.artistID = req.GetArtistId()
	f.albumID = req.GetAlbumId()
	files := make([]*musicv1.TrackFile, 0, len(f.fileIDs))
	for _, id := range f.fileIDs {
		files = append(files, &musicv1.TrackFile{
			Id:        id,
			ArtistId:  req.GetArtistId(),
			AlbumId:   "al1",
			Title:     "One More Time",
			FilePath:  "/data/music/Daft.Punk/One.More.Time.flac",
			Quality:   "FLAC",
			SizeBytes: 40_000_000,
			Container: "flac",
		})
	}
	return &musicv1.ListTrackFilesResponse{Files: files}, nil
}

func (f *fixtureMusicFiles) RemoveTrackFile(_ context.Context, req *musicv1.RemoveTrackFileRequest) (*musicv1.RemoveTrackFileResponse, error) {
	f.removedFileID = req.GetFileId()
	f.deleteFiles = req.GetDeleteFiles()
	return &musicv1.RemoveTrackFileResponse{}, nil
}

func (f *fixtureMusicFiles) AddTrackFile(_ context.Context, req *musicv1.AddTrackFileRequest) (*musicv1.AddTrackFileResponse, error) {
	f.albumID = req.GetAlbumId()
	f.importedPath = req.GetFilePath()
	return &musicv1.AddTrackFileResponse{FileId: "tr-new"}, nil
}

func dialMusicFiles(t *testing.T) (*fixtureMusicFiles, musicv1.MusicManagementServiceClient) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixtureMusicFiles{fileIDs: []string{"tr1"}}
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

func TestHandleListMusicTrackFiles(t *testing.T) {
	fake, client := dialMusicFiles(t)
	s := &server{music: client}
	req := httptest.NewRequest(http.MethodGet, "/api/music/ar1/files?album_id=al1", nil)
	req.SetPathValue("id", "ar1")
	w := httptest.NewRecorder()
	s.handleListMusicTrackFiles(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if fake.artistID != "ar1" || fake.albumID != "al1" {
		t.Fatalf("artist=%s album=%s", fake.artistID, fake.albumID)
	}
	var body struct {
		Available bool `json:"available"`
		Items     []struct {
			ID       string `json:"id"`
			Filename string `json:"filename"`
		} `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || len(body.Items) != 1 || body.Items[0].ID != "tr1" || body.Items[0].Filename != "One.More.Time.flac" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleListMusicTrackFilesSoftFail(t *testing.T) {
	s := &server{}
	req := httptest.NewRequest(http.MethodGet, "/api/music/ar1/files", nil)
	req.SetPathValue("id", "ar1")
	w := httptest.NewRecorder()
	s.handleListMusicTrackFiles(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
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

func TestHandleDeleteMusicTrackFile(t *testing.T) {
	fake, client := dialMusicFiles(t)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{music: client, sessions: sessions}
	req := httptest.NewRequest(http.MethodDelete, "/api/music/ar1/files/tr1?delete_files=1", nil)
	req.SetPathValue("id", "ar1")
	req.SetPathValue("fileId", "tr1")
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleDeleteMusicTrackFile(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if fake.removedFileID != "tr1" || !fake.deleteFiles {
		t.Fatalf("file=%q delete=%v", fake.removedFileID, fake.deleteFiles)
	}
}

func TestHandleDeleteMusicTrackFileForbidden(t *testing.T) {
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("pat", "pat", "", []string{"user"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions}
	req := httptest.NewRequest(http.MethodDelete, "/api/music/ar1/files/tr1", nil)
	req.SetPathValue("id", "ar1")
	req.SetPathValue("fileId", "tr1")
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleDeleteMusicTrackFile(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestHandleDeleteMusicTrackFileNotFound(t *testing.T) {
	_, client := dialMusicFiles(t)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{music: client, sessions: sessions}
	req := httptest.NewRequest(http.MethodDelete, "/api/music/ar1/files/missing", nil)
	req.SetPathValue("id", "ar1")
	req.SetPathValue("fileId", "missing")
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleDeleteMusicTrackFile(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestHandleImportMusicAlbum(t *testing.T) {
	fake, client := dialMusicFiles(t)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{music: client, sessions: sessions}
	req := httptest.NewRequest(http.MethodPost, "/api/music/albums/al1/import", strings.NewReader(`{"path":"/data/music/One.More.Time.flac"}`))
	req.SetPathValue("id", "al1")
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleImportMusicAlbum(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if fake.albumID != "al1" || fake.importedPath != "/data/music/One.More.Time.flac" {
		t.Fatalf("album=%s path=%s", fake.albumID, fake.importedPath)
	}
	var body struct {
		Imported  bool   `json:"imported"`
		ID        string `json:"id"`
		StreamURL string `json:"stream_url"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Imported || body.ID != "tr-new" || body.StreamURL != "/stream/music/tr-new" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleImportMusicAlbumForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	req := httptest.NewRequest(http.MethodPost, "/api/music/albums/al1/import", strings.NewReader(`{"path":"/x"}`))
	req.SetPathValue("id", "al1")
	w := httptest.NewRecorder()
	s.handleImportMusicAlbum(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d", w.Code)
	}
}
