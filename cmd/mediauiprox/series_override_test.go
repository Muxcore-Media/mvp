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

type fixtureSeriesOverride struct {
	automationv1.UnimplementedAutomationServiceServer
	overrides map[string]*automationv1.SeriesOverride
}

func (f *fixtureSeriesOverride) ListSeriesOverrides(_ context.Context, _ *automationv1.ListSeriesOverridesRequest) (*automationv1.ListSeriesOverridesResponse, error) {
	out := make([]*automationv1.SeriesOverride, 0, len(f.overrides))
	for _, o := range f.overrides {
		out = append(out, o)
	}
	return &automationv1.ListSeriesOverridesResponse{Overrides: out}, nil
}

func (f *fixtureSeriesOverride) UpsertSeriesOverride(_ context.Context, req *automationv1.UpsertSeriesOverrideRequest) (*automationv1.UpsertSeriesOverrideResponse, error) {
	if f.overrides == nil {
		f.overrides = map[string]*automationv1.SeriesOverride{}
	}
	o := req.GetOverride()
	f.overrides[o.GetSeriesId()] = o
	return &automationv1.UpsertSeriesOverrideResponse{Override: o}, nil
}

func (f *fixtureSeriesOverride) DeleteSeriesOverride(_ context.Context, req *automationv1.DeleteSeriesOverrideRequest) (*automationv1.DeleteSeriesOverrideResponse, error) {
	delete(f.overrides, req.GetSeriesId())
	return &automationv1.DeleteSeriesOverrideResponse{}, nil
}

func dialSeriesOverride(t *testing.T) (*fixtureSeriesOverride, automationv1.AutomationServiceClient) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixtureSeriesOverride{overrides: map[string]*automationv1.SeriesOverride{
		"s1": {SeriesId: "s1", DelayMinutes: 45, PreferredGroups: []string{"FLUX"}},
	}}
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

func TestHandleGetSeriesOverrideUnavailable(t *testing.T) {
	s := &server{}
	req := httptest.NewRequest(http.MethodGet, "/api/tv/s1/override", nil)
	req.SetPathValue("id", "s1")
	w := httptest.NewRecorder()
	s.handleGetSeriesOverride(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	var body struct {
		Available bool `json:"available"`
		Found     bool `json:"found"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available || body.Found {
		t.Fatalf("%+v", body)
	}
}

func TestHandleGetAndPutSeriesOverride(t *testing.T) {
	fake, client := dialSeriesOverride(t)
	s := &server{automation: client}

	req := httptest.NewRequest(http.MethodGet, "/api/tv/s1/override", nil)
	req.SetPathValue("id", "s1")
	w := httptest.NewRecorder()
	s.handleGetSeriesOverride(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d %s", w.Code, w.Body.String())
	}
	var got struct {
		Available bool `json:"available"`
		Found     bool `json:"found"`
		Override  struct {
			DelayMinutes    int32    `json:"delay_minutes"`
			PreferredGroups []string `json:"preferred_groups"`
		} `json:"override"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Available || !got.Found || got.Override.DelayMinutes != 45 {
		t.Fatalf("%+v", got)
	}

	put := httptest.NewRequest(http.MethodPut, "/api/tv/s1/override", bytes.NewBufferString(`{"delay_minutes":90,"preferred_groups":["EMBER"],"ignored_groups":["CAM"]}`))
	put.SetPathValue("id", "s1")
	pw := httptest.NewRecorder()
	s.handlePutSeriesOverride(pw, put)
	if pw.Code != http.StatusOK {
		t.Fatalf("put status=%d %s", pw.Code, pw.Body.String())
	}
	if fake.overrides["s1"].GetDelayMinutes() != 90 || fake.overrides["s1"].GetPreferredGroups()[0] != "EMBER" {
		t.Fatalf("%+v", fake.overrides["s1"])
	}
}

func TestHandleDeleteSeriesOverride(t *testing.T) {
	fake, client := dialSeriesOverride(t)
	s := &server{automation: client}
	req := httptest.NewRequest(http.MethodDelete, "/api/tv/s1/override", nil)
	req.SetPathValue("id", "s1")
	w := httptest.NewRecorder()
	s.handleDeleteSeriesOverride(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d %s", w.Code, w.Body.String())
	}
	if _, ok := fake.overrides["s1"]; ok {
		t.Fatal("expected deleted")
	}
}
