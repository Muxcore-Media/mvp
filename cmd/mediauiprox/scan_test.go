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

type fixtureLibraryScan struct {
	scannerv1.UnimplementedScannerServiceServer
	watch   bool
	roots   bool
	path    string
	media   string
}

func (f *fixtureLibraryScan) GetStats(context.Context, *scannerv1.GetStatsRequest) (*scannerv1.GetStatsResponse, error) {
	return &scannerv1.GetStatsResponse{
		TotalImported: 12, WatchDirs: 2, LastScanAt: 1700000000,
		LastScanStatus: "completed", LastScanFilesFound: 4, LastScanFilesImported: 3, LastScanFilesSkipped: 1,
	}, nil
}

func (f *fixtureLibraryScan) Scan(_ context.Context, req *scannerv1.ScanRequest) (*scannerv1.ScanResponse, error) {
	f.watch = true
	f.path = req.GetPath()
	f.media = req.GetMediaType()
	return &scannerv1.ScanResponse{FilesFound: 5, FilesImported: 4, FilesSkipped: 1}, nil
}

func (f *fixtureLibraryScan) ScanLibraryRoots(_ context.Context, req *scannerv1.ScanLibraryRootsRequest) (*scannerv1.ScanLibraryRootsResponse, error) {
	f.roots = true
	f.path = req.GetPath()
	f.media = req.GetMediaType()
	return &scannerv1.ScanLibraryRootsResponse{FilesFound: 2, FilesImported: 2}, nil
}

func privilegedScanRequest(t *testing.T, method, path, body string) (*server, *http.Request, *fixtureLibraryScan) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixtureLibraryScan{}
	srv := grpc.NewServer()
	scannerv1.RegisterScannerServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{scanner: scannerv1.NewScannerServiceClient(conn), sessions: sessions}
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	return s, req, fake
}

func TestHandleLibraryScanStatusForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	w := httptest.NewRecorder()
	s.handleLibraryScanStatus(w, httptest.NewRequest(http.MethodGet, "/api/scan", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleLibraryScanStatusUnavailable(t *testing.T) {
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions}
	req := httptest.NewRequest(http.MethodGet, "/api/scan", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleLibraryScanStatus(w, req)
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
		t.Fatal("expected available=false")
	}
}

func TestHandleLibraryScanStatus(t *testing.T) {
	s, req, _ := privilegedScanRequest(t, http.MethodGet, "/api/scan", "")
	w := httptest.NewRecorder()
	s.handleLibraryScanStatus(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available     bool   `json:"available"`
		Scanner       bool   `json:"scanner"`
		Status        string `json:"status"`
		WatchDirs     int32  `json:"watch_dirs"`
		TotalImported int32  `json:"total_imported"`
		LastFound     int32  `json:"last_found"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || !body.Scanner || body.Status != "idle" || body.WatchDirs != 2 || body.TotalImported != 12 || body.LastFound != 4 {
		t.Fatalf("%#v", body)
	}
}

func TestHandleLibraryScanWatch(t *testing.T) {
	s, req, fake := privilegedScanRequest(t, http.MethodPost, "/api/scan", `{"type":"watch","media_type":"movie"}`)
	w := httptest.NewRecorder()
	s.handleLibraryScan(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if !fake.watch || fake.media != "movie" {
		t.Fatalf("watch=%v media=%q", fake.watch, fake.media)
	}
	var body struct {
		Type          string `json:"type"`
		FilesImported int32  `json:"files_imported"`
		Message       string `json:"message"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Type != "watch" || body.FilesImported != 4 || !strings.Contains(body.Message, "watch scan") {
		t.Fatalf("%#v", body)
	}
}

func TestHandleLibraryScanRoots(t *testing.T) {
	s, req, fake := privilegedScanRequest(t, http.MethodPost, "/api/scan", `{"type":"library_roots"}`)
	w := httptest.NewRecorder()
	s.handleLibraryScan(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if !fake.roots {
		t.Fatal("expected ScanLibraryRoots")
	}
}

func TestHandleLibraryScanRootsIncludesPlus(t *testing.T) {
	plus := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/scan" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"files_found": 3, "files_imported": 2, "files_skipped": 1})
	}))
	t.Cleanup(plus.Close)
	s, req, fake := privilegedScanRequest(t, http.MethodPost, "/api/scan", `{"type":"library_roots"}`)
	s.booksHTTP = mustURL(plus.URL)
	w := httptest.NewRecorder()
	s.handleLibraryScan(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if !fake.roots {
		t.Fatal("expected ScanLibraryRoots")
	}
	var body struct {
		FilesImported int32 `json:"files_imported"`
		Libraries     []struct {
			Library   string `json:"library"`
			Available bool   `json:"available"`
			Imported  int32  `json:"files_imported"`
		} `json:"libraries"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.FilesImported != 4 { // scanner 2 + books 2
		t.Fatalf("imported=%d %#v", body.FilesImported, body)
	}
	var booksOK bool
	for _, lib := range body.Libraries {
		if lib.Library == "books" && lib.Available && lib.Imported == 2 {
			booksOK = true
		}
	}
	if !booksOK || !strings.Contains(body.Message, "books imported=2") {
		t.Fatalf("%#v", body)
	}
}

func TestHandleLibraryScanPlusWithoutScanner(t *testing.T) {
	plus := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"files_found": 1, "files_imported": 1, "files_skipped": 0})
	}))
	t.Cleanup(plus.Close)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions, musicHTTP: mustURL(plus.URL)}
	req := httptest.NewRequest(http.MethodPost, "/api/scan", strings.NewReader(`{"type":"library_roots","media_type":"music"}`))
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleLibraryScan(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		FilesImported int32  `json:"files_imported"`
		Message       string `json:"message"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.FilesImported != 1 || !strings.Contains(body.Message, "music imported=1") {
		t.Fatalf("%#v", body)
	}
}

func TestHandleLibraryScanStatusPlusWithoutScanner(t *testing.T) {
	plus := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/artists" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]any{})
	}))
	t.Cleanup(plus.Close)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions, musicHTTP: mustURL(plus.URL)}
	req := httptest.NewRequest(http.MethodGet, "/api/scan", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleLibraryScanStatus(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Status    string `json:"status"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.Status != "idle" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleLibraryScanForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	w := httptest.NewRecorder()
	s.handleLibraryScan(w, httptest.NewRequest(http.MethodPost, "/api/scan", strings.NewReader(`{}`)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}
