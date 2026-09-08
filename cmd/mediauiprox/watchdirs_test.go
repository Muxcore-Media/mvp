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
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fixtureWatchDirs struct {
	scannerv1.UnimplementedScannerServiceServer
	added   *scannerv1.AddWatchDirRequest
	updated *scannerv1.UpdateWatchDirRequest
	enabled *scannerv1.SetWatchDirEnabledRequest
	deleted string
	paused  bool
}

func (f *fixtureWatchDirs) ListWatchDirs(context.Context, *scannerv1.ListWatchDirsRequest) (*scannerv1.ListWatchDirsResponse, error) {
	return &scannerv1.ListWatchDirsResponse{Dirs: []*scannerv1.WatchDir{{
		Id: "wd1", Path: "/downloads", MediaType: "both", LibraryPath: "/data/movies", Enabled: !f.paused,
	}}}, nil
}

func (f *fixtureWatchDirs) UpdateWatchDir(_ context.Context, req *scannerv1.UpdateWatchDirRequest) (*scannerv1.UpdateWatchDirResponse, error) {
	f.updated = req
	return &scannerv1.UpdateWatchDirResponse{}, nil
}

func (f *fixtureWatchDirs) SetWatchDirEnabled(_ context.Context, req *scannerv1.SetWatchDirEnabledRequest) (*scannerv1.SetWatchDirEnabledResponse, error) {
	f.enabled = req
	f.paused = !req.GetEnabled()
	return &scannerv1.SetWatchDirEnabledResponse{}, nil
}

func (f *fixtureWatchDirs) AddWatchDir(_ context.Context, req *scannerv1.AddWatchDirRequest) (*scannerv1.AddWatchDirResponse, error) {
	f.added = req
	return &scannerv1.AddWatchDirResponse{Id: "wd-new"}, nil
}

func (f *fixtureWatchDirs) RemoveWatchDir(_ context.Context, req *scannerv1.RemoveWatchDirRequest) (*scannerv1.RemoveWatchDirResponse, error) {
	f.deleted = req.GetId()
	return &scannerv1.RemoveWatchDirResponse{}, nil
}

func dialWatchDirs(t *testing.T) (*fixtureWatchDirs, scannerv1.ScannerServiceClient) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixtureWatchDirs{}
	srv := grpc.NewServer()
	scannerv1.RegisterScannerServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return fake, scannerv1.NewScannerServiceClient(conn)
}

func privilegedWatchDirsRequest(t *testing.T, method, path, body string) (*server, *http.Request, *fixtureWatchDirs) {
	t.Helper()
	fake, client := dialWatchDirs(t)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{scanner: client, sessions: sessions}
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	return s, req, fake
}

func TestHandleListWatchDirsForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	w := httptest.NewRecorder()
	s.handleListWatchDirs(w, httptest.NewRequest(http.MethodGet, "/api/scan/watch-dirs", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleListWatchDirsUnavailable(t *testing.T) {
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions}
	req := httptest.NewRequest(http.MethodGet, "/api/scan/watch-dirs", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleListWatchDirs(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Available bool  `json:"available"`
		Dirs      []any `json:"dirs"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available || body.Dirs == nil {
		t.Fatalf("%#v", body)
	}
}

func TestHandleListWatchDirs(t *testing.T) {
	s, req, _ := privilegedWatchDirsRequest(t, http.MethodGet, "/api/scan/watch-dirs", "")
	w := httptest.NewRecorder()
	s.handleListWatchDirs(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Dirs      []struct {
			Path string `json:"path"`
		} `json:"dirs"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.Dirs[0].Path != "/downloads" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleCreateWatchDir(t *testing.T) {
	s, req, fake := privilegedWatchDirsRequest(t, http.MethodPost, "/api/scan/watch-dirs", `{"path":"/downloads/tv","media_type":"tv","library_path":"/data/tv"}`)
	w := httptest.NewRecorder()
	s.handleCreateWatchDir(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.added == nil || fake.added.GetPath() != "/downloads/tv" || fake.added.GetMediaType() != "tv" {
		t.Fatalf("added %#v", fake.added)
	}
}

func TestHandleUpdateWatchDirEnabled(t *testing.T) {
	s, req, fake := privilegedWatchDirsRequest(t, http.MethodPatch, "/api/scan/watch-dirs/wd1", `{"enabled":false}`)
	req.SetPathValue("id", "wd1")
	w := httptest.NewRecorder()
	s.handleUpdateWatchDir(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.enabled == nil || fake.enabled.GetId() != "wd1" || fake.enabled.GetEnabled() {
		t.Fatalf("enabled %#v", fake.enabled)
	}
	var body struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
		Updated bool   `json:"updated"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Updated || body.Enabled {
		t.Fatalf("%#v", body)
	}
}

func TestHandleUpdateWatchDirPaths(t *testing.T) {
	s, req, fake := privilegedWatchDirsRequest(t, http.MethodPatch, "/api/scan/watch-dirs/wd1", `{"media_type":"tv","library_path":"/data/tv"}`)
	req.SetPathValue("id", "wd1")
	w := httptest.NewRecorder()
	s.handleUpdateWatchDir(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.updated == nil || fake.updated.GetId() != "wd1" || fake.updated.GetMediaType() != "tv" || fake.updated.GetLibraryPath() != "/data/tv" {
		t.Fatalf("updated %#v", fake.updated)
	}
}

func TestHandleDeleteWatchDir(t *testing.T) {
	s, req, fake := privilegedWatchDirsRequest(t, http.MethodDelete, "/api/scan/watch-dirs/wd1", "")
	req.SetPathValue("id", "wd1")
	w := httptest.NewRecorder()
	s.handleDeleteWatchDir(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.deleted != "wd1" {
		t.Fatalf("deleted %q", fake.deleted)
	}
}
