package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestNormalizePlaybackEventType(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"started":          "playback.started",
		"playback.started": "playback.started",
		"progress":         "playback.progress",
		"stopped":          "playback.stopped",
		"paused":           "playback.stopped",
		"playback.stopped": "playback.stopped",
		"":                 "",
		"unknown":          "",
	}
	for in, want := range cases {
		if got := normalizePlaybackEventType(in); got != want {
			t.Fatalf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestHandlePlaybackSessionRejected(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handlePlaybackSession(w, httptest.NewRequest(http.MethodGet, "/api/playback/session", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status %d", w.Code)
	}

	bad := httptest.NewRecorder()
	s.handlePlaybackSession(bad, httptest.NewRequest(http.MethodPost, "/api/playback/session", strings.NewReader(`{`)))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid json %d", bad.Code)
	}

	missing := httptest.NewRecorder()
	s.handlePlaybackSession(missing, httptest.NewRequest(http.MethodPost, "/api/playback/session", strings.NewReader(
		`{"event_type":"started"}`,
	)))
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing media_id %d body %s", missing.Code, missing.Body.String())
	}
}

func TestHandlePlaybackSessionAcceptedWithoutMonitor(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handlePlaybackSession(w, httptest.NewRequest(http.MethodPost, "/api/playback/session", strings.NewReader(
		`{"event_type":"started","media_id":"m1","title":"Dune"}`,
	)))
	if w.Code != http.StatusAccepted {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var body struct {
		Accepted  bool `json:"accepted"`
		Forwarded bool `json:"forwarded"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Accepted || body.Forwarded {
		t.Fatalf("%+v", body)
	}
}

func TestHandlePlaybackSessionForwardsToMonitor(t *testing.T) {
	var got monitorSessionEvent
	var gotToken string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ingest" {
			http.NotFound(w, r)
			return
		}
		gotToken = r.Header.Get("X-Playback-Monitor-Token")
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			http.Error(w, "read", http.StatusBadRequest)
			return
		}
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Error(err)
			http.Error(w, "json", http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{"session_id": "sess-monitor-1", "created": true})
	}))
	t.Cleanup(up.Close)
	u, _ := url.Parse(up.URL)

	sessions := newSessionStore(time.Hour)
	tok, err := sessions.Create("alice", "Alice")
	if err != nil {
		t.Fatal(err)
	}
	s := &server{
		playbackMonitorHTTP:  u,
		playbackMonitorToken: "house-token",
		sessions:             sessions,
	}
	req := httptest.NewRequest(http.MethodPost, "/api/playback/session", strings.NewReader(
		`{"event_type":"progress","session_id":"web-1","media_id":"m1","title":"Dune","media_type":"movie","position_seconds":42,"duration_seconds":7200,"is_transcode":true}`,
	))
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	req.RemoteAddr = "203.0.113.9:4444"
	w := httptest.NewRecorder()
	s.handlePlaybackSession(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var body struct {
		Accepted  bool   `json:"accepted"`
		Forwarded bool   `json:"forwarded"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Accepted || !body.Forwarded || body.SessionID != "sess-monitor-1" {
		t.Fatalf("%+v", body)
	}
	if gotToken != "house-token" {
		t.Fatalf("token %q", gotToken)
	}
	if got.EventType != "playback.progress" || got.ServerType != "native" || got.SourceModule != "media-ui" {
		t.Fatalf("event %+v", got)
	}
	if got.UserID != "alice" || got.UserName != "Alice" || got.ExternalSessionID != "web-1" {
		t.Fatalf("identity %+v", got)
	}
	if got.ItemID != "m1" || got.Title != "Dune" || got.PositionSeconds != 42 || !got.IsTranscode {
		t.Fatalf("media %+v", got)
	}
	if got.ServerID != "muxcore-native" || got.Player != "media-ui" {
		t.Fatalf("server %+v", got)
	}
}

func TestHandlePlaybackSessionMonitorDownAccepted(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "db not initialized", http.StatusInternalServerError)
	}))
	t.Cleanup(up.Close)
	u, _ := url.Parse(up.URL)
	s := &server{playbackMonitorHTTP: u}
	w := httptest.NewRecorder()
	s.handlePlaybackSession(w, httptest.NewRequest(http.MethodPost, "/api/playback/session", strings.NewReader(
		`{"event_type":"stopped","media_id":"m1"}`,
	)))
	if w.Code != http.StatusAccepted {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var body struct {
		Forwarded bool `json:"forwarded"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Forwarded {
		t.Fatal("expected forwarded false")
	}
}

func TestHandlePlaybackSessionStoppedByKick(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"session_id": "sess-1", "stopped": true})
	}))
	t.Cleanup(up.Close)
	u, _ := url.Parse(up.URL)
	s := &server{playbackMonitorHTTP: u, playbackMonitorToken: "house-token"}
	w := httptest.NewRecorder()
	s.handlePlaybackSession(w, httptest.NewRequest(http.MethodPost, "/api/playback/session", strings.NewReader(
		`{"event_type":"progress","media_id":"m1","session_id":"web-1"}`,
	)))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Stopped   bool `json:"stopped"`
		Forwarded bool `json:"forwarded"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Stopped || !body.Forwarded {
		t.Fatalf("%+v", body)
	}
}
