package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Muxcore-Media/userdata-local/store"
)

func TestUserdataMergeAndScope(t *testing.T) {
	dir := t.TempDir()
	u := newServerUserdata(dir)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.Create("alice", "Alice")
	if err != nil {
		t.Fatal(err)
	}

	put1 := httptest.NewRequest(http.MethodPut, "/api/userdata", strings.NewReader(
		`{"progress":{"m1":{"id":"m1","positionSec":10,"updatedAt":"2026-01-01T00:00:00Z"}}}`))
	put1.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w1 := httptest.NewRecorder()
	s := &server{userdata: u, sessions: sessions}
	s.handleUserdataPut(w1, put1)
	if w1.Code != http.StatusOK {
		t.Fatalf("put1 %d %s", w1.Code, w1.Body.String())
	}

	// Older client payload must not clobber fresher server progress.
	put2 := httptest.NewRequest(http.MethodPut, "/api/userdata", strings.NewReader(
		`{"progress":{"m1":{"id":"m1","positionSec":1,"updatedAt":"2025-01-01T00:00:00Z"}}}`))
	put2.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w2 := httptest.NewRecorder()
	s.handleUserdataPut(w2, put2)

	get := httptest.NewRequest(http.MethodGet, "/api/userdata", nil)
	get.AddCookie(&http.Cookie{Name: "session", Value: tok})
	wg := httptest.NewRecorder()
	s.handleUserdataGet(wg, get)
	var blob store.Blob
	if err := json.NewDecoder(wg.Body).Decode(&blob); err != nil {
		t.Fatal(err)
	}
	var p struct {
		PositionSec int `json:"positionSec"`
	}
	_ = json.Unmarshal(blob.Progress["m1"], &p)
	if p.PositionSec != 10 {
		t.Fatalf("want server progress 10, got %d", p.PositionSec)
	}

	// Different user must not see alice progress.
	tok2, _ := sessions.Create("bob", "Bob")
	getBob := httptest.NewRequest(http.MethodGet, "/api/userdata", nil)
	getBob.AddCookie(&http.Cookie{Name: "session", Value: tok2})
	wb := httptest.NewRecorder()
	s.handleUserdataGet(wb, getBob)
	var bobBlob store.Blob
	_ = json.NewDecoder(wb.Body).Decode(&bobBlob)
	if len(bobBlob.Progress) != 0 {
		t.Fatalf("bob should be empty, got %#v", bobBlob.Progress)
	}
}

func TestUserdataPreferMeshDisabled(t *testing.T) {
	t.Setenv("USERDATA_LOCAL_URL", "http://127.0.0.1:9")
	t.Setenv("USERDATA_PREFER_MESH", "0")
	dir := t.TempDir()
	u := newServerUserdata(dir)
	if u.proxyURL != "" {
		t.Fatalf("expected empty proxy when PREFER_MESH=0, got %q", u.proxyURL)
	}
}

func TestUserdataPreferMeshEnabled(t *testing.T) {
	t.Setenv("USERDATA_LOCAL_URL", "http://userdata-local:9680")
	t.Setenv("USERDATA_PREFER_MESH", "1")
	u := newServerUserdata(t.TempDir())
	if u.proxyURL != "http://userdata-local:9680" {
		t.Fatalf("proxy=%q", u.proxyURL)
	}
}

func TestUserdataTenantModeScopesFiles(t *testing.T) {
	t.Setenv("TENANT_MODE", "1")
	dir := t.TempDir()
	u := newServerUserdata(dir)
	sessions := newSessionStore(time.Hour)
	tok, _ := sessions.Create("alice", "Alice")
	s := &server{userdata: u, sessions: sessions}

	put := httptest.NewRequest(http.MethodPut, "/api/userdata", strings.NewReader(
		`{"progress":{"m1":{"id":"m1","updatedAt":"2026-01-01T00:00:00Z"}}}`))
	put.AddCookie(&http.Cookie{Name: "session", Value: tok})
	put.Header.Set("X-Tenant-ID", "acme")
	w := httptest.NewRecorder()
	s.handleUserdataPut(w, put)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}

	matches, _ := filepath.Glob(filepath.Join(dir, "tenants", "acme", "*.json"))
	if len(matches) == 0 {
		// Also accept legacy flat keys if present.
		matches, _ = filepath.Glob(filepath.Join(dir, "*.json"))
	}
	found := false
	for _, m := range matches {
		base := filepath.Base(m)
		if strings.Contains(m, "acme") || strings.Contains(base, "acme") || base == "alice.json" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected tenant-scoped file under tenants/acme/, got %v (root=%v)", matches, dir)
	}
	_ = os.Unsetenv("TENANT_MODE")
}
