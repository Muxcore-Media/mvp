package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	plexv1 "github.com/Muxcore-Media/plex/proto/plexv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fixturePlexPlay struct {
	plexv1.UnimplementedPlexBridgeServiceServer
	playURL    string
	playFail   bool
	baseURL    string
	machineID  string
	lastRating string
}

func (f *fixturePlexPlay) PlayURL(_ context.Context, req *plexv1.PlayURLRequest) (*plexv1.PlayURLResponse, error) {
	f.lastRating = req.GetRatingKey()
	if f.playFail {
		return nil, errors.New("play url unavailable")
	}
	return &plexv1.PlayURLResponse{Url: f.playURL}, nil
}

func (f *fixturePlexPlay) Status(context.Context, *plexv1.StatusRequest) (*plexv1.StatusResponse, error) {
	return &plexv1.StatusResponse{
		Configured: f.baseURL != "",
		BaseUrl:    f.baseURL,
		MachineId:  f.machineID,
	}, nil
}

func dialPlexPlay(t *testing.T, f *fixturePlexPlay) plexv1.PlexBridgeServiceClient {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	plexv1.RegisterPlexBridgeServiceServer(srv, f)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return plexv1.NewPlexBridgeServiceClient(conn)
}

func TestHandlePlexPlaySuccess(t *testing.T) {
	s := &server{plex: dialPlexPlay(t, &fixturePlexPlay{
		playURL: "https://plex.example/web/index.html#!/server/m1/details?key=/library/metadata/99",
	})}
	w := httptest.NewRecorder()
	s.handlePlexPlay(w, httptest.NewRequest(http.MethodGet, "/api/plex/play?rating_key=99", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["url"] != "https://plex.example/web/index.html#!/server/m1/details?key=/library/metadata/99" {
		t.Fatalf("url=%s", body["url"])
	}
}

func TestHandlePlexPlayRequiresRatingKey(t *testing.T) {
	s := &server{plex: dialPlexPlay(t, &fixturePlexPlay{})}
	w := httptest.NewRecorder()
	s.handlePlexPlay(w, httptest.NewRequest(http.MethodGet, "/api/plex/play", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandlePlexPlayUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handlePlexPlay(w, httptest.NewRequest(http.MethodGet, "/api/plex/play?rating_key=99", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandlePlexPlayFallbackStatus(t *testing.T) {
	s := &server{plex: dialPlexPlay(t, &fixturePlexPlay{
		playFail:  true,
		baseURL:   "https://plex.example",
		machineID: "machine-1",
	})}
	w := httptest.NewRecorder()
	s.handlePlexPlay(w, httptest.NewRequest(http.MethodGet, "/api/plex/play?ratingKey=99", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	want := "https://plex.example/web/index.html#!/server/machine-1/details?key=/library/metadata/99"
	if body["url"] != want {
		t.Fatalf("url=%s", body["url"])
	}
}

func TestPlexDetailsURL(t *testing.T) {
	if got := plexDetailsURL("https://plex.example/", "m1", "99"); got != "https://plex.example/web/index.html#!/server/m1/details?key=/library/metadata/99" {
		t.Fatalf("got %s", got)
	}
	if plexDetailsURL("", "m1", "99") != "" {
		t.Fatal("expected empty without base")
	}
}
