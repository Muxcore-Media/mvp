package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	automationv1 "github.com/Muxcore-Media/contracts-automation/muxcore/automation/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fixtureAutomation struct {
	automationv1.UnimplementedAutomationServiceServer
	searchQuery    string
	searchType     string
	grabbedGUID    string
	blockedGUID    string
	blockedItemID  string
	retriedHistory string
	removedQueue   string
	wantedMissing  bool
	addedWanted    *automationv1.AddToQueueRequest
}

func (f *fixtureAutomation) SearchItem(_ context.Context, req *automationv1.SearchItemRequest) (*automationv1.SearchItemResponse, error) {
	f.searchQuery = req.GetQuery()
	f.searchType = req.GetItemType()
	return &automationv1.SearchItemResponse{
		Matches: []*automationv1.ReleaseMatch{
			{
				Guid: "low", Title: "Movie.720p.WEB", IndexerName: "fixture",
				DownloadProtocol: "torrent", Size: 2e9, Score: 10, Seeders: 40,
				DownloadUrl: "http://example.test/low",
			},
			{
				Guid: "high", Title: "Movie.2160p.BluRay.REMUX", IndexerName: "fixture",
				DownloadProtocol: "torrent", Size: 40e9, Score: 210, Seeders: 12,
				DownloadUrl: "http://example.test/high",
			},
		},
	}, nil
}

func (f *fixtureAutomation) Dispatch(_ context.Context, req *automationv1.DispatchRequest) (*automationv1.DispatchResponse, error) {
	f.grabbedGUID = req.GetGuid()
	return &automationv1.DispatchResponse{DownloadId: "dl-1", Status: "queued"}, nil
}

func (f *fixtureAutomation) ListCutoffUnmet(_ context.Context, _ *automationv1.ListCutoffUnmetRequest) (*automationv1.ListCutoffUnmetResponse, error) {
	return &automationv1.ListCutoffUnmetResponse{
		Items: []*automationv1.CutoffItem{
			{QueueId: "q-ok", ItemType: "movie", ItemId: "m-ok", Title: "Almost There", Year: 2022, CurrentScore: 90, CutoffScore: 100},
			{QueueId: "q-far", ItemType: "tv", ItemId: "s-far", Title: "Needs Upgrade", Year: 2021, CurrentScore: 10, CutoffScore: 200},
		},
		Total: 2, Page: 1, PageSize: 50,
	}, nil
}

func (f *fixtureAutomation) SearchNow(_ context.Context, req *automationv1.SearchNowRequest) (*automationv1.SearchNowResponse, error) {
	f.searchQuery = req.GetItemId()
	return &automationv1.SearchNowResponse{Started: true, Message: "wanted search started"}, nil
}

func (f *fixtureAutomation) BlocklistRelease(_ context.Context, req *automationv1.BlocklistReleaseRequest) (*automationv1.BlocklistReleaseResponse, error) {
	f.blockedGUID = req.GetGuid()
	f.blockedItemID = req.GetWantedItemId()
	return &automationv1.BlocklistReleaseResponse{Success: true}, nil
}

func (f *fixtureAutomation) GetHistory(_ context.Context, _ *automationv1.GetHistoryRequest) (*automationv1.GetHistoryResponse, error) {
	return &automationv1.GetHistoryResponse{
		Records: []*automationv1.DownloadRecord{
			{Id: "h-fail", WantedItemId: "q-1", Guid: "g-fail", Title: "Broken Import", Status: "import_failed", StatusLabel: "Import failed", StatusDetail: "path not watched"},
			{Id: "h-ok", WantedItemId: "q-2", Guid: "g-ok", Title: "Grabbed Movie", Status: "completed", StatusLabel: "Completed"},
		},
		Total: 2, Page: 1, PageSize: 50,
	}, nil
}

func (f *fixtureAutomation) GetQueue(_ context.Context, req *automationv1.GetQueueRequest) (*automationv1.GetQueueResponse, error) {
	f.wantedMissing = req.GetMissing()
	return &automationv1.GetQueueResponse{
		Items: []*automationv1.QueueItem{
			{Id: "q-miss", ItemType: "movie", ItemId: "m-miss", Title: "Still Missing", Year: 2024, Monitored: true, Missing: true},
		},
		Total: 1, Page: 1, PageSize: 50,
	}, nil
}

func (f *fixtureAutomation) RetryImport(_ context.Context, req *automationv1.RetryImportRequest) (*automationv1.RetryImportResponse, error) {
	f.retriedHistory = req.GetHistoryId()
	return &automationv1.RetryImportResponse{Attempted: 1, Message: "ok"}, nil
}

func (f *fixtureAutomation) RemoveFromQueue(_ context.Context, req *automationv1.RemoveFromQueueRequest) (*automationv1.RemoveFromQueueResponse, error) {
	f.removedQueue = req.GetQueueId()
	return &automationv1.RemoveFromQueueResponse{}, nil
}

func (f *fixtureAutomation) AddToQueue(_ context.Context, req *automationv1.AddToQueueRequest) (*automationv1.AddToQueueResponse, error) {
	f.addedWanted = req
	return &automationv1.AddToQueueResponse{QueueId: "w_" + req.GetItemType() + "_" + req.GetItemId()}, nil
}

func dialAutomationFixture(t *testing.T, impl *fixtureAutomation) automationv1.AutomationServiceClient {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	automationv1.RegisterAutomationServiceServer(srv, impl)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return automationv1.NewAutomationServiceClient(conn)
}

func TestReleaseSearchUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handleReleaseSearch(w, httptest.NewRequest(http.MethodGet, "/api/releases/search?q=Dune", nil))
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
		t.Fatal("expected unavailable")
	}
}

func TestReleaseSearchScoresDescending(t *testing.T) {
	fix := &fixtureAutomation{}
	s := &server{automation: dialAutomationFixture(t, fix)}
	w := httptest.NewRecorder()
	s.handleReleaseSearch(w, httptest.NewRequest(http.MethodGet, "/api/releases/search?q=Dune&type=movie", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Items     []releaseMatchJSON
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || len(body.Items) != 2 {
		t.Fatalf("%#v", body)
	}
	if body.Items[0].GUID != "high" || body.Items[0].Score != 210 {
		t.Fatalf("want remux first, got %#v", body.Items[0])
	}
	if fix.searchQuery != "Dune" {
		t.Fatalf("query=%q", fix.searchQuery)
	}
}

func TestReleaseSearchParsesQuality(t *testing.T) {
	fix := &fixtureAutomation{}
	fake := &stubFormatsClient{}
	s := &server{automation: dialAutomationFixture(t, fix), formats: fake}
	w := httptest.NewRecorder()
	s.handleReleaseSearch(w, httptest.NewRequest(http.MethodGet, "/api/releases/search?q=Dune&type=movie", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Items []releaseMatchJSON
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) == 0 || body.Items[0].Quality == nil {
		t.Fatalf("quality %#v", body.Items)
	}
	if label, _ := body.Items[0].Quality["label"].(string); label != "1080p Remux" {
		t.Fatalf("label %#v", body.Items[0].Quality)
	}
}

func TestReleaseSearchAcceptsLibraryPlusTypes(t *testing.T) {
	fix := &fixtureAutomation{}
	s := &server{automation: dialAutomationFixture(t, fix)}
	w := httptest.NewRecorder()
	s.handleReleaseSearch(w, httptest.NewRequest(http.MethodGet, "/api/releases/search?q=Radiohead&type=music", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fix.searchType != "music" || fix.searchQuery != "Radiohead" {
		t.Fatalf("search type=%q query=%q", fix.searchType, fix.searchQuery)
	}
	w = httptest.NewRecorder()
	s.handleReleaseSearch(w, httptest.NewRequest(http.MethodGet, "/api/releases/search?q=X&type=podcast", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d", w.Code)
	}
}

func TestReleaseGrabDispatches(t *testing.T) {
	fix := &fixtureAutomation{}
	s := &server{automation: dialAutomationFixture(t, fix)}
	body := bytes.NewBufferString(`{"guid":"high","title":"Movie.REMUX","item_type":"movie","item_id":"m1","score":210}`)
	w := httptest.NewRecorder()
	s.handleReleaseGrab(w, httptest.NewRequest(http.MethodPost, "/api/releases/grab", body))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if fix.grabbedGUID != "high" {
		t.Fatalf("guid=%q", fix.grabbedGUID)
	}
	var out struct {
		DownloadID string `json:"download_id"`
		Status     string `json:"status"`
	}
	if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.DownloadID != "dl-1" || out.Status != "queued" {
		t.Fatalf("%#v", out)
	}
}

func TestCutoffUnmetUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handleCutoffUnmet(w, httptest.NewRequest(http.MethodGet, "/api/releases/upgrades", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Available bool `json:"available"`
		Total     int  `json:"total"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available || body.Total != 0 {
		t.Fatalf("%#v", body)
	}
}

func TestCutoffUnmetWorstGapFirst(t *testing.T) {
	s := &server{automation: dialAutomationFixture(t, &fixtureAutomation{})}
	w := httptest.NewRecorder()
	s.handleCutoffUnmet(w, httptest.NewRequest(http.MethodGet, "/api/releases/upgrades", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool
		Items     []cutoffItemJSON
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || len(body.Items) != 2 {
		t.Fatalf("%#v", body)
	}
	if body.Items[0].ItemID != "s-far" || body.Items[0].CutoffScore-body.Items[0].CurrentScore != 190 {
		t.Fatalf("want largest gap first, got %#v", body.Items[0])
	}
}

func TestSearchNowStartsCycle(t *testing.T) {
	fix := &fixtureAutomation{}
	s := &server{automation: dialAutomationFixture(t, fix)}
	body := bytes.NewBufferString(`{"item_type":"movie","item_id":"m-ok"}`)
	w := httptest.NewRecorder()
	s.handleSearchNow(w, httptest.NewRequest(http.MethodPost, "/api/releases/search-now", body))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if fix.searchQuery != "m-ok" {
		t.Fatalf("item_id=%q", fix.searchQuery)
	}
	var out struct {
		Started bool   `json:"started"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if !out.Started || out.Message == "" {
		t.Fatalf("%#v", out)
	}
}

func TestReleaseBlocklistsGUID(t *testing.T) {
	fix := &fixtureAutomation{}
	s := &server{automation: dialAutomationFixture(t, fix)}
	body := bytes.NewBufferString(`{"guid":"low","item_id":"m1","reason":"wrong edition"}`)
	w := httptest.NewRecorder()
	s.handleReleaseBlock(w, httptest.NewRequest(http.MethodPost, "/api/releases/block", body))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if fix.blockedGUID != "low" || fix.blockedItemID != "m1" {
		t.Fatalf("blocked guid=%q item=%q", fix.blockedGUID, fix.blockedItemID)
	}
}
