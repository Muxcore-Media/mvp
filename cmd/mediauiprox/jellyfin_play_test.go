package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	jellyfinv1 "github.com/Muxcore-Media/jellyfin/proto/jellyfinv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fixtureJellyfinBridge struct {
	jellyfinv1.UnimplementedJellyfinBridgeServer
	playURL  string
	playFail bool
	baseURL  string
}

func (f fixtureJellyfinBridge) ListItemLinks(_ context.Context, _ *jellyfinv1.ListItemLinksRequest) (*jellyfinv1.ListItemLinksResponse, error) {
	return &jellyfinv1.ListItemLinksResponse{
		Links: []*jellyfinv1.ItemLink{{
			MuxcoreId:  "mux-1",
			JellyfinId: "jf-99",
		}},
	}, nil
}

func (f fixtureJellyfinBridge) PlayURL(_ context.Context, req *jellyfinv1.PlayURLRequest) (*jellyfinv1.PlayURLResponse, error) {
	if f.playFail {
		return nil, errors.New("play url unavailable")
	}
	if req.GetItemId() != "jf-99" {
		return &jellyfinv1.PlayURLResponse{}, nil
	}
	return &jellyfinv1.PlayURLResponse{Url: f.playURL}, nil
}

func (f fixtureJellyfinBridge) Status(_ context.Context, _ *jellyfinv1.StatusRequest) (*jellyfinv1.StatusResponse, error) {
	return &jellyfinv1.StatusResponse{BaseUrl: f.baseURL}, nil
}

func dialJellyfinFixture(t *testing.T, playURL, baseURL string, playFail bool) jellyfinv1.JellyfinBridgeClient {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	jellyfinv1.RegisterJellyfinBridgeServer(srv, fixtureJellyfinBridge{playURL: playURL, baseURL: baseURL, playFail: playFail})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return jellyfinv1.NewJellyfinBridgeClient(conn)
}

func TestHandleJellyfinPlaySuccess(t *testing.T) {
	s := &server{jellyfin: dialJellyfinFixture(t, "https://media.zem.systems/web/index.html#!/details?id=jf-99", "", false)}
	w := httptest.NewRecorder()
	s.handleJellyfinPlay(w, httptest.NewRequest(http.MethodGet, "/api/jellyfin/play?mux_id=mux-1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["url"] != "https://media.zem.systems/web/index.html#!/details?id=jf-99" {
		t.Fatalf("url=%s", body["url"])
	}
}

func TestHandleJellyfinPlayRequiresMuxID(t *testing.T) {
	s := &server{jellyfin: dialJellyfinFixture(t, "", "", false)}
	w := httptest.NewRecorder()
	s.handleJellyfinPlay(w, httptest.NewRequest(http.MethodGet, "/api/jellyfin/play", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleJellyfinPlayNotLinked(t *testing.T) {
	s := &server{jellyfin: dialJellyfinFixture(t, "", "", false)}
	w := httptest.NewRecorder()
	s.handleJellyfinPlay(w, httptest.NewRequest(http.MethodGet, "/api/jellyfin/play?mux_id=unknown", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
}

func TestHandleJellyfinPlayFallbackBaseURL(t *testing.T) {
	s := &server{jellyfin: dialJellyfinFixture(t, "", "https://jellyfin.example", true)}
	w := httptest.NewRecorder()
	s.handleJellyfinPlay(w, httptest.NewRequest(http.MethodGet, "/api/jellyfin/play?mux_id=mux-1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["url"] != "https://jellyfin.example/web/index.html#!/details?id=jf-99" {
		t.Fatalf("url=%s", body["url"])
	}
}
