package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	musicv1 "github.com/Muxcore-Media/media-music/proto/gen/muxcore/music/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fixtureMusicMonitor struct {
	musicv1.UnimplementedMusicManagementServiceServer
	artistID         string
	artistMon        bool
	qualityProfileID string
	rootFolderPath   string
	albumID          string
	albumMon         bool
	addedAlbum       *musicv1.AddAlbumRequest
	addedArtist      *musicv1.AddArtistRequest
}

func (f *fixtureMusicMonitor) UpdateArtist(_ context.Context, req *musicv1.UpdateArtistRequest) (*musicv1.UpdateArtistResponse, error) {
	f.artistID = req.GetId()
	if req.Monitored != nil {
		f.artistMon = req.GetMonitored()
	}
	if req.QualityProfileId != nil {
		f.qualityProfileID = req.GetQualityProfileId()
	}
	if req.RootFolderPath != nil {
		f.rootFolderPath = req.GetRootFolderPath()
	}
	return &musicv1.UpdateArtistResponse{Artist: &musicv1.Artist{
		Id: req.GetId(), Monitored: f.artistMon,
		QualityProfileId: f.qualityProfileID, RootFolderPath: f.rootFolderPath,
	}}, nil
}

func (f *fixtureMusicMonitor) UpdateAlbumMonitored(_ context.Context, req *musicv1.UpdateAlbumMonitoredRequest) (*musicv1.UpdateAlbumMonitoredResponse, error) {
	f.albumID = req.GetAlbumId()
	f.albumMon = req.GetMonitored()
	return &musicv1.UpdateAlbumMonitoredResponse{}, nil
}

func (f *fixtureMusicMonitor) AddArtist(_ context.Context, req *musicv1.AddArtistRequest) (*musicv1.AddArtistResponse, error) {
	f.addedArtist = req
	return &musicv1.AddArtistResponse{Artist: &musicv1.Artist{
		Id: "ar-new", Name: req.GetName(), Monitored: req.GetMonitored(),
		QualityProfileId: req.GetQualityProfileId(), RootFolderPath: req.GetRootFolderPath(),
	}}, nil
}

func (f *fixtureMusicMonitor) AddAlbum(_ context.Context, req *musicv1.AddAlbumRequest) (*musicv1.AddAlbumResponse, error) {
	f.addedAlbum = req
	return &musicv1.AddAlbumResponse{Album: &musicv1.Album{
		Id: "al-new", ArtistId: req.GetArtistId(), Title: req.GetTitle(),
		Year: req.GetYear(), Monitored: req.GetMonitored(),
	}}, nil
}

func dialMusicMonitor(t *testing.T) (*fixtureMusicMonitor, musicv1.MusicManagementServiceClient) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixtureMusicMonitor{artistMon: true, albumMon: true}
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

func TestHandlePatchMusicArtist(t *testing.T) {
	fake, client := dialMusicMonitor(t)
	s := &server{music: client}
	req := httptest.NewRequest(http.MethodPatch, "/api/music/ar1", bytes.NewBufferString(`{"monitored":false}`))
	req.SetPathValue("id", "ar1")
	w := httptest.NewRecorder()
	s.handlePatchMusicArtist(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.artistID != "ar1" || fake.artistMon {
		t.Fatalf("artist=%s mon=%v", fake.artistID, fake.artistMon)
	}
	var body struct {
		Monitored bool `json:"monitored"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Monitored {
		t.Fatal("expected unmonitored")
	}
}

func TestHandlePatchMusicArtistQualityProfile(t *testing.T) {
	fake, client := dialMusicMonitor(t)
	s := &server{music: client}
	req := httptest.NewRequest(http.MethodPatch, "/api/music/ar1", bytes.NewBufferString(`{"quality_profile_id":"qp_uhd"}`))
	req.SetPathValue("id", "ar1")
	w := httptest.NewRecorder()
	s.handlePatchMusicArtist(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.artistID != "ar1" || fake.qualityProfileID != "qp_uhd" {
		t.Fatalf("artist=%s qp=%s", fake.artistID, fake.qualityProfileID)
	}
	var body struct {
		QualityProfileID string `json:"quality_profile_id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.QualityProfileID != "qp_uhd" {
		t.Fatalf("qp=%s", body.QualityProfileID)
	}
}

func TestHandlePatchMusicArtistRootFolder(t *testing.T) {
	fake, client := dialMusicMonitor(t)
	s := &server{music: client}
	req := httptest.NewRequest(http.MethodPatch, "/api/music/ar1", bytes.NewBufferString(`{"root_folder_path":"/data/music"}`))
	req.SetPathValue("id", "ar1")
	w := httptest.NewRecorder()
	s.handlePatchMusicArtist(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.artistID != "ar1" || fake.rootFolderPath != "/data/music" {
		t.Fatalf("artist=%s root=%s", fake.artistID, fake.rootFolderPath)
	}
	var body struct {
		RootFolderPath string `json:"root_folder_path"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.RootFolderPath != "/data/music" {
		t.Fatalf("root=%s", body.RootFolderPath)
	}
}

func TestHandlePatchMusicAlbum(t *testing.T) {
	fake, client := dialMusicMonitor(t)
	s := &server{music: client}
	req := httptest.NewRequest(http.MethodPatch, "/api/music/albums/al1", bytes.NewBufferString(`{"monitored":false}`))
	req.SetPathValue("id", "al1")
	w := httptest.NewRecorder()
	s.handlePatchMusicAlbum(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.albumID != "al1" || fake.albumMon {
		t.Fatalf("album=%s mon=%v", fake.albumID, fake.albumMon)
	}
}

func TestHandleDeleteMusicArtist(t *testing.T) {
	fake := &fixtureMusicRemove{}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	musicv1.RegisterMusicManagementServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	s := &server{music: musicv1.NewMusicManagementServiceClient(conn)}
	req := httptest.NewRequest(http.MethodDelete, "/api/music/ar1?delete_files=1", nil)
	req.SetPathValue("id", "ar1")
	w := httptest.NewRecorder()
	s.handleDeleteMusicArtist(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.id != "ar1" || !fake.deleteFiles {
		t.Fatalf("id=%s delete=%v", fake.id, fake.deleteFiles)
	}
	var body struct {
		Removed     bool `json:"removed"`
		DeleteFiles bool `json:"delete_files"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Removed || !body.DeleteFiles {
		t.Fatalf("unexpected body %+v", body)
	}
}

type fixtureMusicRemove struct {
	musicv1.UnimplementedMusicManagementServiceServer
	id          string
	deleteFiles bool
	refreshedID string
}

func (f *fixtureMusicRemove) RemoveArtist(_ context.Context, req *musicv1.RemoveArtistRequest) (*musicv1.RemoveArtistResponse, error) {
	f.id = req.GetId()
	f.deleteFiles = req.GetDeleteFiles()
	return &musicv1.RemoveArtistResponse{}, nil
}

func (f *fixtureMusicRemove) RefreshMetadata(_ context.Context, req *musicv1.RefreshMetadataRequest) (*musicv1.RefreshMetadataResponse, error) {
	f.refreshedID = req.GetArtistId()
	return &musicv1.RefreshMetadataResponse{}, nil
}

func TestHandleRefreshMusicArtist(t *testing.T) {
	fake := &fixtureMusicRemove{}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	musicv1.RegisterMusicManagementServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	s := &server{music: musicv1.NewMusicManagementServiceClient(conn)}
	req := httptest.NewRequest(http.MethodPost, "/api/music/ar1/refresh", nil)
	req.SetPathValue("id", "ar1")
	w := httptest.NewRecorder()
	s.handleRefreshMusicArtist(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.refreshedID != "ar1" {
		t.Fatalf("refreshed=%s", fake.refreshedID)
	}
	var body struct {
		Refreshed bool `json:"refreshed"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Refreshed {
		t.Fatal("expected refreshed")
	}
}

func TestHandleRefreshMusicArtistUnavailable(t *testing.T) {
	s := &server{}
	req := httptest.NewRequest(http.MethodPost, "/api/music/ar1/refresh", nil)
	req.SetPathValue("id", "ar1")
	w := httptest.NewRecorder()
	s.handleRefreshMusicArtist(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandlePatchMusicAlbumUnavailable(t *testing.T) {
	s := &server{}
	req := httptest.NewRequest(http.MethodPatch, "/api/music/albums/al1", bytes.NewBufferString(`{"monitored":true}`))
	req.SetPathValue("id", "al1")
	w := httptest.NewRecorder()
	s.handlePatchMusicAlbum(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleAddMusicAlbum(t *testing.T) {
	fake, client := dialMusicMonitor(t)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{music: client, sessions: sessions}
	req := httptest.NewRequest(http.MethodPost, "/api/music/ar1/albums", bytes.NewBufferString(`{"title":"Homework","year":1997}`))
	req.SetPathValue("id", "ar1")
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleAddMusicAlbum(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.addedAlbum == nil || fake.addedAlbum.GetArtistId() != "ar1" || fake.addedAlbum.GetTitle() != "Homework" || fake.addedAlbum.GetYear() != 1997 || !fake.addedAlbum.GetMonitored() {
		t.Fatalf("%+v", fake.addedAlbum)
	}
	var body struct {
		Added bool `json:"added"`
		Album struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			Year  int32  `json:"year"`
		} `json:"album"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Added || body.Album.ID != "al-new" || body.Album.Title != "Homework" || body.Album.Year != 1997 {
		t.Fatalf("%#v", body)
	}
}

func TestHandleAddMusicAlbumForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	req := httptest.NewRequest(http.MethodPost, "/api/music/ar1/albums", bytes.NewBufferString(`{"title":"Homework"}`))
	req.SetPathValue("id", "ar1")
	w := httptest.NewRecorder()
	s.handleAddMusicAlbum(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleAddMusicArtist(t *testing.T) {
	fake, client := dialMusicMonitor(t)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{music: client, sessions: sessions}
	req := httptest.NewRequest(http.MethodPost, "/api/music", bytes.NewBufferString(`{"name":"Daft Punk"}`))
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleAddMusicArtist(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.addedArtist == nil || fake.addedArtist.GetName() != "Daft Punk" || !fake.addedArtist.GetMonitored() {
		t.Fatalf("%+v", fake.addedArtist)
	}
	var body struct {
		Added  bool `json:"added"`
		Artist struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"artist"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Added || body.Artist.ID != "ar-new" || body.Artist.Name != "Daft Punk" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleAddMusicArtistForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	req := httptest.NewRequest(http.MethodPost, "/api/music", bytes.NewBufferString(`{"name":"Daft Punk"}`))
	w := httptest.NewRecorder()
	s.handleAddMusicArtist(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}
