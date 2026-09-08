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

	rootsv1 "github.com/Muxcore-Media/media-root-folders/proto/rootsv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fixtureRoots struct {
	rootsv1.UnimplementedRootFolderServiceServer
	kind       string
	browsePath string
	created    *rootsv1.CreateRootRequest
	updated    *rootsv1.UpdateRootRequest
	deletedID  string
	probed     string
	pickedKind string
	roots      []*rootsv1.RootFolder
}

func (f *fixtureRoots) ListRoots(_ context.Context, req *rootsv1.ListRootsRequest) (*rootsv1.ListRootsResponse, error) {
	f.kind = req.GetMediaKind()
	return &rootsv1.ListRootsResponse{Roots: f.roots}, nil
}

func (f *fixtureRoots) BrowsePath(_ context.Context, req *rootsv1.BrowsePathRequest) (*rootsv1.BrowsePathResponse, error) {
	f.browsePath = req.GetPath()
	path := req.GetPath()
	if path == "" {
		path = "/"
	}
	return &rootsv1.BrowsePathResponse{
		Path:   path,
		Parent: "/data",
		Entries: []*rootsv1.BrowseEntry{
			{Name: "movies", Path: "/data/movies", IsDir: true},
		},
	}, nil
}

func (f *fixtureRoots) PickRoot(_ context.Context, req *rootsv1.PickRootRequest) (*rootsv1.PickRootResponse, error) {
	f.pickedKind = req.GetMediaKind()
	if len(f.roots) == 0 {
		return &rootsv1.PickRootResponse{}, nil
	}
	return &rootsv1.PickRootResponse{Root: f.roots[0]}, nil
}

func (f *fixtureRoots) ProbeRoot(_ context.Context, req *rootsv1.ProbeRootRequest) (*rootsv1.ProbeRootResponse, error) {
	f.probed = req.GetPath()
	return &rootsv1.ProbeRootResponse{
		Path: req.GetPath(), Accessible: true, FreeBytes: 120_000_000_000, TotalBytes: 500_000_000_000,
	}, nil
}

func (f *fixtureRoots) CreateRoot(_ context.Context, req *rootsv1.CreateRootRequest) (*rootsv1.CreateRootResponse, error) {
	f.created = req
	return &rootsv1.CreateRootResponse{Root: &rootsv1.RootFolder{
		Id: "r-new", Path: req.GetPath(), Name: req.GetName(), MediaKind: req.GetMediaKind(), IsDefault: req.GetIsDefault(),
	}}, nil
}

func (f *fixtureRoots) UpdateRoot(_ context.Context, req *rootsv1.UpdateRootRequest) (*rootsv1.UpdateRootResponse, error) {
	f.updated = req
	return &rootsv1.UpdateRootResponse{Root: &rootsv1.RootFolder{
		Id: req.GetId(), Path: req.GetPath(), Name: req.GetName(), MediaKind: req.GetMediaKind(),
		IsDefault: req.GetIsDefault(), NamingTemplateId: req.GetNamingTemplateId(),
	}}, nil
}

func (f *fixtureRoots) DeleteRoot(_ context.Context, req *rootsv1.DeleteRootRequest) (*rootsv1.DeleteRootResponse, error) {
	f.deletedID = req.GetId()
	return &rootsv1.DeleteRootResponse{}, nil
}

func dialRoots(t *testing.T, roots []*rootsv1.RootFolder) (*fixtureRoots, rootsv1.RootFolderServiceClient) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixtureRoots{roots: roots}
	srv := grpc.NewServer()
	rootsv1.RegisterRootFolderServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return fake, rootsv1.NewRootFolderServiceClient(conn)
}

func TestHandleListRootsUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handleListRoots(w, httptest.NewRequest(http.MethodGet, "/api/roots", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Available bool  `json:"available"`
		Roots     []any `json:"roots"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available || body.Roots == nil {
		t.Fatalf("%#v", body)
	}
}

func TestHandleListRootsFiltersKind(t *testing.T) {
	fake, client := dialRoots(t, []*rootsv1.RootFolder{{
		Id: "r1", Path: "/data/movies", Name: "Movies", MediaKind: "movies", IsDefault: true, Accessible: true,
	}})
	s := &server{roots: client}
	w := httptest.NewRecorder()
	s.handleListRoots(w, httptest.NewRequest(http.MethodGet, "/api/roots?kind=movies", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.kind != "movies" {
		t.Fatalf("kind %q", fake.kind)
	}
	var body struct {
		Available bool `json:"available"`
		Roots     []struct {
			ID        string `json:"id"`
			Path      string `json:"path"`
			IsDefault bool   `json:"is_default"`
		} `json:"roots"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || len(body.Roots) != 1 || body.Roots[0].Path != "/data/movies" || !body.Roots[0].IsDefault {
		t.Fatalf("%#v", body)
	}
}

func privilegedRootsRequest(t *testing.T, method, path, body string) (*server, *http.Request, *fixtureRoots) {
	t.Helper()
	fake, client := dialRoots(t, nil)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{roots: client, sessions: sessions}
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	return s, req, fake
}

func TestHandleBrowseRootsForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	w := httptest.NewRecorder()
	s.handleBrowseRoots(w, httptest.NewRequest(http.MethodGet, "/api/roots/browse?path=/data", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleBrowseRootsUnavailable(t *testing.T) {
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions}
	req := httptest.NewRequest(http.MethodGet, "/api/roots/browse?path=/data", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleBrowseRoots(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Available bool  `json:"available"`
		Entries   []any `json:"entries"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available || body.Entries == nil {
		t.Fatalf("%#v", body)
	}
}

func TestHandleBrowseRootsListsDirs(t *testing.T) {
	s, req, fake := privilegedRootsRequest(t, http.MethodGet, "/api/roots/browse?path=/data", "")
	w := httptest.NewRecorder()
	s.handleBrowseRoots(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.browsePath != "/data" {
		t.Fatalf("path %q", fake.browsePath)
	}
	var body struct {
		Available bool `json:"available"`
		Entries   []struct {
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || len(body.Entries) != 1 || body.Entries[0].Path != "/data/movies" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleCreateRootMovieFolder(t *testing.T) {
	s, req, fake := privilegedRootsRequest(t, http.MethodPost, "/api/roots", `{"path":"/data/uhd","name":"UHD","media_kind":"movies","is_default":true}`)
	w := httptest.NewRecorder()
	s.handleCreateRoot(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.created == nil || fake.created.GetPath() != "/data/uhd" || !fake.created.GetIsDefault() {
		t.Fatalf("%#v", fake.created)
	}
	var body struct {
		Root struct {
			Path string `json:"path"`
			Name string `json:"name"`
		} `json:"root"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Root.Path != "/data/uhd" || body.Root.Name != "UHD" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleProbeRootForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	w := httptest.NewRecorder()
	s.handleProbeRoot(w, httptest.NewRequest(http.MethodPost, "/api/roots/probe", strings.NewReader(`{"path":"/data/movies"}`)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleProbeRoot(t *testing.T) {
	s, req, fake := privilegedRootsRequest(t, http.MethodPost, "/api/roots/probe", `{"path":"/data/movies"}`)
	w := httptest.NewRecorder()
	s.handleProbeRoot(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.probed != "/data/movies" {
		t.Fatalf("probed %q", fake.probed)
	}
	var body struct {
		Available  bool  `json:"available"`
		Accessible bool  `json:"accessible"`
		FreeBytes  int64 `json:"free_bytes"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || !body.Accessible || body.FreeBytes != 120_000_000_000 {
		t.Fatalf("%#v", body)
	}
}

func TestHandleProbeRootRequiresPath(t *testing.T) {
	s, req, _ := privilegedRootsRequest(t, http.MethodPost, "/api/roots/probe", `{}`)
	w := httptest.NewRecorder()
	s.handleProbeRoot(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandlePatchRootForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	w := httptest.NewRecorder()
	s.handlePatchRoot(w, httptest.NewRequest(http.MethodPatch, "/api/roots/r1", strings.NewReader(`{"name":"UHD"}`)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandlePatchRoot(t *testing.T) {
	s, req, fake := privilegedRootsRequest(t, http.MethodPatch, "/api/roots/r1", `{"name":"UHD","mediaKind":"movies","isDefault":true}`)
	req.SetPathValue("id", "r1")
	w := httptest.NewRecorder()
	s.handlePatchRoot(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.updated == nil || fake.updated.GetId() != "r1" || fake.updated.GetName() != "UHD" || fake.updated.GetMediaKind() != "movies" || !fake.updated.GetIsDefault() {
		t.Fatalf("updated %#v", fake.updated)
	}
}

func TestHandleDeleteRoot(t *testing.T) {
	s, req, fake := privilegedRootsRequest(t, http.MethodDelete, "/api/roots/r1", "")
	req.SetPathValue("id", "r1")
	w := httptest.NewRecorder()
	s.handleDeleteRoot(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.deletedID != "r1" {
		t.Fatalf("deleted %q", fake.deletedID)
	}
}

func TestHandlePickRootRequiresKind(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handlePickRoot(w, httptest.NewRequest(http.MethodGet, "/api/roots/pick", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandlePickRootUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handlePickRoot(w, httptest.NewRequest(http.MethodGet, "/api/roots/pick?kind=movies", nil))
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
		t.Fatalf("%#v", body)
	}
}

func TestHandlePickRootDefaultMovieFolder(t *testing.T) {
	fake, client := dialRoots(t, []*rootsv1.RootFolder{{
		Id: "r1", Path: "/data/movies", Name: "Movies", MediaKind: "movies", IsDefault: true, Accessible: true,
	}})
	s := &server{roots: client}
	w := httptest.NewRecorder()
	s.handlePickRoot(w, httptest.NewRequest(http.MethodGet, "/api/roots/pick?kind=movies", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.pickedKind != "movies" {
		t.Fatalf("kind %q", fake.pickedKind)
	}
	var body struct {
		Available bool `json:"available"`
		Root      struct {
			Path      string `json:"path"`
			IsDefault bool   `json:"is_default"`
		} `json:"root"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.Root.Path != "/data/movies" || !body.Root.IsDefault {
		t.Fatalf("%#v", body)
	}
}
