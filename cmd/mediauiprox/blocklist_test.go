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

type fixtureBlocklist struct {
	automationv1.UnimplementedAutomationServiceServer
	clearedAll  bool
	clearedID   string
	clearedGUID string
}

func (f *fixtureBlocklist) ListBlocklist(_ context.Context, _ *automationv1.ListBlocklistRequest) (*automationv1.ListBlocklistResponse, error) {
	return &automationv1.ListBlocklistResponse{
		Entries: []*automationv1.BlocklistEntry{
			{WantedItemId: "q1", Guid: "g-bad", Title: "CAM.Rip", Reason: "household", Loop: 1, CreatedAt: "2026-09-08T00:00:00Z"},
		},
		Total: 1, Page: 1, PageSize: 50,
	}, nil
}

func (f *fixtureBlocklist) ClearBlocklist(_ context.Context, req *automationv1.ClearBlocklistRequest) (*automationv1.ClearBlocklistResponse, error) {
	f.clearedAll = req.GetClearAll()
	f.clearedID = req.GetWantedItemId()
	f.clearedGUID = req.GetGuid()
	return &automationv1.ClearBlocklistResponse{Removed: 1}, nil
}

func dialBlocklist(t *testing.T) (*fixtureBlocklist, automationv1.AutomationServiceClient) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixtureBlocklist{}
	srv := grpc.NewServer()
	automationv1.RegisterAutomationServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return fake, automationv1.NewAutomationServiceClient(conn)
}

func TestHandleListBlocklistUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handleListBlocklist(w, httptest.NewRequest(http.MethodGet, "/api/blocklist", nil))
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
		t.Fatalf("%+v", body)
	}
}

func TestHandleListBlocklistLive(t *testing.T) {
	_, client := dialBlocklist(t)
	s := &server{automation: client}
	w := httptest.NewRecorder()
	s.handleListBlocklist(w, httptest.NewRequest(http.MethodGet, "/api/blocklist", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Items     []struct {
			Title string `json:"title"`
			GUID  string `json:"guid"`
		} `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.Items[0].Title != "CAM.Rip" || body.Items[0].GUID != "g-bad" {
		t.Fatalf("%+v", body)
	}
}

func TestHandleClearBlocklistEntry(t *testing.T) {
	fake, client := dialBlocklist(t)
	s := &server{automation: client}
	req := httptest.NewRequest(http.MethodPost, "/api/blocklist/clear", bytes.NewBufferString(`{"wanted_item_id":"q1","guid":"g-bad"}`))
	w := httptest.NewRecorder()
	s.handleClearBlocklist(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if fake.clearedAll || fake.clearedID != "q1" || fake.clearedGUID != "g-bad" {
		t.Fatalf("cleared all=%v id=%q guid=%q", fake.clearedAll, fake.clearedID, fake.clearedGUID)
	}
}

func TestHandleClearBlocklistAll(t *testing.T) {
	fake, client := dialBlocklist(t)
	s := &server{automation: client}
	req := httptest.NewRequest(http.MethodPost, "/api/blocklist/clear", bytes.NewBufferString(`{"clear_all":true}`))
	w := httptest.NewRecorder()
	s.handleClearBlocklist(w, req)
	if w.Code != http.StatusOK || !fake.clearedAll {
		t.Fatalf("status=%d all=%v", w.Code, fake.clearedAll)
	}
}
