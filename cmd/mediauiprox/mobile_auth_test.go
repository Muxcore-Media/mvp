package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSafeMobileRedirectURI(t *testing.T) {
	if got := safeMobileRedirectURI(""); got != defaultMobileRedirectURI {
		t.Fatalf("empty: got %q", got)
	}
	if got := safeMobileRedirectURI("muxcore://auth/callback"); got != "muxcore://auth/callback" {
		t.Fatalf("valid: got %q", got)
	}
	if got := safeMobileRedirectURI("https://evil.example/callback"); got != defaultMobileRedirectURI {
		t.Fatalf("https rejected: got %q", got)
	}
	if got := safeMobileRedirectURI("muxcore://other/callback"); got != defaultMobileRedirectURI {
		t.Fatalf("wrong host rejected: got %q", got)
	}
}

func TestAppendCodeToRedirect(t *testing.T) {
	got, err := appendCodeToRedirect("muxcore://auth/callback", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "code=abc123") {
		t.Fatalf("expected code in redirect, got %q", got)
	}
}

func TestHandleMobileAuthLoginRedirectsToAuth(t *testing.T) {
	s := &server{
		authHTTP:  "http://auth.test",
		publicURL: "https://mux.test",
	}
	req := httptest.NewRequest(http.MethodGet, "/api/mobile/auth/login?redirect_uri=muxcore%3A%2F%2Fauth%2Fcallback", nil)
	w := httptest.NewRecorder()
	s.handleMobileAuthLogin(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "http://auth.test/login?redirect=") {
		t.Fatalf("unexpected location: %s", loc)
	}
	if !strings.Contains(loc, "mux.test") || !strings.Contains(loc, "mobile%2Fauth%2Fdone") {
		t.Fatalf("done callback missing from redirect: %s", loc)
	}
}

func TestHandleMobileAuthDoneRedirectsToApp(t *testing.T) {
	s := &server{}
	req := httptest.NewRequest(http.MethodGet, "/api/mobile/auth/done?code=onetwo&redirect_uri=muxcore%3A%2F%2Fauth%2Fcallback", nil)
	w := httptest.NewRecorder()
	s.handleMobileAuthDone(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "muxcore://auth/callback") || !strings.Contains(loc, "code=onetwo") {
		t.Fatalf("unexpected location: %s", loc)
	}
}

func TestHandleMobileSessionCreatesBearerToken(t *testing.T) {
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login/exchange" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":    "jwt",
			"user_id":  "u1",
			"username": "alice",
		})
	}))
	defer authSrv.Close()

	s := &server{
		authInternal: authSrv.URL,
		sessions:     newSessionStore(time.Hour),
	}
	req := httptest.NewRequest(http.MethodPost, "/api/mobile/session", strings.NewReader(`{"code":"good"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleMobileSession(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["session_token"] == "" || out["username"] != "alice" {
		t.Fatalf("unexpected body: %#v", out)
	}
}
