package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestExchangeAuthCodeSuccess(t *testing.T) {
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login/exchange" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":     "jwt",
			"user_id":   "u1",
			"username":  "alice",
			"tenant_id": "t1",
		})
	}))
	defer authSrv.Close()

	s := &server{authInternal: authSrv.URL}
	got, err := s.exchangeAuthCode("good-code")
	if err != nil {
		t.Fatalf("exchangeAuthCode: %v", err)
	}
	if got.UserID != "u1" || got.Username != "alice" || got.TenantID != "t1" {
		t.Fatalf("unexpected result: %#v", got)
	}
}

func TestExchangeAuthCodeUpstreamTimeout(t *testing.T) {
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer authSrv.Close()

	oldClient := authUpstreamClient
	authUpstreamClient = &http.Client{Timeout: 50 * time.Millisecond}
	defer func() { authUpstreamClient = oldClient }()

	s := &server{authInternal: authSrv.URL}
	_, err := s.exchangeAuthCode("slow")
	if err == nil {
		t.Fatal("expected timeout/unavailable error")
	}
	status, message, code := authExchangeHTTP(err)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", status)
	}
	if code != "auth.unavailable" {
		t.Fatalf("code=%q want auth.unavailable", code)
	}
	if message != "auth unavailable" {
		t.Fatalf("message=%q", message)
	}
}

func TestHandleMobileSessionAuthExchangeErrorShape(t *testing.T) {
	s := &server{
		authInternal: "http://127.0.0.1:1",
		sessions:     newSessionStore(time.Hour),
	}
	req := httptest.NewRequest(http.MethodPost, "/api/mobile/session", strings.NewReader(`{"code":"bad"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleMobileSession(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
	var out map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["error"] != "auth unavailable" || out["code"] != "auth.unavailable" {
		t.Fatalf("unexpected body: %#v", out)
	}
}
