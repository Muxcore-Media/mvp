package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandleJellyfinLinkRequiresMuxID(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handleJellyfinLink(w, httptest.NewRequest(http.MethodGet, "/api/jellyfin/link", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleJellyfinLinkUnavailable(t *testing.T) {
	s := &server{}
	w := httptest.NewRecorder()
	s.handleJellyfinLink(w, httptest.NewRequest(http.MethodGet, "/api/jellyfin/link?mux_id=mux-1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Available bool `json:"available"`
		Linked    bool `json:"linked"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available || body.Linked {
		t.Fatalf("%#v", body)
	}
}

func TestHandleJellyfinLinkFound(t *testing.T) {
	s := &server{jellyfin: dialJellyfinFixture(t, "", "", false)}
	w := httptest.NewRecorder()
	s.handleJellyfinLink(w, httptest.NewRequest(http.MethodGet, "/api/jellyfin/link?mux_id=mux-1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available  bool   `json:"available"`
		Linked     bool   `json:"linked"`
		JellyfinID string `json:"jellyfinId"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || !body.Linked || body.JellyfinID != "jf-99" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleJellyfinMatchForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	w := httptest.NewRecorder()
	s.handleJellyfinMatch(w, httptest.NewRequest(http.MethodPost, "/api/jellyfin/match", strings.NewReader(`{"mux_id":"mux-1"}`)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleJellyfinMatch(t *testing.T) {
	fx := &fixtureJellyfinBridge{matchLinked: true, matchReason: "provider_id"}
	s, req := privilegedJellyfinRequest(t, http.MethodPost, "/api/jellyfin/match", `{"mux_id":"mux-1","title":"Dune","media_kind":"movie","tmdb_id":438631}`)
	s.jellyfin = dialJellyfinBridge(t, fx)
	w := httptest.NewRecorder()
	s.handleJellyfinMatch(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Matched     bool   `json:"matched"`
		Linked      bool   `json:"linked"`
		JellyfinID  string `json:"jellyfinId"`
		MatchReason string `json:"matchReason"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Matched || !body.Linked || body.JellyfinID != "jf-99" || body.MatchReason != "provider_id" || fx.lastMatchMuxID != "mux-1" {
		t.Fatalf("%#v last=%s", body, fx.lastMatchMuxID)
	}
}

func TestHandleDeleteJellyfinLink(t *testing.T) {
	fx := &fixtureJellyfinBridge{}
	s, req := privilegedJellyfinRequest(t, http.MethodDelete, "/api/jellyfin/link?mux_id=mux-1", "")
	s.jellyfin = dialJellyfinBridge(t, fx)
	w := httptest.NewRecorder()
	s.handleDeleteJellyfinLink(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		OK    bool   `json:"ok"`
		MuxID string `json:"muxId"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || body.MuxID != "mux-1" || fx.lastDeleteMuxID != "mux-1" {
		t.Fatalf("%#v last=%s", body, fx.lastDeleteMuxID)
	}
}
