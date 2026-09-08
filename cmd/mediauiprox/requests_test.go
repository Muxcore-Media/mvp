package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestProxyRequestMedia_PassesStatusDetailFields(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/requests" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"r1","itemType":"movie","status":"stalled","status_detail":"no peers","status_label":"Stalled — no peers"}]`))
	}))
	t.Cleanup(upstream.Close)

	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	s := &server{requestHTTP: u}
	w := httptest.NewRecorder()
	s.proxyRequestMedia(w, httptest.NewRequest(http.MethodGet, "/api/requests", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows=%d", len(rows))
	}
	if rows[0]["status_detail"] != "no peers" {
		t.Fatalf("status_detail=%v", rows[0]["status_detail"])
	}
	if rows[0]["status_label"] != "Stalled — no peers" {
		t.Fatalf("status_label=%v", rows[0]["status_label"])
	}
}

func TestProxyRequestMedia_OldResponseWithoutStatusFields(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/requests" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"r2","itemType":"tv","status":"downloading","title":"Show"}]`))
	}))
	t.Cleanup(upstream.Close)

	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	s := &server{requestHTTP: u}
	w := httptest.NewRecorder()
	s.proxyRequestMedia(w, httptest.NewRequest(http.MethodGet, "/api/requests", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["id"] != "r2" || rows[0]["status"] != "downloading" {
		t.Fatalf("unexpected row %#v", rows[0])
	}
	if _, ok := rows[0]["status_detail"]; ok {
		t.Fatalf("status_detail should be absent, got %#v", rows[0]["status_detail"])
	}
	if _, ok := rows[0]["status_label"]; ok {
		t.Fatalf("status_label should be absent, got %#v", rows[0]["status_label"])
	}
}

func TestProxyRequestMedia_ForwardsQueryAndPath(t *testing.T) {
	var gotPath, gotQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(upstream.Close)

	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	s := &server{requestHTTP: u}
	w := httptest.NewRecorder()
	s.proxyRequestMedia(w, httptest.NewRequest(http.MethodGet, "/api/requests?status=downloading", nil))
	if gotPath != "/api/requests" || gotQuery != "status=downloading" {
		t.Fatalf("upstream path=%q query=%q", gotPath, gotQuery)
	}
}

func TestProxyRequestMedia_ForwardsApproveWithSessionRoles(t *testing.T) {
	var gotPath, gotUser, gotRoles, gotMethod string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotUser = r.Header.Get("X-MuxCore-User")
		gotRoles = r.Header.Get("X-MuxCore-Roles")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"requested"}`))
	}))
	t.Cleanup(upstream.Close)

	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin-1", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{requestHTTP: u, sessions: sessions}
	req := httptest.NewRequest(http.MethodPost, "/api/requests/req-1/approve", strings.NewReader("{}"))
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.proxyRequestMedia(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if gotMethod != http.MethodPost || gotPath != "/api/requests/req-1/approve" {
		t.Fatalf("upstream %s %s", gotMethod, gotPath)
	}
	if gotUser != "admin" || gotRoles != "admin" {
		t.Fatalf("user=%q roles=%q", gotUser, gotRoles)
	}
}

func TestProxyRequestMedia_ForwardsCallerIDAndPolicyPath(t *testing.T) {
	var gotPath, gotCaller, gotUser string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotCaller = r.Header.Get("X-Caller-Id")
		gotUser = r.Header.Get("X-MuxCore-User")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"canRequest":true,"maxPerWeek":0}`))
	}))
	t.Cleanup(upstream.Close)
	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("alice-id", "alice", "", []string{"member"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{requestHTTP: u, sessions: sessions}
	req := httptest.NewRequest(http.MethodGet, "/api/request-policy", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.proxyRequestMedia(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if gotPath != "/api/request-policy" {
		t.Fatalf("path %q", gotPath)
	}
	if gotCaller != "alice-id" || gotUser != "alice" {
		t.Fatalf("caller=%q user=%q", gotCaller, gotUser)
	}
}
