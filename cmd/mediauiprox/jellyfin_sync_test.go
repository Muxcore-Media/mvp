package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func privilegedJellyfinRequest(t *testing.T, method, target, body string) (*server, *http.Request) {
	t.Helper()
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	r.AddCookie(&http.Cookie{Name: "session", Value: tok})
	return &server{sessions: sessions}, r
}

func TestHandleJellyfinStatusForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	w := httptest.NewRecorder()
	s.handleJellyfinStatus(w, httptest.NewRequest(http.MethodGet, "/api/jellyfin/status", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleJellyfinStatusUnavailable(t *testing.T) {
	s, req := privilegedJellyfinRequest(t, http.MethodGet, "/api/jellyfin/status", "")
	w := httptest.NewRecorder()
	s.handleJellyfinStatus(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
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

func TestHandleJellyfinStatusConfigured(t *testing.T) {
	s, req := privilegedJellyfinRequest(t, http.MethodGet, "/api/jellyfin/status", "")
	s.jellyfin = dialJellyfinBridge(t, &fixtureJellyfinBridge{
		configured:   true,
		baseURL:      "https://jellyfin.example",
		conflictMode: "jellyfin",
		itemLinks:    12,
	})
	w := httptest.NewRecorder()
	s.handleJellyfinStatus(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available    bool   `json:"available"`
		Configured   bool   `json:"configured"`
		BaseURL      string `json:"baseUrl"`
		ConflictMode string `json:"conflictMode"`
		ItemLinks    int    `json:"itemLinks"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || !body.Configured || body.BaseURL != "https://jellyfin.example" || body.ItemLinks != 12 || body.ConflictMode != "jellyfin" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleJellyfinSyncForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	w := httptest.NewRecorder()
	s.handleJellyfinSync(w, httptest.NewRequest(http.MethodPost, "/api/jellyfin/sync", strings.NewReader(`{"dry_run":true}`)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleJellyfinSyncDryRun(t *testing.T) {
	fx := &fixtureJellyfinBridge{syncScanned: 40, syncMatched: 12, syncUpserted: 3, syncRemoved: 1, syncErrors: []string{"skipped orphan"}}
	s, req := privilegedJellyfinRequest(t, http.MethodPost, "/api/jellyfin/sync", `{"direction":"jellyfin","dry_run":true}`)
	s.jellyfin = dialJellyfinBridge(t, fx)
	w := httptest.NewRecorder()
	s.handleJellyfinSync(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool     `json:"available"`
		Direction string   `json:"direction"`
		DryRun    bool     `json:"dryRun"`
		Scanned   int      `json:"scanned"`
		Matched   int      `json:"matched"`
		Upserted  int      `json:"upserted"`
		Removed   int      `json:"removed"`
		Errors    []string `json:"errors"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || !body.DryRun || body.Direction != "jellyfin" || body.Scanned != 40 || body.Matched != 12 || body.Upserted != 3 || body.Removed != 1 || len(body.Errors) != 1 {
		t.Fatalf("%#v", body)
	}
	if fx.lastSyncDirection != "jellyfin" || !fx.lastSyncDryRun {
		t.Fatalf("rpc direction=%s dry=%v", fx.lastSyncDirection, fx.lastSyncDryRun)
	}
}

func TestHandleJellyfinSyncRejectsDirection(t *testing.T) {
	s, req := privilegedJellyfinRequest(t, http.MethodPost, "/api/jellyfin/sync", `{"direction":"sideways"}`)
	s.jellyfin = dialJellyfinBridge(t, &fixtureJellyfinBridge{})
	w := httptest.NewRecorder()
	s.handleJellyfinSync(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
}

func TestHandleJellyfinRefresh(t *testing.T) {
	fx := &fixtureJellyfinBridge{refreshOK: true}
	s, req := privilegedJellyfinRequest(t, http.MethodPost, "/api/jellyfin/refresh", `{"item_id":"jf-99"}`)
	s.jellyfin = dialJellyfinBridge(t, fx)
	w := httptest.NewRecorder()
	s.handleJellyfinRefresh(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		OK     bool   `json:"ok"`
		ItemID string `json:"itemId"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || body.ItemID != "jf-99" || fx.lastRefreshItemID != "jf-99" {
		t.Fatalf("%#v last=%s", body, fx.lastRefreshItemID)
	}
}
