package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	jellyfinv1 "github.com/Muxcore-Media/jellyfin/proto/jellyfinv1"
	plexv1 "github.com/Muxcore-Media/plex/proto/plexv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fixturePlexStop struct {
	plexv1.UnimplementedPlexBridgeServiceServer
	sessionID string
	reason    string
}

func (f *fixturePlexStop) TerminateSession(_ context.Context, req *plexv1.TerminateSessionRequest) (*plexv1.TerminateSessionResponse, error) {
	f.sessionID = req.GetSessionId()
	f.reason = req.GetReason()
	return &plexv1.TerminateSessionResponse{Ok: true}, nil
}

func dialPlexStop(t *testing.T) (*fixturePlexStop, plexv1.PlexBridgeServiceClient) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixturePlexStop{}
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

type fixtureJellyfinSessions struct {
	jellyfinv1.UnimplementedJellyfinBridgeServer
	stoppedID string
}

func (f *fixtureJellyfinSessions) ListSessions(context.Context, *jellyfinv1.ListSessionsRequest) (*jellyfinv1.ListSessionsResponse, error) {
	return &jellyfinv1.ListSessionsResponse{
		Sessions: []*jellyfinv1.Session{{
			Id: "jf-1", UserId: "u1", UserName: "pat", ItemTitle: "Arrival",
			PositionSeconds: 90, Device: "Living room", Client: "Jellyfin Android",
		}},
	}, nil
}

func (f *fixtureJellyfinSessions) TerminateSession(_ context.Context, req *jellyfinv1.TerminateSessionRequest) (*jellyfinv1.TerminateSessionResponse, error) {
	f.stoppedID = req.GetSessionId()
	return &jellyfinv1.TerminateSessionResponse{Ok: true}, nil
}

func dialJellyfinSessions(t *testing.T) (*fixtureJellyfinSessions, jellyfinv1.JellyfinBridgeClient) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixtureJellyfinSessions{}
	srv := grpc.NewServer()
	jellyfinv1.RegisterJellyfinBridgeServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return fake, jellyfinv1.NewJellyfinBridgeClient(conn)
}

func TestHandlePlaybackSessionsUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handlePlaybackSessions(w, httptest.NewRequest(http.MethodGet, "/api/sessions", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Available bool  `json:"available"`
		Total     int   `json:"total"`
		Items     []any `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available || body.Total != 0 || body.Items == nil {
		t.Fatalf("unavailable %#v", body)
	}
}

func TestHandlePlaybackSessionsLive(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sessions/active" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer house-token" {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sessions": []map[string]any{
				{
					"ID":              "sess-1",
					"Title":           "Dune",
					"UserName":        "sam",
					"MuxcoreID":       "m1",
					"MediaType":       "movie",
					"Player":          "MuxCore",
					"State":           "playing",
					"IsTranscode":     false,
					"PositionSeconds": 120,
					"DurationSeconds": 9000,
				},
			},
		})
	}))
	t.Cleanup(up.Close)
	u, _ := url.Parse(up.URL)
	s := &server{playbackMonitorHTTP: u, playbackMonitorToken: "house-token"}
	w := httptest.NewRecorder()
	s.handlePlaybackSessions(w, httptest.NewRequest(http.MethodGet, "/api/sessions", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Items     []struct {
			Title string `json:"title"`
			Href  string `json:"href"`
			User  string `json:"user"`
		} `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.Items[0].Title != "Dune" || body.Items[0].Href != "/movies/m1" || body.Items[0].User != "sam" {
		t.Fatalf("sessions %#v", body)
	}
}

func TestHandlePlaybackSessionsJellyfinBridge(t *testing.T) {
	_, client := dialJellyfinSessions(t)
	s := &server{jellyfin: client}
	w := httptest.NewRecorder()
	s.handlePlaybackSessions(w, httptest.NewRequest(http.MethodGet, "/api/sessions", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Items     []struct {
			Title      string `json:"title"`
			User       string `json:"user"`
			ServerType string `json:"serverType"`
			ID         string `json:"id"`
		} `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.Items[0].Title != "Arrival" || body.Items[0].User != "pat" || body.Items[0].ServerType != "jellyfin" || body.Items[0].ID != "jf-1" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleStopPlaybackSession(t *testing.T) {
	var gotPath, gotAuth string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"stopped": true,
			"session": map[string]any{"ID": "sess-1", "ServerType": "native", "ExternalSessionID": "web-1"},
		})
	}))
	t.Cleanup(up.Close)
	u, _ := url.Parse(up.URL)
	s := &server{playbackMonitorHTTP: u, playbackMonitorToken: "house-token"}
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/sess-1/stop", nil)
	req.SetPathValue("id", "sess-1")
	w := httptest.NewRecorder()
	s.handleStopPlaybackSession(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if gotPath != "/sessions/sess-1/stop" || gotAuth != "Bearer house-token" {
		t.Fatalf("path=%q auth=%q", gotPath, gotAuth)
	}
	var body struct {
		Stopped    bool   `json:"stopped"`
		ServerType string `json:"serverType"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Stopped || body.ServerType != "native" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleStopPlaybackSessionJellyfinOnly(t *testing.T) {
	fake, client := dialJellyfinSessions(t)
	s := &server{jellyfin: client}
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/jf-1/stop", nil)
	req.SetPathValue("id", "jf-1")
	w := httptest.NewRecorder()
	s.handleStopPlaybackSession(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.stoppedID != "jf-1" {
		t.Fatalf("stopped %q", fake.stoppedID)
	}
	var body struct {
		Stopped         bool   `json:"stopped"`
		ServerType      string `json:"serverType"`
		JellyfinStopped bool   `json:"jellyfinStopped"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Stopped || body.ServerType != "jellyfin" || !body.JellyfinStopped {
		t.Fatalf("%#v", body)
	}
}

func TestHandleStopPlaybackSessionPlex(t *testing.T) {
	fake, client := dialPlexStop(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"stopped": true,
			"session": map[string]any{"ID": "sess-plex", "ServerType": "plex", "ExternalSessionID": "plex-42"},
		})
	}))
	t.Cleanup(up.Close)
	u, _ := url.Parse(up.URL)
	s := &server{playbackMonitorHTTP: u, playbackMonitorToken: "house-token", plex: client}
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/sess-plex/stop", nil)
	req.SetPathValue("id", "sess-plex")
	w := httptest.NewRecorder()
	s.handleStopPlaybackSession(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.sessionID != "plex-42" || fake.reason != "household stop" {
		t.Fatalf("plex %#v", fake)
	}
	var body struct {
		Stopped     bool   `json:"stopped"`
		ServerType  string `json:"serverType"`
		PlexStopped bool   `json:"plexStopped"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Stopped || body.ServerType != "plex" || !body.PlexStopped {
		t.Fatalf("%#v", body)
	}
}

func TestHandlePlaybackSessionEventsUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handlePlaybackSessionEvents(w, httptest.NewRequest(http.MethodGet, "/api/sessions/events", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandlePlaybackSessionEvents(t *testing.T) {
	var gotAuth, gotPath string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		_, _ = w.Write([]byte("event: connected\ndata: {\"event\":\"connected\"}\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	t.Cleanup(up.Close)
	u, _ := url.Parse(up.URL)
	s := &server{playbackMonitorHTTP: u, playbackMonitorToken: "house-token"}
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/events", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		s.handlePlaybackSessionEvents(w, req)
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(w.Body.String(), "connected") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if gotPath != "/events/streams" || gotAuth != "Bearer house-token" {
		t.Fatalf("path=%q auth=%q", gotPath, gotAuth)
	}
	if !strings.Contains(w.Body.String(), "connected") {
		t.Fatalf("body %q", w.Body.String())
	}
}

func TestHandlePlaybackHistoryUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handlePlaybackHistory(w, httptest.NewRequest(http.MethodGet, "/api/history", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Available bool  `json:"available"`
		Items     []any `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available || body.Items == nil {
		t.Fatalf("unavailable %#v", body)
	}
}

func TestHandlePlaybackHistoryLive(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/history" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer house-token" {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sessions": []map[string]any{
				{
					"ID":              "h1",
					"Title":           "Dune",
					"UserName":        "sam",
					"UserID":          "u-sam",
					"MuxcoreID":       "m1",
					"MediaType":       "movie",
					"State":           "stopped",
					"PositionSeconds": 8800,
					"DurationSeconds": 9000,
					"LastProgressAt":  "2026-09-08T01:00:00Z",
				},
			},
		})
	}))
	t.Cleanup(up.Close)
	u, _ := url.Parse(up.URL)
	s := &server{playbackMonitorHTTP: u, playbackMonitorToken: "house-token"}
	w := httptest.NewRecorder()
	s.handlePlaybackHistory(w, httptest.NewRequest(http.MethodGet, "/api/history", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Items     []struct {
			Title   string `json:"title"`
			Href    string `json:"href"`
			Watched bool   `json:"watched"`
			User    string `json:"user"`
			UserID  string `json:"userId"`
		} `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.Items[0].Title != "Dune" || body.Items[0].Href != "/movies/m1" || !body.Items[0].Watched || body.Items[0].User != "sam" || body.Items[0].UserID != "u-sam" {
		t.Fatalf("history %#v", body)
	}
}

func TestHandlePlaybackHistoryFilter(t *testing.T) {
	var gotQuery url.Values
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/history" {
			http.NotFound(w, r)
			return
		}
		gotQuery = r.URL.Query()
		_ = json.NewEncoder(w).Encode(map[string]any{"sessions": []map[string]any{}})
	}))
	t.Cleanup(up.Close)
	u, _ := url.Parse(up.URL)
	s := &server{playbackMonitorHTTP: u, playbackMonitorToken: "house-token"}
	w := httptest.NewRecorder()
	s.handlePlaybackHistory(w, httptest.NewRequest(http.MethodGet, "/api/history?userId=u-sam&q=Dune", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if gotQuery.Get("user_id") != "u-sam" || gotQuery.Get("q") != "Dune" || gotQuery.Get("limit") != "100" {
		t.Fatalf("query %#v", gotQuery)
	}
}

func TestHistoryWatched(t *testing.T) {
	if !historyWatched(8800, 9000) {
		t.Fatal("near end is watched")
	}
	if historyWatched(120, 9000) {
		t.Fatal("early progress is not watched")
	}
}

func TestPlaybackSessionHref(t *testing.T) {
	if playbackSessionHref("movie", "m1") != "/movies/m1" {
		t.Fatal(playbackSessionHref("movie", "m1"))
	}
	if playbackSessionHref("episode", "s1") != "/tv/s1" {
		t.Fatal(playbackSessionHref("episode", "s1"))
	}
}
