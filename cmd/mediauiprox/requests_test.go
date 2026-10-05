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

func TestProxyRequestMedia_ForwardsApproveWithSessionBearer(t *testing.T) {
	var gotPath, gotCaller, gotAuth, gotMethod string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotCaller = r.Header.Get("X-Caller-Id")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"requested"}`))
	}))
	t.Cleanup(upstream.Close)

	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithAuth("admin-1", "admin", "", []string{"admin"}, "auth-local-admin")
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
	if gotCaller != "admin-1" || gotAuth != "Bearer auth-local-admin" {
		t.Fatalf("caller=%q auth=%q", gotCaller, gotAuth)
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
	if gotCaller != "alice-id" || gotUser != "" {
		t.Fatalf("caller=%q user=%q (X-MuxCore-* must not be forwarded)", gotCaller, gotUser)
	}
}

// ADR-0019 / NFR-SEC-007: spoofed client identity and credential headers never
// reach request-media; the signed-in user's auth-local bearer does.
func TestProxyRequestMedia_StripsSpoofedIdentityHeaders(t *testing.T) {
	var got http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(upstream.Close)
	u, _ := url.Parse(upstream.URL)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithAuth("alice-id", "alice", "home", []string{"member"}, "alice-auth-token")
	if err != nil {
		t.Fatal(err)
	}
	s := &server{requestHTTP: u, sessions: sessions}

	spoof := func(req *http.Request) {
		req.Header.Set("X-Caller-Id", "admin")
		req.Header.Set("X-MuxCore-User", "admin")
		req.Header.Set("X-MuxCore-Roles", "admin")
		req.Header.Set("X-MuxCore-User-Id", "admin")
		req.Header.Set("X-Tenant-ID", "other-tenant")
		req.Header.Set("X-Auth-Claims-Tenant", "other-tenant")
		req.Header.Set("X-User-ID", "admin")
		req.Header.Set("X-Auth-Token", "stolen")
		req.Header.Set("X-Request-Trace", "keep-me")
	}

	// Signed-in user: identity comes from the session only.
	req := httptest.NewRequest(http.MethodPost, "/api/request", strings.NewReader("{}"))
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	spoof(req)
	s.proxyRequestMedia(httptest.NewRecorder(), req)
	if got.Get("X-Caller-Id") != "alice-id" {
		t.Fatalf("X-Caller-Id=%q", got.Get("X-Caller-Id"))
	}
	if got.Get("Authorization") != "Bearer alice-auth-token" {
		t.Fatalf("Authorization=%q", got.Get("Authorization"))
	}
	if got.Get("Cookie") != "" {
		t.Fatalf("cookie forwarded: %q", got.Get("Cookie"))
	}
	for _, h := range []string{"X-MuxCore-User", "X-MuxCore-Roles", "X-MuxCore-User-Id", "X-Tenant-ID", "X-Auth-Claims-Tenant", "X-User-ID", "X-Auth-Token"} {
		if v := got.Get(h); v != "" {
			t.Fatalf("%s forwarded: %q", h, v)
		}
	}
	if got.Get("X-Request-Trace") != "keep-me" {
		t.Fatal("ordinary client headers should still pass")
	}

	// Bearer-authenticated client (BFF session token): the BFF token itself is
	// replaced by the auth-local token.
	req = httptest.NewRequest(http.MethodGet, "/api/requests", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	spoof(req)
	s.proxyRequestMedia(httptest.NewRecorder(), req)
	if got.Get("Authorization") != "Bearer alice-auth-token" || got.Get("X-Caller-Id") != "alice-id" {
		t.Fatalf("bearer client: auth=%q caller=%q", got.Get("Authorization"), got.Get("X-Caller-Id"))
	}

	// No session: nothing identifying is forwarded at all.
	req = httptest.NewRequest(http.MethodGet, "/api/requests", nil)
	req.Header.Set("Authorization", "Bearer not-a-session")
	spoof(req)
	s.proxyRequestMedia(httptest.NewRecorder(), req)
	for _, h := range []string{"Authorization", "X-Caller-Id", "X-MuxCore-User", "X-Tenant-ID", "Cookie"} {
		if v := got.Get(h); v != "" {
			t.Fatalf("anonymous: %s forwarded: %q", h, v)
		}
	}
}

func TestReverseProxy_StripsClientIdentityHeaders(t *testing.T) {
	var got http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
	}))
	t.Cleanup(upstream.Close)
	u, _ := url.Parse(upstream.URL)
	req := httptest.NewRequest(http.MethodGet, "/stream/movies/1", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "bff-session"})
	req.Header.Set("Authorization", "Bearer bff-session")
	req.Header.Set("X-Caller-Id", "admin")
	req.Header.Set("X-Tenant-ID", "t2")
	req.Header.Set("Range", "bytes=0-1")
	reverseProxy(u).ServeHTTP(httptest.NewRecorder(), req)
	for _, h := range []string{"Cookie", "Authorization", "X-Caller-Id", "X-Tenant-ID"} {
		if v := got.Get(h); v != "" {
			t.Fatalf("%s forwarded: %q", h, v)
		}
	}
	if got.Get("Range") != "bytes=0-1" {
		t.Fatal("Range must pass through")
	}
}
