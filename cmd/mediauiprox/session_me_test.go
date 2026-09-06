package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSessionMeReturnsHouseholdUserID(t *testing.T) {
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("alice", "Alice", "home", []string{"member"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions}

	for _, path := range []string{"/api/session", "/api/me"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: tok})
		w := httptest.NewRecorder()
		s.handleSessionMe(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status %d %s", path, w.Code, w.Body.String())
		}
		if got := w.Header().Get("X-MuxCore-User-Id"); got != "alice" {
			t.Fatalf("%s X-MuxCore-User-Id=%q want alice", path, got)
		}
		var body struct {
			UserID   string   `json:"user_id"`
			Username string   `json:"username"`
			TenantID string   `json:"tenant_id"`
			Roles    []string `json:"roles"`
		}
		if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.UserID != "alice" || body.Username != "Alice" || body.TenantID != "home" {
			t.Fatalf("%s identity %#v", path, body)
		}
		if len(body.Roles) != 1 || body.Roles[0] != "member" {
			t.Fatalf("%s roles %#v", path, body.Roles)
		}
	}
}

func TestSessionMeUnauthorizedWithoutSession(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	w := httptest.NewRecorder()
	s.handleSessionMe(w, httptest.NewRequest(http.MethodGet, "/api/session", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d want 401", w.Code)
	}
}

func TestSessionMeBearerToken(t *testing.T) {
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.Create("u1", "ender")
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions}
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	s.handleSessionMe(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.UserID != "u1" {
		t.Fatalf("user_id=%q want u1", body.UserID)
	}
}
