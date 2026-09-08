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

	renamev1 "github.com/Muxcore-Media/media-rename/proto/renamev1"
	rootsv1 "github.com/Muxcore-Media/media-root-folders/proto/rootsv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fixtureOrganizeRename struct {
	renamev1.UnimplementedRenameServiceServer
	req *renamev1.BatchRenameRequest
}

func (f *fixtureOrganizeRename) BatchRename(_ context.Context, req *renamev1.BatchRenameRequest) (*renamev1.BatchRenameResponse, error) {
	f.req = req
	return &renamev1.BatchRenameResponse{
		Total: 1, Renamed: 1,
		Results: []*renamev1.RenameResult{{
			Original: "Fight.Club.1999.mkv", RenamedTo: "/data/movies/Fight Club (1999)/Fight Club (1999).mkv", Success: true,
		}},
	}, nil
}

func dialOrganizeRename(t *testing.T) (*fixtureOrganizeRename, renamev1.RenameServiceClient) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixtureOrganizeRename{}
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

func privilegedOrganizeRequest(t *testing.T, body string, withRoots bool) (*server, *http.Request, *fixtureOrganizeRename) {
	t.Helper()
	fake, renameClient := dialOrganizeRename(t)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{rename: renameClient, sessions: sessions}
	if withRoots {
		_, rootsClient := dialRoots(t, []*rootsv1.RootFolder{{
			Id: "r1", Path: "/data/movies", Name: "Movies", MediaKind: "movies",
		}})
		s.roots = rootsClient
	}
	req := httptest.NewRequest(http.MethodPost, "/api/rename/organize", bytes.NewBufferString(body))
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	return s, req, fake
}

func TestOrganizePathAllowed(t *testing.T) {
	allowed := []string{"/data/movies", "/downloads"}
	if !organizePathAllowed("/data/movies", allowed) {
		t.Fatal("root itself must be allowed")
	}
	if !organizePathAllowed("/data/movies/inbox", allowed) {
		t.Fatal("child of root must be allowed")
	}
	if organizePathAllowed("/data/movies-uhd", allowed) {
		t.Fatal("prefix without separator must be rejected")
	}
	if organizePathAllowed("/", allowed) {
		t.Fatal("filesystem root must be rejected")
	}
	if organizePathAllowed("/etc", allowed) {
		t.Fatal("unrelated path must be rejected")
	}
	if organizePathAllowed("movies", allowed) {
		t.Fatal("relative path must be rejected")
	}
}

func TestHandleOrganizeLibraryForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	w := httptest.NewRecorder()
	s.handleOrganizeLibrary(w, httptest.NewRequest(http.MethodPost, "/api/rename/organize", bytes.NewBufferString(`{"directory":"/data/movies"}`)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleOrganizeLibraryUnavailable(t *testing.T) {
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions}
	req := httptest.NewRequest(http.MethodPost, "/api/rename/organize", bytes.NewBufferString(`{"directory":"/data/movies"}`))
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleOrganizeLibrary(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleOrganizeLibraryRequiresRoot(t *testing.T) {
	s, req, _ := privilegedOrganizeRequest(t, `{"directory":"/data/movies","dry_run":true}`, false)
	w := httptest.NewRecorder()
	s.handleOrganizeLibrary(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("organize.root_required")) {
		t.Fatalf("body=%s", w.Body.String())
	}
}

func TestHandleOrganizeLibraryRejectsOutsideRoot(t *testing.T) {
	s, req, fake := privilegedOrganizeRequest(t, `{"directory":"/etc","dry_run":true}`, true)
	w := httptest.NewRecorder()
	s.handleOrganizeLibrary(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	if fake.req != nil {
		t.Fatal("BatchRename must not run for a path outside roots")
	}
}

func TestHandleOrganizeLibraryDryRun(t *testing.T) {
	s, req, fake := privilegedOrganizeRequest(t, `{"directory":"/data/movies/inbox","media_type":"movie","dry_run":true}`, true)
	w := httptest.NewRecorder()
	s.handleOrganizeLibrary(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	if fake.req == nil || fake.req.GetDirectory() != "/data/movies/inbox" || !fake.req.GetDryRun() {
		t.Fatalf("batch=%v", fake.req)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["available"] != true || body["dry_run"] != true || body["renamed"] != float64(1) {
		t.Fatalf("body=%v", body)
	}
}

func TestHandleOrganizeLibraryWatchDir(t *testing.T) {
	fake, renameClient := dialOrganizeRename(t)
	_, scanner := dialWatchDirs(t)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("manager", "manager", "", []string{"manager"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{rename: renameClient, scanner: scanner, sessions: sessions}
	req := httptest.NewRequest(http.MethodPost, "/api/rename/organize", bytes.NewBufferString(`{"directory":"/downloads","media_type":"tv","dry_run":false}`))
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleOrganizeLibrary(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	if fake.req == nil || fake.req.GetDirectory() != "/downloads" || fake.req.GetDryRun() || fake.req.GetMediaType() != "tv" {
		t.Fatalf("batch=%v", fake.req)
	}
}
