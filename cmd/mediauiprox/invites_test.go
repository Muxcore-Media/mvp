package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandleInvitePeekProxiesAuth(t *testing.T) {
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/invite/peek" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("token"); got != "abc123" {
			t.Fatalf("token=%s", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"valid":true,"username_hint":"ender"}`))
	}))
	defer authSrv.Close()

	s := &server{authInternal: authSrv.URL}
	w := httptest.NewRecorder()
	s.handleInvitePeek(w, httptest.NewRequest(http.MethodGet, "/api/invite/peek?token=abc123", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["valid"] != true || body["username_hint"] != "ender" {
		t.Fatalf("body=%v", body)
	}
}

func TestHandleInvitePeekRequiresToken(t *testing.T) {
	s := &server{authInternal: "http://127.0.0.1:1"}
	w := httptest.NewRecorder()
	s.handleInvitePeek(w, httptest.NewRequest(http.MethodGet, "/api/invite/peek", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleInviteRedeemProxiesPostBody(t *testing.T) {
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/invite/redeem" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), "invite-token") {
			t.Fatalf("body=%s", string(b))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer authSrv.Close()

	s := &server{authInternal: authSrv.URL}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/invite/redeem", strings.NewReader(`{"token":"invite-token","username":"newbie","password":"secret"}`))
	s.handleInviteRedeem(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
}

func TestHandleListInvitesForbidden(t *testing.T) {
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("alice", "alice", "", []string{"member"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions, authInternal: "http://127.0.0.1:1"}
	req := httptest.NewRequest(http.MethodGet, "/api/invites", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleListInvites(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleListInvitesProxiesAuth(t *testing.T) {
	var gotAuth string
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/invites" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"invites": []map[string]any{{
				"id": "inv1", "prefix": "abcd", "created_by": "admin", "role": "user",
				"max_uses": 1, "use_count": 0, "expires_at": "2026-09-15T00:00:00Z", "created_at": "2026-09-08T00:00:00Z",
			}},
		})
	}))
	t.Cleanup(authSrv.Close)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithAuth("admin-1", "admin", "home", []string{"admin"}, "auth-local-token")
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions, authInternal: authSrv.URL}
	req := httptest.NewRequest(http.MethodGet, "/api/invites", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleListInvites(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if gotAuth != "Bearer auth-local-token" {
		t.Fatalf("auth %q", gotAuth)
	}
	var body struct {
		Available bool `json:"available"`
		Invites   []struct {
			ID     string `json:"id"`
			Prefix string `json:"prefix"`
		} `json:"invites"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.Invites[0].ID != "inv1" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleCreateInviteReturnsJoinURL(t *testing.T) {
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/invites" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "inv2", "token": "raw-token", "prefix": "rawt", "role": "viewer",
			"max_uses": 1, "use_count": 0, "expires_at": "2026-09-15T00:00:00Z", "created_at": "2026-09-08T00:00:00Z",
		})
	}))
	t.Cleanup(authSrv.Close)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithAuth("admin-1", "admin", "", []string{"admin"}, "auth-local-token")
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions, authInternal: authSrv.URL, publicURL: "https://media.example"}
	req := httptest.NewRequest(http.MethodPost, "/api/invites", strings.NewReader(`{"role":"viewer","ttl_hours":24}`))
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleCreateInvite(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Invite struct {
			JoinURL string `json:"join_url"`
			Role    string `json:"role"`
		} `json:"invite"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Invite.JoinURL != "https://media.example/invite/raw-token" || body.Invite.Role != "viewer" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleRevokeInvite(t *testing.T) {
	var gotPath string
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "revoked", "id": "inv1"})
	}))
	t.Cleanup(authSrv.Close)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithAuth("admin-1", "admin", "", []string{"manager"}, "auth-local-token")
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions, authInternal: authSrv.URL}
	req := httptest.NewRequest(http.MethodDelete, "/api/invites/inv1", nil)
	req.SetPathValue("id", "inv1")
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleRevokeInvite(w, req)
	if w.Code != http.StatusOK || gotPath != "/api/invites/inv1" {
		t.Fatalf("status %d path %q %s", w.Code, gotPath, w.Body.String())
	}
}

func TestHandleInvitePeekAuthUnavailable(t *testing.T) {
	s := &server{authInternal: "http://127.0.0.1:1"}
	w := httptest.NewRecorder()
	s.handleInvitePeek(w, httptest.NewRequest(http.MethodGet, "/api/invite/peek?token=x", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", w.Code)
	}
}
