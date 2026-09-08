package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandleGetWatchNotifyUnavailable(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	tok, err := s.sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/watch-notify", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleGetWatchNotify(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Available    bool  `json:"available"`
		Rules        []any `json:"rules"`
		Destinations []any `json:"destinations"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available || body.Rules == nil || body.Destinations == nil {
		t.Fatalf("%#v", body)
	}
}

func TestHandleGetWatchNotifyForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	w := httptest.NewRecorder()
	s.handleGetWatchNotify(w, httptest.NewRequest(http.MethodGet, "/api/watch-notify", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleGetWatchNotifyLive(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer house-token" {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/notification/rules":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"rules": []map[string]any{{
					"id": "nr1", "name": "Session start", "enabled": true,
					"event_type": "playback.started", "title_template": "Playback started",
					"message_template": "{user} started watching \"{title}\"", "severity": "info",
					"destination_ids": []string{"d1"},
					"filters":         map[string]any{"transcode_only": true},
				}},
			})
		case "/notification/destinations":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"destinations": []map[string]any{{
					"id": "d1", "name": "Family Discord", "type": "discord", "enabled": true,
					"events": []string{"playback.started"},
					"config": map[string]any{"webhook_url": "********"},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(up.Close)
	s := &server{
		sessions:             newSessionStore(time.Hour),
		playbackMonitorHTTP:  mustURL(up.URL),
		playbackMonitorToken: "house-token",
	}
	tok, err := s.sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/watch-notify", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleGetWatchNotify(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Rules     []struct {
			Name      string `json:"name"`
			EventType string `json:"eventType"`
			Filters   struct {
				TranscodeOnly bool `json:"transcodeOnly"`
			} `json:"filters"`
		} `json:"rules"`
		Destinations []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"destinations"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || len(body.Rules) != 1 || body.Rules[0].Name != "Session start" ||
		body.Rules[0].EventType != "playback.started" || !body.Rules[0].Filters.TranscodeOnly ||
		body.Destinations[0].Name != "Family Discord" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleUpsertWatchNotifyRule(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/notification/rules" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var got map[string]any
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got["name"] != "Someone started" || got["event_type"] != "playback.started" {
			t.Fatalf("%#v", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"rule": map[string]any{
				"id": "nr1", "name": "Someone started", "enabled": true,
				"event_type": "playback.started", "severity": "info",
			},
		})
	}))
	t.Cleanup(up.Close)
	s := &server{
		sessions:             newSessionStore(time.Hour),
		playbackMonitorHTTP:  mustURL(up.URL),
		playbackMonitorToken: "house-token",
	}
	tok, err := s.sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/watch-notify/rules", strings.NewReader(
		`{"name":"Someone started","eventType":"playback.started","enabled":true}`,
	))
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleUpsertWatchNotifyRule(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Name      string `json:"name"`
		EventType string `json:"eventType"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Name != "Someone started" || body.EventType != "playback.started" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleUpsertWatchNotifyDestination(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/notification/destinations" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"destination": map[string]any{
				"id": "d1", "name": "Family Discord", "type": "discord", "enabled": true,
				"events": []string{"playback.started"},
				"config": map[string]any{"webhook_url": "********"},
			},
		})
	}))
	t.Cleanup(up.Close)
	s := &server{
		sessions:             newSessionStore(time.Hour),
		playbackMonitorHTTP:  mustURL(up.URL),
		playbackMonitorToken: "house-token",
	}
	tok, err := s.sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/watch-notify/destinations", strings.NewReader(
		`{"name":"Family Discord","type":"discord","events":["playback.started"],"config":{"webhook_url":"https://discord.example/hook"}}`,
	))
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleUpsertWatchNotifyDestination(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Name != "Family Discord" || body.Type != "discord" {
		t.Fatalf("%#v", body)
	}
}
