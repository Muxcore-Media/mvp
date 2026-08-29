package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Muxcore-Media/userdata-local/store"
)

func TestParentalBlocksPlaybackUnit(t *testing.T) {
	prefs, _ := json.Marshal(map[string]any{
		"parental": map[string]any{"blocked_tags": "horror,gore"},
	})
	if !parentalBlocksPlayback(prefs, "horror", "", false) {
		t.Fatal("expected horror tag to block")
	}
	if parentalBlocksPlayback(prefs, "family", "", false) {
		t.Fatal("expected family tag to pass")
	}
}

func TestParentalBlocksPlaybackResolve(t *testing.T) {
	t.Setenv("USERDATA_PREFER_MESH", "0")
	t.Setenv("USERDATA_LOCAL_URL", "")
	t.Setenv("TENANT_MODE", "0")
	dir := t.TempDir()
	ud := newServerUserdata(dir)
	prefs, _ := json.Marshal(map[string]any{
		"parental": map[string]any{
			"blocked_tags":  "horror,gore",
			"allow_unrated": false,
		},
	})
	scope := store.Scope{UserID: "kid"}
	_, err := ud.store.Put(scope, store.Blob{
		Progress:  map[string]json.RawMessage{},
		Favorites: map[string]json.RawMessage{},
		Prefs:     prefs,
	})
	if err != nil {
		t.Fatal(err)
	}

	s := &server{userdata: ud, sessions: newSessionStore(3600)}
	tok, err := s.sessions.Create("kid", "Kid")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/playback/resolve?src=/stream/movies/m1&tags=horror&user_id=kid", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	rec := httptest.NewRecorder()
	s.handlePlaybackResolve(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "playback.parental_blocked") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestParentalAllowsCleanPlaybackResolve(t *testing.T) {
	t.Setenv("USERDATA_PREFER_MESH", "0")
	t.Setenv("USERDATA_LOCAL_URL", "")
	t.Setenv("TENANT_MODE", "0")
	dir := t.TempDir()
	ud := newServerUserdata(dir)
	prefs, _ := json.Marshal(map[string]any{
		"parental": map[string]any{"blocked_tags": "horror"},
	})
	scope := store.Scope{UserID: "kid"}
	_, err := ud.store.Put(scope, store.Blob{
		Progress:  map[string]json.RawMessage{},
		Favorites: map[string]json.RawMessage{},
		Prefs:     prefs,
	})
	if err != nil {
		t.Fatal(err)
	}

	s := &server{userdata: ud, sessions: newSessionStore(3600)}
	tok, err := s.sessions.Create("kid", "Kid")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/playback/resolve?src=/stream/movies/m1&tags=family", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	rec := httptest.NewRecorder()
	s.handlePlaybackResolve(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}
