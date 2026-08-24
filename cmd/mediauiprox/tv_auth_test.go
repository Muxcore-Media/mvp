package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandleTVLoginSuccess(t *testing.T) {
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login/device" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user_id":"u1","username":"ender","tenant_id":"home"}`))
	}))
	defer authSrv.Close()

	s := &server{
		authInternal: authSrv.URL,
		sessions:     newSessionStore(24 * time.Hour),
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/tv/login", strings.NewReader(`{"username":"ender","password":"secret"}`))
	s.handleTVLogin(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["username"] != "ender" || body["session_token"] == "" {
		t.Fatalf("body=%v", body)
	}
}

func TestHandleTVLoginRequires2FA(t *testing.T) {
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"requires_2fa":true,"partial_token":"partial-abc","user_id":"u1","username":"ender"}`))
	}))
	defer authSrv.Close()

	s := &server{authInternal: authSrv.URL, sessions: newSessionStore(24 * time.Hour)}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/tv/login", strings.NewReader(`{"username":"ender","password":"secret"}`))
	s.handleTVLogin(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["requires_2fa"] != true || body["partial_token"] != "partial-abc" {
		t.Fatalf("body=%v", body)
	}
}

func TestHandleTVLoginTOTPSuccess(t *testing.T) {
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login/device/totp" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user_id":"u1","username":"ender"}`))
	}))
	defer authSrv.Close()

	s := &server{authInternal: authSrv.URL, sessions: newSessionStore(24 * time.Hour)}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/tv/login/totp", strings.NewReader(`{"partial_token":"partial-abc","totp_code":"123456"}`))
	s.handleTVLoginTOTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["session_token"] == "" {
		t.Fatalf("body=%v", body)
	}
}

func TestHandleTVLoginInvalidCredentials(t *testing.T) {
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer authSrv.Close()

	s := &server{authInternal: authSrv.URL, sessions: newSessionStore(24 * time.Hour)}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/tv/login", strings.NewReader(`{"username":"ender","password":"wrong"}`))
	s.handleTVLogin(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", w.Code)
	}
}
