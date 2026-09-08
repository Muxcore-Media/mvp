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

	maintainv1 "github.com/Muxcore-Media/media-library-maintainer/proto/maintainv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fixtureMaintainer struct {
	maintainv1.UnimplementedMaintainerServiceServer
	scanned   bool
	dryRun    bool
	acted     bool
	freeUp    bool
	approved  string
	postponed string
	cancelled string
	upserted  string
	deleted   string
	toggled   string
	previewed bool
	protected string
	unprot    string
	colName   string
	colDel    string
	exclName  string
	exclDel   string
	synced    int32
	exported  bool
	imported  int32
}

func (f *fixtureMaintainer) ListCandidates(context.Context, *maintainv1.ListCandidatesRequest) (*maintainv1.ListCandidatesResponse, error) {
	return &maintainv1.ListCandidatesResponse{
		Total: 1, Page: 1, PageSize: 50,
		Candidates: []*maintainv1.Candidate{{
			Id: "c1", Title: "Old Movie", Year: 1999, ItemId: "m1",
			Status: maintainv1.CandidateStatus_CANDIDATE_STATUS_PENDING,
			ArrAction: maintainv1.ArrAction_ARR_ACTION_DELETE,
			Scope: maintainv1.MediaScope_MEDIA_SCOPE_MOVIE,
			SizeBytes: 12 << 30,
		}},
	}, nil
}

func (f *fixtureMaintainer) ListRuns(context.Context, *maintainv1.ListRunsRequest) (*maintainv1.ListRunsResponse, error) {
	return &maintainv1.ListRunsResponse{
		Total: 1,
		Runs: []*maintainv1.RunLog{{
			Id: "r1", Kind: "scan", Status: "ok", CandidatesFound: 1, DryRun: true,
		}},
	}, nil
}

func (f *fixtureMaintainer) GetStorageMetrics(context.Context, *maintainv1.GetStorageMetricsRequest) (*maintainv1.GetStorageMetricsResponse, error) {
	return &maintainv1.GetStorageMetricsResponse{
		Paths: []*maintainv1.StoragePathMetric{{
			Path: "/data/movies", FreePercent: 8.5, FreeBytes: 20 << 30, TotalBytes: 200 << 30, ItemCount: 40,
		}},
	}, nil
}

func (f *fixtureMaintainer) ListRules(context.Context, *maintainv1.ListRulesRequest) (*maintainv1.ListRulesResponse, error) {
	return &maintainv1.ListRulesResponse{Rules: []*maintainv1.RuleGroup{{
		Id: "rule1", Name: "Unwatched 90d", Enabled: true,
		Scope: maintainv1.MediaScope_MEDIA_SCOPE_MOVIE,
		ArrAction: maintainv1.ArrAction_ARR_ACTION_DELETE,
		CollectionId: "col1",
		DefinitionJson: `{"op":"and"}`,
	}}}, nil
}

func (f *fixtureMaintainer) ListCollections(context.Context, *maintainv1.ListCollectionsRequest) (*maintainv1.ListCollectionsResponse, error) {
	return &maintainv1.ListCollectionsResponse{Collections: []*maintainv1.Collection{{
		Id: "col1", Name: "Leaving soon", Enabled: true, GraceDays: 7,
		ArrAction: maintainv1.ArrAction_ARR_ACTION_DELETE,
		LeavingSoonEnabled: true, LeavingSoonLabel: "Leaving Soon",
	}}}, nil
}

func (f *fixtureMaintainer) UpsertCollection(_ context.Context, req *maintainv1.UpsertCollectionRequest) (*maintainv1.UpsertCollectionResponse, error) {
	col := req.GetCollection()
	if col.GetId() == "" {
		col.Id = "col-new"
	}
	f.colName = col.GetName()
	return &maintainv1.UpsertCollectionResponse{Collection: col}, nil
}

func (f *fixtureMaintainer) DeleteCollection(_ context.Context, req *maintainv1.DeleteCollectionRequest) (*maintainv1.DeleteCollectionResponse, error) {
	f.colDel = req.GetId()
	return &maintainv1.DeleteCollectionResponse{}, nil
}

func (f *fixtureMaintainer) ListExclusionLists(context.Context, *maintainv1.ListExclusionListsRequest) (*maintainv1.ListExclusionListsResponse, error) {
	return &maintainv1.ListExclusionListsResponse{Lists: []*maintainv1.ExclusionList{{
		Id: "excl1", Name: "Favorites", Type: "local", TmdbIds: []int32{550, 603},
	}}}, nil
}

func (f *fixtureMaintainer) UpsertExclusionList(_ context.Context, req *maintainv1.UpsertExclusionListRequest) (*maintainv1.UpsertExclusionListResponse, error) {
	list := req.GetList()
	if list.GetId() == "" {
		list.Id = "excl-new"
	}
	f.exclName = list.GetName()
	return &maintainv1.UpsertExclusionListResponse{List: list}, nil
}

func (f *fixtureMaintainer) DeleteExclusionList(_ context.Context, req *maintainv1.DeleteExclusionListRequest) (*maintainv1.DeleteExclusionListResponse, error) {
	f.exclDel = req.GetId()
	return &maintainv1.DeleteExclusionListResponse{}, nil
}

func (f *fixtureMaintainer) SyncExclusionLists(context.Context, *maintainv1.SyncExclusionListsRequest) (*maintainv1.SyncExclusionListsResponse, error) {
	f.synced = 1
	return &maintainv1.SyncExclusionListsResponse{ListsSynced: 1, IdsLoaded: 2}, nil
}

func (f *fixtureMaintainer) ExportRules(context.Context, *maintainv1.ExportRulesRequest) (*maintainv1.ExportRulesResponse, error) {
	f.exported = true
	return &maintainv1.ExportRulesResponse{RulesJson: `[{"name":"Unwatched 90d"}]`, RulesYaml: "name: Unwatched 90d\n"}, nil
}

func (f *fixtureMaintainer) ImportRules(_ context.Context, req *maintainv1.ImportRulesRequest) (*maintainv1.ImportRulesResponse, error) {
	f.imported = 1
	if req.GetRulesJson() == "" && req.GetRulesYaml() == "" {
		f.imported = 0
	}
	return &maintainv1.ImportRulesResponse{Imported: f.imported}, nil
}

func (f *fixtureMaintainer) GetRule(_ context.Context, req *maintainv1.GetRuleRequest) (*maintainv1.GetRuleResponse, error) {
	return &maintainv1.GetRuleResponse{Rule: &maintainv1.RuleGroup{
		Id: req.GetId(), Name: "Unwatched 90d", Enabled: true,
		Scope: maintainv1.MediaScope_MEDIA_SCOPE_MOVIE,
		ArrAction: maintainv1.ArrAction_ARR_ACTION_DELETE,
	}}, nil
}

func (f *fixtureMaintainer) UpsertRule(_ context.Context, req *maintainv1.UpsertRuleRequest) (*maintainv1.UpsertRuleResponse, error) {
	rule := req.GetRule()
	if rule.GetId() == "" {
		rule.Id = "rule-new"
	}
	f.upserted = rule.GetName()
	if rule.GetId() != "" && rule.GetId() != "rule-new" {
		f.toggled = rule.GetId()
	}
	return &maintainv1.UpsertRuleResponse{Rule: rule}, nil
}

func (f *fixtureMaintainer) DeleteRule(_ context.Context, req *maintainv1.DeleteRuleRequest) (*maintainv1.DeleteRuleResponse, error) {
	f.deleted = req.GetId()
	return &maintainv1.DeleteRuleResponse{}, nil
}

func (f *fixtureMaintainer) ListProtections(context.Context, *maintainv1.ListProtectionsRequest) (*maintainv1.ListProtectionsResponse, error) {
	return &maintainv1.ListProtectionsResponse{Protections: []*maintainv1.Protection{{
		Id: "p1", ItemId: "m1", Title: "Fight Club", Reason: "Household favorite",
		Scope: maintainv1.MediaScope_MEDIA_SCOPE_MOVIE,
	}}}, nil
}

func (f *fixtureMaintainer) UpsertProtection(_ context.Context, req *maintainv1.UpsertProtectionRequest) (*maintainv1.UpsertProtectionResponse, error) {
	p := req.GetProtection()
	if p.GetId() == "" {
		p.Id = "p-new"
	}
	f.protected = p.GetItemId()
	return &maintainv1.UpsertProtectionResponse{Protection: p}, nil
}

func (f *fixtureMaintainer) DeleteProtection(_ context.Context, req *maintainv1.DeleteProtectionRequest) (*maintainv1.DeleteProtectionResponse, error) {
	f.unprot = req.GetId()
	return &maintainv1.DeleteProtectionResponse{}, nil
}

func (f *fixtureMaintainer) PreviewRule(context.Context, *maintainv1.PreviewRuleRequest) (*maintainv1.PreviewRuleResponse, error) {
	f.previewed = true
	return &maintainv1.PreviewRuleResponse{
		Total: 1,
		Matches: []*maintainv1.Candidate{{Id: "c1", Title: "Old Movie"}},
	}, nil
}

func (f *fixtureMaintainer) ScanNow(_ context.Context, req *maintainv1.ScanNowRequest) (*maintainv1.ScanNowResponse, error) {
	f.scanned = true
	f.dryRun = req.GetDryRun()
	return &maintainv1.ScanNowResponse{
		CandidatesFound: 2,
		Run:             &maintainv1.RunLog{Id: "r2", Kind: "scan", DryRun: req.GetDryRun(), CandidatesFound: 2},
	}, nil
}

func (f *fixtureMaintainer) ActNow(_ context.Context, req *maintainv1.ActNowRequest) (*maintainv1.ActNowResponse, error) {
	f.acted = true
	f.freeUp = req.GetFreeUp()
	return &maintainv1.ActNowResponse{
		ActionsTaken: 1,
		Run:          &maintainv1.RunLog{Id: "r3", Kind: "act", ActionsTaken: 1, DryRun: req.GetDryRun()},
	}, nil
}

func (f *fixtureMaintainer) ApproveCandidate(_ context.Context, req *maintainv1.ApproveCandidateRequest) (*maintainv1.ApproveCandidateResponse, error) {
	f.approved = req.GetId()
	return &maintainv1.ApproveCandidateResponse{Candidate: &maintainv1.Candidate{
		Id: req.GetId(), Status: maintainv1.CandidateStatus_CANDIDATE_STATUS_APPROVED, Title: "Old Movie",
	}}, nil
}

func (f *fixtureMaintainer) PostponeCandidate(_ context.Context, req *maintainv1.PostponeCandidateRequest) (*maintainv1.PostponeCandidateResponse, error) {
	f.postponed = req.GetId()
	return &maintainv1.PostponeCandidateResponse{Candidate: &maintainv1.Candidate{
		Id: req.GetId(), Status: maintainv1.CandidateStatus_CANDIDATE_STATUS_POSTPONED, Title: "Old Movie",
	}}, nil
}

func (f *fixtureMaintainer) CancelCandidate(_ context.Context, req *maintainv1.CancelCandidateRequest) (*maintainv1.CancelCandidateResponse, error) {
	f.cancelled = req.GetId()
	return &maintainv1.CancelCandidateResponse{Candidate: &maintainv1.Candidate{
		Id: req.GetId(), Status: maintainv1.CandidateStatus_CANDIDATE_STATUS_CANCELLED, Title: "Old Movie",
	}}, nil
}

func dialMaintainer(t *testing.T) (*fixtureMaintainer, maintainv1.MaintainerServiceClient) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixtureMaintainer{}
	srv := grpc.NewServer()
	maintainv1.RegisterMaintainerServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return fake, maintainv1.NewMaintainerServiceClient(conn)
}

func privilegedMaintainer(t *testing.T, method, path, body string, client maintainv1.MaintainerServiceClient) (*server, *http.Request, *fixtureMaintainer) {
	t.Helper()
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{maintainer: client, sessions: sessions}
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	return s, req, nil
}

func TestHandleListMaintainerUnavailable(t *testing.T) {
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions}
	req := httptest.NewRequest(http.MethodGet, "/api/maintainer", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleListMaintainer(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["available"] != false {
		t.Fatalf("body=%v", body)
	}
}

func TestHandleListMaintainerForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	w := httptest.NewRecorder()
	s.handleListMaintainer(w, httptest.NewRequest(http.MethodGet, "/api/maintainer", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleListMaintainer(t *testing.T) {
	_, client := dialMaintainer(t)
	s, req, _ := privilegedMaintainer(t, http.MethodGet, "/api/maintainer", "", client)
	w := httptest.NewRecorder()
	s.handleListMaintainer(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available  bool `json:"available"`
		RulesTotal int  `json:"rules_total"`
		Candidates []struct {
			Title  string `json:"title"`
			Status string `json:"status"`
		} `json:"candidates"`
		Storage []struct {
			Path string `json:"path"`
		} `json:"storage"`
		Rules []struct {
			Name         string `json:"name"`
			CollectionID string `json:"collection_id"`
		} `json:"rules"`
		Protections []struct {
			Title string `json:"title"`
		} `json:"protections"`
		Collections []struct {
			Name      string `json:"name"`
			GraceDays int    `json:"grace_days"`
		} `json:"collections"`
		Exclusions []struct {
			Name      string `json:"name"`
			TmdbCount int    `json:"tmdb_count"`
			HasAPIKey bool   `json:"has_api_key"`
		} `json:"exclusions"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.RulesTotal != 1 || len(body.Candidates) != 1 || body.Candidates[0].Title != "Old Movie" || body.Candidates[0].Status != "pending" {
		t.Fatalf("%#v", body)
	}
	if len(body.Rules) != 1 || body.Rules[0].Name != "Unwatched 90d" || body.Rules[0].CollectionID != "col1" {
		t.Fatalf("rules=%#v", body.Rules)
	}
	if len(body.Protections) != 1 || body.Protections[0].Title != "Fight Club" {
		t.Fatalf("protections=%#v", body.Protections)
	}
	if len(body.Collections) != 1 || body.Collections[0].Name != "Leaving soon" || body.Collections[0].GraceDays != 7 {
		t.Fatalf("collections=%#v", body.Collections)
	}
	if len(body.Exclusions) != 1 || body.Exclusions[0].Name != "Favorites" || body.Exclusions[0].TmdbCount != 2 || body.Exclusions[0].HasAPIKey {
		t.Fatalf("exclusions=%#v", body.Exclusions)
	}
	if len(body.Storage) != 1 || body.Storage[0].Path != "/data/movies" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleMaintainerScanAndAct(t *testing.T) {
	fake, client := dialMaintainer(t)
	s, req, _ := privilegedMaintainer(t, http.MethodPost, "/api/maintainer/scan", `{}`, client)
	w := httptest.NewRecorder()
	s.handleMaintainerScan(w, req)
	if w.Code != http.StatusOK || !fake.scanned || !fake.dryRun {
		t.Fatalf("scan %d dry=%v %s", w.Code, fake.dryRun, w.Body.String())
	}
	s, req, _ = privilegedMaintainer(t, http.MethodPost, "/api/maintainer/act", `{"free_up":true}`, client)
	w = httptest.NewRecorder()
	s.handleMaintainerAct(w, req)
	if w.Code != http.StatusOK || !fake.acted || !fake.freeUp {
		t.Fatalf("act %d free=%v %s", w.Code, fake.freeUp, w.Body.String())
	}
}

func TestHandleMaintainerCandidateActions(t *testing.T) {
	fake, client := dialMaintainer(t)
	s, req, _ := privilegedMaintainer(t, http.MethodPost, "/api/maintainer/candidates/c1/approve", `{}`, client)
	req.SetPathValue("id", "c1")
	req.SetPathValue("action", "approve")
	w := httptest.NewRecorder()
	s.handleMaintainerCandidateAction(w, req)
	if w.Code != http.StatusOK || fake.approved != "c1" {
		t.Fatalf("approve %d id=%q %s", w.Code, fake.approved, w.Body.String())
	}
	s, req, _ = privilegedMaintainer(t, http.MethodPost, "/api/maintainer/candidates/c1/postpone", `{"days":14}`, client)
	req.SetPathValue("id", "c1")
	req.SetPathValue("action", "postpone")
	w = httptest.NewRecorder()
	s.handleMaintainerCandidateAction(w, req)
	if w.Code != http.StatusOK || fake.postponed != "c1" {
		t.Fatalf("postpone %d id=%q %s", w.Code, fake.postponed, w.Body.String())
	}
	s, req, _ = privilegedMaintainer(t, http.MethodPost, "/api/maintainer/candidates/c1/cancel", `{}`, client)
	req.SetPathValue("id", "c1")
	req.SetPathValue("action", "cancel")
	w = httptest.NewRecorder()
	s.handleMaintainerCandidateAction(w, req)
	if w.Code != http.StatusOK || fake.cancelled != "c1" {
		t.Fatalf("cancel %d id=%q %s", w.Code, fake.cancelled, w.Body.String())
	}
}

func TestHouseholdRuleDefinition(t *testing.T) {
	raw, err := householdRuleDefinition("stale_unwatched", 90)
	if err != nil || !strings.Contains(raw, "watch.never_watched") || !strings.Contains(raw, "90") {
		t.Fatalf("def=%q err=%v", raw, err)
	}
}

func TestParseHouseholdTmdbIDs(t *testing.T) {
	got := parseHouseholdTmdbIDs("550, 603\n13", []int32{272})
	if len(got) != 4 || got[0] != 272 || got[1] != 550 || got[2] != 603 || got[3] != 13 {
		t.Fatalf("%v", got)
	}
}

func TestHandleMaintainerExclusionsAndRulesIO(t *testing.T) {
	fake, client := dialMaintainer(t)
	s, req, _ := privilegedMaintainer(t, http.MethodPost, "/api/maintainer/exclusions", `{"name":"Never delete","type":"local","tmdb_ids_text":"550,603"}`, client)
	w := httptest.NewRecorder()
	s.handleUpsertMaintainerExclusion(w, req)
	if w.Code != http.StatusOK || fake.exclName != "Never delete" {
		t.Fatalf("upsert %d name=%q %s", w.Code, fake.exclName, w.Body.String())
	}
	var created struct {
		Exclusion struct {
			Type      string `json:"type"`
			TmdbCount int    `json:"tmdb_count"`
			HasAPIKey bool   `json:"has_api_key"`
		} `json:"exclusion"`
	}
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Exclusion.Type != "local" || created.Exclusion.TmdbCount != 2 || created.Exclusion.HasAPIKey {
		t.Fatalf("%#v", created)
	}
	s, req, _ = privilegedMaintainer(t, http.MethodPost, "/api/maintainer/exclusions/sync", `{}`, client)
	w = httptest.NewRecorder()
	s.handleSyncMaintainerExclusions(w, req)
	if w.Code != http.StatusOK || fake.synced != 1 {
		t.Fatalf("sync %d %s", w.Code, w.Body.String())
	}
	s, req, _ = privilegedMaintainer(t, http.MethodDelete, "/api/maintainer/exclusions/excl1", "", client)
	req.SetPathValue("id", "excl1")
	w = httptest.NewRecorder()
	s.handleDeleteMaintainerExclusion(w, req)
	if w.Code != http.StatusOK || fake.exclDel != "excl1" {
		t.Fatalf("delete %d id=%q %s", w.Code, fake.exclDel, w.Body.String())
	}
	s, req, _ = privilegedMaintainer(t, http.MethodGet, "/api/maintainer/rules/export", "", client)
	w = httptest.NewRecorder()
	s.handleExportMaintainerRules(w, req)
	if w.Code != http.StatusOK || !fake.exported {
		t.Fatalf("export %d %s", w.Code, w.Body.String())
	}
	s, req, _ = privilegedMaintainer(t, http.MethodPost, "/api/maintainer/rules/import", `{"rules_yaml":"name: Unwatched 90d\n"}`, client)
	w = httptest.NewRecorder()
	s.handleImportMaintainerRules(w, req)
	if w.Code != http.StatusOK || fake.imported != 1 {
		t.Fatalf("import %d imported=%d %s", w.Code, fake.imported, w.Body.String())
	}
}

func TestHandleMaintainerCollections(t *testing.T) {
	fake, client := dialMaintainer(t)
	s, req, _ := privilegedMaintainer(t, http.MethodPost, "/api/maintainer/collections", `{"name":"Leaving soon movies","grace_days":14}`, client)
	w := httptest.NewRecorder()
	s.handleUpsertMaintainerCollection(w, req)
	if w.Code != http.StatusOK || fake.colName != "Leaving soon movies" {
		t.Fatalf("upsert %d name=%q %s", w.Code, fake.colName, w.Body.String())
	}
	var created struct {
		Collection struct {
			Name               string `json:"name"`
			GraceDays          int    `json:"grace_days"`
			LeavingSoonEnabled bool   `json:"leaving_soon_enabled"`
			LeavingSoonLabel   string `json:"leaving_soon_label"`
		} `json:"collection"`
	}
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Collection.GraceDays != 14 || !created.Collection.LeavingSoonEnabled || created.Collection.LeavingSoonLabel != "Leaving Soon" {
		t.Fatalf("%#v", created)
	}
	s, req, _ = privilegedMaintainer(t, http.MethodDelete, "/api/maintainer/collections/col1", "", client)
	req.SetPathValue("id", "col1")
	w = httptest.NewRecorder()
	s.handleDeleteMaintainerCollection(w, req)
	if w.Code != http.StatusOK || fake.colDel != "col1" {
		t.Fatalf("delete %d id=%q %s", w.Code, fake.colDel, w.Body.String())
	}
}

func TestHandleMaintainerRules(t *testing.T) {
	fake, client := dialMaintainer(t)
	s, req, _ := privilegedMaintainer(t, http.MethodPost, "/api/maintainer/rules", `{"name":"Stale unwatched movies","preset":"stale_unwatched","days":90,"scope":"movie","action":"delete","collection_id":"col1"}`, client)
	w := httptest.NewRecorder()
	s.handleUpsertMaintainerRule(w, req)
	if w.Code != http.StatusOK || fake.upserted != "Stale unwatched movies" {
		t.Fatalf("upsert %d name=%q %s", w.Code, fake.upserted, w.Body.String())
	}
	var saved struct {
		Rule struct {
			CollectionID string `json:"collection_id"`
		} `json:"rule"`
	}
	if err := json.NewDecoder(w.Body).Decode(&saved); err != nil {
		t.Fatal(err)
	}
	if saved.Rule.CollectionID != "col1" {
		t.Fatalf("collection_id=%q", saved.Rule.CollectionID)
	}
	s, req, _ = privilegedMaintainer(t, http.MethodPost, "/api/maintainer/rules/preview", `{"preset":"stale_unwatched","days":90}`, client)
	w = httptest.NewRecorder()
	s.handlePreviewMaintainerRule(w, req)
	if w.Code != http.StatusOK || !fake.previewed {
		t.Fatalf("preview %d %s", w.Code, w.Body.String())
	}
	s, req, _ = privilegedMaintainer(t, http.MethodPost, "/api/maintainer/rules/rule1/toggle", `{}`, client)
	req.SetPathValue("id", "rule1")
	w = httptest.NewRecorder()
	s.handleToggleMaintainerRule(w, req)
	if w.Code != http.StatusOK || fake.toggled != "rule1" {
		t.Fatalf("toggle %d id=%q %s", w.Code, fake.toggled, w.Body.String())
	}
	s, req, _ = privilegedMaintainer(t, http.MethodDelete, "/api/maintainer/rules/rule1", "", client)
	req.SetPathValue("id", "rule1")
	w = httptest.NewRecorder()
	s.handleDeleteMaintainerRule(w, req)
	if w.Code != http.StatusOK || fake.deleted != "rule1" {
		t.Fatalf("delete %d id=%q %s", w.Code, fake.deleted, w.Body.String())
	}
}

func TestHandleMaintainerProtections(t *testing.T) {
	fake, client := dialMaintainer(t)
	s, req, _ := privilegedMaintainer(t, http.MethodPost, "/api/maintainer/protections", `{"item_id":"m1","title":"Fight Club","scope":"movie"}`, client)
	w := httptest.NewRecorder()
	s.handleUpsertMaintainerProtection(w, req)
	if w.Code != http.StatusOK || fake.protected != "m1" {
		t.Fatalf("protect %d id=%q %s", w.Code, fake.protected, w.Body.String())
	}
	s, req, _ = privilegedMaintainer(t, http.MethodDelete, "/api/maintainer/protections/p1", "", client)
	req.SetPathValue("id", "p1")
	w = httptest.NewRecorder()
	s.handleDeleteMaintainerProtection(w, req)
	if w.Code != http.StatusOK || fake.unprot != "p1" {
		t.Fatalf("unprotect %d id=%q %s", w.Code, fake.unprot, w.Body.String())
	}
}
