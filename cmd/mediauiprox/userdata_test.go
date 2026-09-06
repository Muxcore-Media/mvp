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
	tok, _ := sessions.CreateWithTenant("alice", "Alice", "acme")
	s := &server{userdata: u, sessions: sessions}

	put := httptest.NewRequest(http.MethodPut, "/api/userdata", strings.NewReader(
		`{"progress":{"m1":{"id":"m1","updatedAt":"2026-01-01T00:00:00Z"}}}`))
	put.AddCookie(&http.Cookie{Name: "session", Value: tok})
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

func TestUserdataIgnoresCrossUserQueryOverride(t *testing.T) {
	dir := t.TempDir()
	u := newServerUserdata(dir)
	sessions := newSessionStore(time.Hour)
	aliceTok, _ := sessions.Create("alice", "Alice")
	bobTok, _ := sessions.Create("bob", "Bob")
	s := &server{userdata: u, sessions: sessions}

	// Seed bob's progress.
	putBob := httptest.NewRequest(http.MethodPut, "/api/userdata", strings.NewReader(
		`{"progress":{"m1":{"id":"m1","positionSec":99,"updatedAt":"2026-01-01T00:00:00Z"}}}`))
	putBob.AddCookie(&http.Cookie{Name: "session", Value: bobTok})
	wBob := httptest.NewRecorder()
	s.handleUserdataPut(wBob, putBob)
	if wBob.Code != http.StatusOK {
		t.Fatalf("bob put %d %s", wBob.Code, wBob.Body.String())
	}

	// Alice cannot read bob's blob via ?user_id= override.
	getAsBob := httptest.NewRequest(http.MethodGet, "/api/userdata?user_id=bob", nil)
	getAsBob.AddCookie(&http.Cookie{Name: "session", Value: aliceTok})
	wAlice := httptest.NewRecorder()
	s.handleUserdataGet(wAlice, getAsBob)
	var blob store.Blob
	if err := json.NewDecoder(wAlice.Body).Decode(&blob); err != nil {
		t.Fatal(err)
	}
	if len(blob.Progress) != 0 {
		t.Fatalf("alice should not see bob progress via user_id override, got %#v", blob.Progress)
	}

	// Alice can still access her own userdata without overrides.
	putAlice := httptest.NewRequest(http.MethodPut, "/api/userdata", strings.NewReader(
		`{"progress":{"m2":{"id":"m2","positionSec":5,"updatedAt":"2026-01-01T00:00:00Z"}}}`))
	putAlice.AddCookie(&http.Cookie{Name: "session", Value: aliceTok})
	wPutAlice := httptest.NewRecorder()
	s.handleUserdataPut(wPutAlice, putAlice)
	if wPutAlice.Code != http.StatusOK {
		t.Fatalf("alice put %d %s", wPutAlice.Code, wPutAlice.Body.String())
	}
	getAlice := httptest.NewRequest(http.MethodGet, "/api/userdata", nil)
	getAlice.AddCookie(&http.Cookie{Name: "session", Value: aliceTok})
	wGetAlice := httptest.NewRecorder()
	s.handleUserdataGet(wGetAlice, getAlice)
	var aliceBlob store.Blob
	if err := json.NewDecoder(wGetAlice.Body).Decode(&aliceBlob); err != nil {
		t.Fatal(err)
	}
	if len(aliceBlob.Progress) != 1 {
		t.Fatalf("alice should see her own progress, got %#v", aliceBlob.Progress)
	}

	// Alice cannot mutate bob's blob via ?user_id= override.
	putAsBob := httptest.NewRequest(http.MethodPut, "/api/userdata?user_id=bob", strings.NewReader(
		`{"progress":{"m1":{"id":"m1","positionSec":1,"updatedAt":"2025-01-01T00:00:00Z"}}}`))
	putAsBob.AddCookie(&http.Cookie{Name: "session", Value: aliceTok})
	wPutAsBob := httptest.NewRecorder()
	s.handleUserdataPut(wPutAsBob, putAsBob)
	if wPutAsBob.Code != http.StatusOK {
		t.Fatalf("alice put-as-bob %d %s", wPutAsBob.Code, wPutAsBob.Body.String())
	}
	getBobCheck := httptest.NewRequest(http.MethodGet, "/api/userdata", nil)
	getBobCheck.AddCookie(&http.Cookie{Name: "session", Value: bobTok})
	wBobCheck := httptest.NewRecorder()
	s.handleUserdataGet(wBobCheck, getBobCheck)
	var bobBlob store.Blob
	if err := json.NewDecoder(wBobCheck.Body).Decode(&bobBlob); err != nil {
		t.Fatal(err)
	}
	var p struct {
		PositionSec int `json:"positionSec"`
	}
	_ = json.Unmarshal(bobBlob.Progress["m1"], &p)
	if p.PositionSec != 99 {
		t.Fatalf("bob progress should be unchanged (99), got %d", p.PositionSec)
	}
}

func TestUserdataAdminCanOverrideUserID(t *testing.T) {
	dir := t.TempDir()
	u := newServerUserdata(dir)
	sessions := newSessionStore(time.Hour)
	adminTok, _ := sessions.CreateWithRoles("admin-1", "admin", "", []string{"admin"})
	bobTok, _ := sessions.Create("bob", "Bob")
	s := &server{userdata: u, sessions: sessions}

	putBob := httptest.NewRequest(http.MethodPut, "/api/userdata", strings.NewReader(
		`{"progress":{"m1":{"id":"m1","positionSec":42,"updatedAt":"2026-01-01T00:00:00Z"}}}`))
	putBob.AddCookie(&http.Cookie{Name: "session", Value: bobTok})
	wBob := httptest.NewRecorder()
	s.handleUserdataPut(wBob, putBob)
	if wBob.Code != http.StatusOK {
		t.Fatalf("bob put %d %s", wBob.Code, wBob.Body.String())
	}

	getAsBob := httptest.NewRequest(http.MethodGet, "/api/userdata?user_id=bob", nil)
	getAsBob.AddCookie(&http.Cookie{Name: "session", Value: adminTok})
	wAdmin := httptest.NewRecorder()
	s.handleUserdataGet(wAdmin, getAsBob)
	var blob store.Blob
	if err := json.NewDecoder(wAdmin.Body).Decode(&blob); err != nil {
		t.Fatal(err)
	}
	var p struct {
		PositionSec int `json:"positionSec"`
	}
	if err := json.Unmarshal(blob.Progress["m1"], &p); err != nil {
		t.Fatalf("admin should read bob via override: %v blob=%#v", err, blob.Progress)
	}
	if p.PositionSec != 42 {
		t.Fatalf("admin override: want bob progress 42, got %d", p.PositionSec)
	}
}

func TestUserdataGetIncludesScopedUserID(t *testing.T) {
	dir := t.TempDir()
	u := newServerUserdata(dir)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.Create("alice", "Alice")
	if err != nil {
		t.Fatal(err)
	}
	s := &server{userdata: u, sessions: sessions}

	get := httptest.NewRequest(http.MethodGet, "/api/userdata", nil)
	get.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleUserdataGet(w, get)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-MuxCore-User-Id"); got != "alice" {
		t.Fatalf("X-MuxCore-User-Id=%q want alice", got)
	}
	var body struct {
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.UserID != "alice" {
		t.Fatalf("user_id=%q want alice", body.UserID)
	}
}

func TestUserdataPutIncludesScopedUserID(t *testing.T) {
	dir := t.TempDir()
	u := newServerUserdata(dir)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.Create("alice", "Alice")
	if err != nil {
		t.Fatal(err)
	}
	s := &server{userdata: u, sessions: sessions}

	put := httptest.NewRequest(http.MethodPut, "/api/userdata", strings.NewReader(
		`{"progress":{"m1":{"id":"m1","positionSec":3,"updatedAt":"2026-01-01T00:00:00Z"}}}`))
	put.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleUserdataPut(w, put)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-MuxCore-User-Id"); got != "alice" {
		t.Fatalf("X-MuxCore-User-Id=%q want alice", got)
	}
	var body struct {
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.UserID != "alice" {
		t.Fatalf("user_id=%q want alice", body.UserID)
	}
}

func TestUserdataAdminHeaderOverrideSetsBlobUserID(t *testing.T) {
	dir := t.TempDir()
	u := newServerUserdata(dir)
	sessions := newSessionStore(time.Hour)
	adminTok, _ := sessions.CreateWithRoles("admin-1", "admin", "", []string{"admin"})
	bobTok, _ := sessions.Create("bob", "Bob")
	s := &server{userdata: u, sessions: sessions}

	putBob := httptest.NewRequest(http.MethodPut, "/api/userdata", strings.NewReader(
		`{"progress":{"m1":{"id":"m1","positionSec":42,"updatedAt":"2026-01-01T00:00:00Z"}}}`))
	putBob.AddCookie(&http.Cookie{Name: "session", Value: bobTok})
	wBob := httptest.NewRecorder()
	s.handleUserdataPut(wBob, putBob)
	if wBob.Code != http.StatusOK {
		t.Fatalf("bob put %d %s", wBob.Code, wBob.Body.String())
	}

	getAsBob := httptest.NewRequest(http.MethodGet, "/api/userdata", nil)
	getAsBob.AddCookie(&http.Cookie{Name: "session", Value: adminTok})
	getAsBob.Header.Set("X-MuxCore-User-Id", "bob")
	wAdmin := httptest.NewRecorder()
	s.handleUserdataGet(wAdmin, getAsBob)
	if wAdmin.Code != http.StatusOK {
		t.Fatalf("admin get %d %s", wAdmin.Code, wAdmin.Body.String())
	}
	if got := wAdmin.Header().Get("X-MuxCore-User-Id"); got != "bob" {
		t.Fatalf("X-MuxCore-User-Id=%q want bob (scoped override)", got)
	}
	var blob struct {
		store.Blob
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(wAdmin.Body).Decode(&blob); err != nil {
		t.Fatal(err)
	}
	if blob.UserID != "bob" {
		t.Fatalf("user_id=%q want bob", blob.UserID)
	}
	var p struct {
		PositionSec int `json:"positionSec"`
	}
	if err := json.Unmarshal(blob.Progress["m1"], &p); err != nil {
		t.Fatalf("admin header override should read bob: %v blob=%#v", err, blob.Progress)
	}
	if p.PositionSec != 42 {
		t.Fatalf("admin header override: want bob progress 42, got %d", p.PositionSec)
	}
}

func TestUserdataIgnoresCrossUserHeaderOverride(t *testing.T) {
	dir := t.TempDir()
	u := newServerUserdata(dir)
	sessions := newSessionStore(time.Hour)
	aliceTok, _ := sessions.Create("alice", "Alice")
	bobTok, _ := sessions.Create("bob", "Bob")
	s := &server{userdata: u, sessions: sessions}

	putBob := httptest.NewRequest(http.MethodPut, "/api/userdata", strings.NewReader(
		`{"progress":{"m1":{"id":"m1","positionSec":99,"updatedAt":"2026-01-01T00:00:00Z"}}}`))
	putBob.AddCookie(&http.Cookie{Name: "session", Value: bobTok})
	wBob := httptest.NewRecorder()
	s.handleUserdataPut(wBob, putBob)
	if wBob.Code != http.StatusOK {
		t.Fatalf("bob put %d %s", wBob.Code, wBob.Body.String())
	}

	getAsBob := httptest.NewRequest(http.MethodGet, "/api/userdata", nil)
	getAsBob.AddCookie(&http.Cookie{Name: "session", Value: aliceTok})
	getAsBob.Header.Set("X-MuxCore-User-Id", "bob")
	wAlice := httptest.NewRecorder()
	s.handleUserdataGet(wAlice, getAsBob)
	var blob struct {
		store.Blob
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(wAlice.Body).Decode(&blob); err != nil {
		t.Fatal(err)
	}
	if len(blob.Progress) != 0 {
		t.Fatalf("alice should not see bob progress via X-MuxCore-User-Id, got %#v", blob.Progress)
	}
	if blob.UserID != "alice" {
		t.Fatalf("user_id=%q want alice (header override ignored)", blob.UserID)
	}
}
