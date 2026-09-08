package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	plexv1 "github.com/Muxcore-Media/plex/proto/plexv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fixturePlexSyncLists struct {
	plexv1.UnimplementedPlexBridgeServiceServer
	lastUserID   string
	lastClientID string
	lastRefresh  bool
}

func (f *fixturePlexSyncLists) ListSyncLists(_ context.Context, req *plexv1.ListSyncListsRequest) (*plexv1.ListSyncListsResponse, error) {
	f.lastUserID = req.GetUserId()
	f.lastClientID = req.GetClientId()
	f.lastRefresh = req.GetRefresh()
	return &plexv1.ListSyncListsResponse{
		MachineIdentifier: "machine-1",
		UpdatedAt:         "2026-09-08T12:00:00Z",
		Lists: []*plexv1.PlexSyncListMessage{{
			Id:               "list-1",
			ClientIdentifier: "client-1",
			DeviceUserId:     "plex-user",
			DeviceName:       "Pat iPad",
			DevicePlatform:   "iOS",
			DeviceProduct:    "Plex for iOS",
			Items: []*plexv1.PlexSyncItemMessage{{
				Id:                 "item-1",
				Title:              "Dune",
				RootTitle:          "Movies",
				State:              "downloaded",
				ItemsCount:         1,
				ItemsCompleteCount: 1,
				TotalSizeBytes:     1024,
				VideoResolution:    "1080",
			}},
		}},
	}, nil
}

func dialPlexSyncLists(t *testing.T) (*fixturePlexSyncLists, plexv1.PlexBridgeServiceClient) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixturePlexSyncLists{}
	srv := grpc.NewServer()
	plexv1.RegisterPlexBridgeServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return fake, plexv1.NewPlexBridgeServiceClient(conn)
}

func TestHandlePlexSyncListsUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handlePlexSyncLists(w, httptest.NewRequest(http.MethodGet, "/api/plex/sync-lists", nil))
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

func TestHandlePlexSyncLists(t *testing.T) {
	fake, client := dialPlexSyncLists(t)
	s := &server{plex: client}
	w := httptest.NewRecorder()
	s.handlePlexSyncLists(w, httptest.NewRequest(http.MethodGet, "/api/plex/sync-lists?refresh=1&userId=plex-user&clientId=client-1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available         bool   `json:"available"`
		MachineIdentifier string `json:"machineIdentifier"`
		Total             int    `json:"total"`
		Lists             []struct {
			DeviceName string `json:"deviceName"`
			Items      []struct {
				Title string `json:"title"`
			} `json:"items"`
		} `json:"lists"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.Total != 1 || body.MachineIdentifier != "machine-1" || body.Lists[0].DeviceName != "Pat iPad" || body.Lists[0].Items[0].Title != "Dune" {
		t.Fatalf("%#v", body)
	}
	if !fake.lastRefresh || fake.lastUserID != "plex-user" || fake.lastClientID != "client-1" {
		t.Fatalf("rpc refresh=%v user=%s client=%s", fake.lastRefresh, fake.lastUserID, fake.lastClientID)
	}
}
