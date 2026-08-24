package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

func TestHandleInvitePeekAuthUnavailable(t *testing.T) {
	s := &server{authInternal: "http://127.0.0.1:1"}
	w := httptest.NewRecorder()
	s.handleInvitePeek(w, httptest.NewRequest(http.MethodGet, "/api/invite/peek?token=x", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", w.Code)
	}
}
