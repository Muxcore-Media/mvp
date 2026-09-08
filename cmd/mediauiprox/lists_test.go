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

	listsyncv1 "github.com/Muxcore-Media/media-list-sync/proto/listsyncv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fixtureListSources struct {
	listsyncv1.UnimplementedListSyncServiceServer
	created *listsyncv1.AddSourceRequest
	updated *listsyncv1.UpdateSourceRequest
	deleted   string
	synced    bool
	syncedID  string
	testedID  string
	paused    bool
}

func (f *fixtureListSources) ListSources(context.Context, *listsyncv1.ListSourcesRequest) (*listsyncv1.ListSourcesResponse, error) {
	return &listsyncv1.ListSourcesResponse{Sources: []*listsyncv1.ListSource{{
		Id: "ls1", Name: "Trakt watchlist", Type: "trakt", Enabled: !f.paused, Username: "sam",
		ListUrl: "https://trakt.tv/users/sam/watchlist", SyncIntervalMinutes: 1440, ApiKey: "secret",
	}}}, nil
}

func (f *fixtureListSources) AddSource(_ context.Context, req *listsyncv1.AddSourceRequest) (*listsyncv1.AddSourceResponse, error) {
	f.created = req
	return &listsyncv1.AddSourceResponse{Source: &listsyncv1.ListSource{
		Id: "ls-new", Name: req.GetName(), Type: req.GetType(), Enabled: true,
		ListUrl: req.GetListUrl(), QualityProfileId: req.GetQualityProfileId(),
	}}, nil
}

func (f *fixtureListSources) UpdateSource(_ context.Context, req *listsyncv1.UpdateSourceRequest) (*listsyncv1.UpdateSourceResponse, error) {
	f.updated = req
	if req.Enabled != nil {
		f.paused = !req.GetEnabled()
	}
	return &listsyncv1.UpdateSourceResponse{Source: &listsyncv1.ListSource{
		Id: req.GetId(), Name: "Trakt watchlist", Type: "trakt", Enabled: !f.paused,
	}}, nil
}

func (f *fixtureListSources) RemoveSource(_ context.Context, req *listsyncv1.RemoveSourceRequest) (*listsyncv1.RemoveSourceResponse, error) {
	f.deleted = req.GetId()
	return &listsyncv1.RemoveSourceResponse{}, nil
}

func (f *fixtureListSources) SyncNow(_ context.Context, req *listsyncv1.SyncNowRequest) (*listsyncv1.SyncNowResponse, error) {
	f.synced = true
	f.syncedID = req.GetSourceId()
	return &listsyncv1.SyncNowResponse{ItemsFound: 12, ItemsNew: 3}, nil
}

func (f *fixtureListSources) TestSource(_ context.Context, req *listsyncv1.TestSourceRequest) (*listsyncv1.TestSourceResponse, error) {
	f.testedID = req.GetSourceId()
	return &listsyncv1.TestSourceResponse{Ok: true, Message: "Found 4 titles", ItemsFound: 4}, nil
}

func (f *fixtureListSources) GetHistory(context.Context, *listsyncv1.GetHistoryRequest) (*listsyncv1.GetHistoryResponse, error) {
	return &listsyncv1.GetHistoryResponse{
		Total: 1,
		Entries: []*listsyncv1.SyncLogEntry{{
			Id: "log1", SourceId: "ls1", SourceName: "Trakt watchlist", Status: "ok",
			ItemsFound: 12, ItemsNew: 3, StartedAt: "2026-09-08T10:00:00Z",
		}},
	}, nil
}

func (f *fixtureListSources) GetItems(_ context.Context, req *listsyncv1.GetItemsRequest) (*listsyncv1.GetItemsResponse, error) {
	return &listsyncv1.GetItemsResponse{
		Total: 1, Page: 1, PageSize: 50,
		Items: []*listsyncv1.SyncItem{{
			Id: "it1", SourceId: req.GetSourceId(), Title: "Fight Club", Year: 1999,
			MediaType: "movie", Status: "added", Action: "watchlist", TmdbId: 550,
		}},
	}, nil
}

func privilegedListsRequest(t *testing.T, method, path, body string) (*server, *http.Request, *fixtureListSources) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixtureListSources{}
	srv := grpc.NewServer()
	listsyncv1.RegisterListSyncServiceServer(srv, fake)
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
	s := &server{listSync: listsyncv1.NewListSyncServiceClient(conn), sessions: sessions}
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	return s, req, fake
}

func TestHandleListSourcesForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	w := httptest.NewRecorder()
	s.handleListSources(w, httptest.NewRequest(http.MethodGet, "/api/lists", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleListSourcesUnavailable(t *testing.T) {
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions}
	req := httptest.NewRequest(http.MethodGet, "/api/lists", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleListSources(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Available bool  `json:"available"`
		Sources   []any `json:"sources"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available || body.Sources == nil {
		t.Fatalf("%#v", body)
	}
}

func TestHandleListSourcesHidesAPIKey(t *testing.T) {
	s, req, _ := privilegedListsRequest(t, http.MethodGet, "/api/lists", "")
	w := httptest.NewRecorder()
	s.handleListSources(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "secret") {
		t.Fatal("api key leaked")
	}
	var body struct {
		Available bool `json:"available"`
		Sources   []struct {
			Name      string `json:"name"`
			HasAPIKey bool   `json:"has_api_key"`
		} `json:"sources"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.Sources[0].Name != "Trakt watchlist" || !body.Sources[0].HasAPIKey {
		t.Fatalf("%#v", body)
	}
}

func TestHandleCreateListSource(t *testing.T) {
	s, req, fake := privilegedListsRequest(t, http.MethodPost, "/api/lists", `{"name":"IMDb Top","type":"imdb","list_url":"https://www.imdb.com/list/ls1","quality_profile_id":"qp1"}`)
	w := httptest.NewRecorder()
	s.handleCreateListSource(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.created == nil || fake.created.GetType() != "imdb" || fake.created.GetQualityProfileId() != "qp1" {
		t.Fatalf("created %#v", fake.created)
	}
}

func TestHandleUpdateListSourceEnabled(t *testing.T) {
	s, req, fake := privilegedListsRequest(t, http.MethodPatch, "/api/lists/ls1", `{"enabled":false}`)
	req.SetPathValue("id", "ls1")
	w := httptest.NewRecorder()
	s.handleUpdateListSource(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.updated == nil || fake.updated.GetId() != "ls1" || fake.updated.Enabled == nil || fake.updated.GetEnabled() {
		t.Fatalf("updated %#v", fake.updated)
	}
	if strings.Contains(w.Body.String(), "secret") {
		t.Fatal("api key leaked")
	}
	var body struct {
		Source struct {
			Enabled bool `json:"enabled"`
		} `json:"source"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Source.Enabled {
		t.Fatalf("%#v", body)
	}
}

func TestHandleDeleteListSource(t *testing.T) {
	s, req, fake := privilegedListsRequest(t, http.MethodDelete, "/api/lists/ls1", "")
	req.SetPathValue("id", "ls1")
	w := httptest.NewRecorder()
	s.handleDeleteListSource(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.deleted != "ls1" {
		t.Fatalf("deleted %q", fake.deleted)
	}
}

func TestHandleSyncListSources(t *testing.T) {
	s, req, fake := privilegedListsRequest(t, http.MethodPost, "/api/lists/sync", "")
	w := httptest.NewRecorder()
	s.handleSyncListSources(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if !fake.synced {
		t.Fatal("expected SyncNow")
	}
	if fake.syncedID != "" {
		t.Fatalf("global sync leaked source %q", fake.syncedID)
	}
}

func TestHandleSyncListSource(t *testing.T) {
	s, req, fake := privilegedListsRequest(t, http.MethodPost, "/api/lists/ls1/sync", "")
	req.SetPathValue("id", "ls1")
	w := httptest.NewRecorder()
	s.handleSyncListSource(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if !fake.synced || fake.syncedID != "ls1" {
		t.Fatalf("synced=%v id=%q", fake.synced, fake.syncedID)
	}
}

func TestHandleSyncListSourceForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	w := httptest.NewRecorder()
	s.handleSyncListSource(w, httptest.NewRequest(http.MethodPost, "/api/lists/ls1/sync", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleTestListSource(t *testing.T) {
	s, req, fake := privilegedListsRequest(t, http.MethodPost, "/api/lists/ls1/test", "")
	req.SetPathValue("id", "ls1")
	w := httptest.NewRecorder()
	s.handleTestListSource(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.testedID != "ls1" {
		t.Fatalf("tested %q", fake.testedID)
	}
	if fake.synced {
		t.Fatal("test must not call SyncNow")
	}
	var body struct {
		OK         bool   `json:"ok"`
		Message    string `json:"message"`
		ItemsFound int    `json:"items_found"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || body.ItemsFound != 4 || body.Message == "" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleTestListSourceForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	w := httptest.NewRecorder()
	s.handleTestListSource(w, httptest.NewRequest(http.MethodPost, "/api/lists/ls1/test", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleListSyncHistoryForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	w := httptest.NewRecorder()
	s.handleListSyncHistory(w, httptest.NewRequest(http.MethodGet, "/api/lists/history", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleListSyncHistoryUnavailable(t *testing.T) {
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions}
	req := httptest.NewRequest(http.MethodGet, "/api/lists/history", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleListSyncHistory(w, req)
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

func TestHandleListSyncHistory(t *testing.T) {
	s, req, _ := privilegedListsRequest(t, http.MethodGet, "/api/lists/history", "")
	w := httptest.NewRecorder()
	s.handleListSyncHistory(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Entries   []struct {
			SourceName string `json:"source_name"`
			ItemsNew   int    `json:"items_new"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.Entries[0].SourceName != "Trakt watchlist" || body.Entries[0].ItemsNew != 3 {
		t.Fatalf("%#v", body)
	}
}

func TestHandleListSyncItems(t *testing.T) {
	s, req, _ := privilegedListsRequest(t, http.MethodGet, "/api/lists/items?source_id=ls1", "")
	w := httptest.NewRecorder()
	s.handleListSyncItems(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Items     []struct {
			Title  string `json:"title"`
			Status string `json:"status"`
		} `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.Items[0].Title != "Fight Club" || body.Items[0].Status != "added" {
		t.Fatalf("%#v", body)
	}
}
