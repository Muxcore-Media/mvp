package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandleListAPIKeysForbidden(t *testing.T) {
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("alice", "alice", "", []string{"member"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions, authInternal: "http://127.0.0.1:1"}
	req := httptest.NewRequest(http.MethodGet, "/api/keys", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleListAPIKeys(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleListAPIKeysProxiesAuth(t *testing.T) {
	var gotAuth string
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tokens" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tokens": []map[string]any{{
				"id": "tok1", "name": "laptop", "prefix": "mct_abc12345", "user_id": "u1", "username": "pat",
				"secret": "must-not-leak",
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
	req := httptest.NewRequest(http.MethodGet, "/api/keys", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleListAPIKeys(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if gotAuth != "Bearer auth-local-token" {
		t.Fatalf("auth %q", gotAuth)
	}
	if strings.Contains(w.Body.String(), "must-not-leak") {
		t.Fatal("list must not echo secrets")
	}
	var body struct {
		Available bool `json:"available"`
		Keys      []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || body.Keys[0].Name != "laptop" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleCreateAPIKeyReturnsSecretOnce(t *testing.T) {
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/tokens" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":  map[string]any{"id": "tok-new", "name": "laptop", "prefix": "mct_new"},
			"secret": "mct_copyonce",
		})
	}))
	t.Cleanup(authSrv.Close)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithAuth("admin-1", "admin", "", []string{"admin"}, "auth-local-token")
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions, authInternal: authSrv.URL}
	req := httptest.NewRequest(http.MethodPost, "/api/keys", strings.NewReader(`{"name":"laptop"}`))
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleCreateAPIKey(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Secret string `json:"secret"`
		Token  struct {
			ID string `json:"id"`
		} `json:"token"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Secret != "mct_copyonce" || body.Token.ID != "tok-new" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleRotateAPIKeyProxiesAuth(t *testing.T) {
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/tokens/tok1/rotate" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":  map[string]any{"id": "tok2", "name": "laptop"},
			"secret": "mct_rotated",
		})
	}))
	t.Cleanup(authSrv.Close)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithAuth("admin-1", "admin", "", []string{"admin"}, "auth-local-token")
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions, authInternal: authSrv.URL}
	req := httptest.NewRequest(http.MethodPost, "/api/keys/tok1/rotate", nil)
	req.SetPathValue("id", "tok1")
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleRotateAPIKey(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "mct_rotated") {
		t.Fatalf("rotate must return the new secret: %s", w.Body.String())
	}
}

func TestHandleDeleteAPIKeyProxiesAuth(t *testing.T) {
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/tokens/tok1" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"removed": true, "id": "tok1"})
	}))
	t.Cleanup(authSrv.Close)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithAuth("admin-1", "admin", "", []string{"admin"}, "auth-local-token")
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions, authInternal: authSrv.URL}
	req := httptest.NewRequest(http.MethodDelete, "/api/keys/tok1", nil)
	req.SetPathValue("id", "tok1")
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleDeleteAPIKey(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
}
