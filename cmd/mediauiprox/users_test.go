package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandleListUsersForbidden(t *testing.T) {
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("alice", "alice", "", []string{"member"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions, authInternal: "http://127.0.0.1:1"}
	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleListUsers(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleListUsersProxiesAuth(t *testing.T) {
	var gotAuth string
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/users" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"users": []map[string]any{{
				"id": "u1", "username": "pat", "roles": []string{"user"}, "totp_enabled": false,
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
	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleListUsers(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if gotAuth != "Bearer auth-local-token" {
		t.Fatalf("auth %q", gotAuth)
	}
	var body struct {
		Available bool `json:"available"`
		Users     []struct {
			ID       string `json:"id"`
			Username string `json:"username"`
		} `json:"users"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.Users[0].Username != "pat" {
		t.Fatalf("%#v", body)
	}
}

func TestHandlePatchUserProxiesRoles(t *testing.T) {
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/api/users/u1" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"user": map[string]any{"id": "u1", "username": "pat", "roles": []string{"viewer"}},
		})
	}))
	t.Cleanup(authSrv.Close)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithAuth("admin-1", "admin", "", []string{"admin"}, "auth-local-token")
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions, authInternal: authSrv.URL}
	req := httptest.NewRequest(http.MethodPatch, "/api/users/u1", strings.NewReader(`{"role":"viewer"}`))
	req.SetPathValue("id", "u1")
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handlePatchUser(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
}

func TestHandleCreateUserProxiesAuth(t *testing.T) {
	var gotBody map[string]any
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/users" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"user": map[string]any{"id": "u2", "username": "sam", "roles": []string{"viewer"}},
		})
	}))
	t.Cleanup(authSrv.Close)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithAuth("admin-1", "admin", "home", []string{"admin"}, "auth-local-token")
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions, authInternal: authSrv.URL}
	req := httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(`{"username":"sam","password":"password123","role":"viewer"}`))
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleCreateUser(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if gotBody["username"] != "sam" || gotBody["tenantId"] != "home" {
		t.Fatalf("%#v", gotBody)
	}
	var body struct {
		User struct {
			Username string `json:"username"`
		} `json:"user"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.User.Username != "sam" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleCreateUserRejectsShortPassword(t *testing.T) {
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin-1", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions, authInternal: "http://127.0.0.1:1"}
	req := httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(`{"username":"sam","password":"short"}`))
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleCreateUser(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
}

func TestHandleSetUserPasswordProxiesAuth(t *testing.T) {
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/users/u1/password" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": "u1"})
	}))
	t.Cleanup(authSrv.Close)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithAuth("admin-1", "admin", "", []string{"admin"}, "auth-local-token")
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions, authInternal: authSrv.URL}
	req := httptest.NewRequest(http.MethodPost, "/api/users/u1/password", strings.NewReader(`{"password":"newpass99"}`))
	req.SetPathValue("id", "u1")
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleSetUserPassword(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
}

func TestHandleDeleteUserRejectsSelf(t *testing.T) {
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin-1", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions, authInternal: "http://127.0.0.1:1"}
	req := httptest.NewRequest(http.MethodDelete, "/api/users/admin-1", nil)
	req.SetPathValue("id", "admin-1")
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleDeleteUser(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
}

func TestHandleDeleteUserProxiesAuth(t *testing.T) {
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/users/u1" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"removed": true, "id": "u1"})
	}))
	t.Cleanup(authSrv.Close)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithAuth("admin-1", "admin", "", []string{"admin"}, "auth-local-token")
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions, authInternal: authSrv.URL}
	req := httptest.NewRequest(http.MethodDelete, "/api/users/u1", nil)
	req.SetPathValue("id", "u1")
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleDeleteUser(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
}
