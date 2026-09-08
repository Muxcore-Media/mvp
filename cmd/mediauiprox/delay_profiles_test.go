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

type fixtureDelayProfiles struct {
	automationv1.UnimplementedAutomationServiceServer
	profiles map[string]int32
}

func (f *fixtureDelayProfiles) ListDelayProfiles(_ context.Context, _ *automationv1.ListDelayProfilesRequest) (*automationv1.ListDelayProfilesResponse, error) {
	out := make([]*automationv1.DelayProfile, 0, len(f.profiles))
	for proto, mins := range f.profiles {
		out = append(out, &automationv1.DelayProfile{Protocol: proto, WaitMinutes: mins})
	}
	return &automationv1.ListDelayProfilesResponse{Profiles: out}, nil
}

func (f *fixtureDelayProfiles) UpsertDelayProfile(_ context.Context, req *automationv1.UpsertDelayProfileRequest) (*automationv1.UpsertDelayProfileResponse, error) {
	if f.profiles == nil {
		f.profiles = map[string]int32{}
	}
	f.profiles[req.GetProtocol()] = req.GetWaitMinutes()
	return &automationv1.UpsertDelayProfileResponse{
		Profile: &automationv1.DelayProfile{Protocol: req.GetProtocol(), WaitMinutes: req.GetWaitMinutes()},
	}, nil
}

func dialDelayProfiles(t *testing.T) (*fixtureDelayProfiles, automationv1.AutomationServiceClient) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixtureDelayProfiles{profiles: map[string]int32{"torrent": 15, "usenet": 0}}
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

func TestHandleListDelayProfilesUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handleListDelayProfiles(w, httptest.NewRequest(http.MethodGet, "/api/delay-profiles", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	var body struct {
		Available bool  `json:"available"`
		Profiles  []any `json:"profiles"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available || body.Profiles == nil {
		t.Fatalf("%+v", body)
	}
}

func TestHandleListDelayProfilesLive(t *testing.T) {
	_, client := dialDelayProfiles(t)
	s := &server{automation: client}
	w := httptest.NewRecorder()
	s.handleListDelayProfiles(w, httptest.NewRequest(http.MethodGet, "/api/delay-profiles", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Profiles  []struct {
			Protocol    string `json:"protocol"`
			WaitMinutes int32  `json:"wait_minutes"`
		} `json:"profiles"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || len(body.Profiles) != 2 {
		t.Fatalf("%+v", body)
	}
	got := map[string]int32{}
	for _, p := range body.Profiles {
		got[p.Protocol] = p.WaitMinutes
	}
	if got["torrent"] != 15 || got["usenet"] != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestHandleUpsertDelayProfile(t *testing.T) {
	fake, client := dialDelayProfiles(t)
	s := &server{automation: client}
	req := httptest.NewRequest(http.MethodPut, "/api/delay-profiles", bytes.NewBufferString(`{"protocol":"Torrent","wait_minutes":30}`))
	w := httptest.NewRecorder()
	s.handleUpsertDelayProfile(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if fake.profiles["torrent"] != 30 {
		t.Fatalf("saved=%v", fake.profiles)
	}
	var body struct {
		Protocol    string `json:"protocol"`
		WaitMinutes int32  `json:"wait_minutes"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Protocol != "torrent" || body.WaitMinutes != 30 {
		t.Fatalf("%+v", body)
	}
}

func TestHandleUpsertDelayProfileRejectsBadWait(t *testing.T) {
	_, client := dialDelayProfiles(t)
	s := &server{automation: client}
	req := httptest.NewRequest(http.MethodPut, "/api/delay-profiles", bytes.NewBufferString(`{"protocol":"torrent","wait_minutes":-1}`))
	w := httptest.NewRecorder()
	s.handleUpsertDelayProfile(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestHandleUpsertDelayProfileUnavailable(t *testing.T) {
	s := &server{}
	req := httptest.NewRequest(http.MethodPut, "/api/delay-profiles", bytes.NewBufferString(`{"protocol":"torrent","wait_minutes":5}`))
	w := httptest.NewRecorder()
	s.handleUpsertDelayProfile(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", w.Code)
	}
}
