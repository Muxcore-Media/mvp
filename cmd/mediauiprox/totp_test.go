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

func TestHandleGetTOTPUnauthorized(t *testing.T) {
	s := &server{authInternal: "http://127.0.0.1:1"}
	w := httptest.NewRecorder()
	s.handleGetTOTP(w, httptest.NewRequest(http.MethodGet, "/api/totp", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleGetTOTPSoftFail(t *testing.T) {
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithAuth("u1", "sam", "home", []string{"user"}, "auth-local-token")
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions, authInternal: "http://127.0.0.1:1"}
	req := httptest.NewRequest(http.MethodGet, "/api/totp", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleGetTOTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Enabled   bool `json:"enabled"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available || body.Enabled {
		t.Fatalf("%#v", body)
	}
}

func TestHandleGetTOTPProxiesAuth(t *testing.T) {
	var gotAuth string
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/totp" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"enabled": true})
	}))
	t.Cleanup(authSrv.Close)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithAuth("u1", "sam", "home", []string{"user"}, "auth-local-token")
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions, authInternal: authSrv.URL}
	req := httptest.NewRequest(http.MethodGet, "/api/totp", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleGetTOTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if gotAuth != "Bearer auth-local-token" {
		t.Fatalf("auth %q", gotAuth)
	}
	var body struct {
		Available bool `json:"available"`
		Enabled   bool `json:"enabled"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || !body.Enabled {
		t.Fatalf("%#v", body)
	}
}

func TestHandleEnableVerifyDisableTOTP(t *testing.T) {
	var lastPath, lastMethod, lastBody string
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastPath = r.URL.Path
		lastMethod = r.Method
		raw, _ := io.ReadAll(r.Body)
		lastBody = string(raw)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/totp":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"enabled": true, "secret": "SECRETBASE32", "qr_code_url": "otpauth://totp/MuxCore:sam?secret=SECRETBASE32",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/totp/verify":
			_ = json.NewEncoder(w).Encode(map[string]any{"verified": true, "enabled": true})
		case r.Method == http.MethodDelete && r.URL.Path == "/api/totp":
			_ = json.NewEncoder(w).Encode(map[string]any{"enabled": false})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(authSrv.Close)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithAuth("u1", "sam", "home", []string{"user"}, "auth-local-token")
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions, authInternal: authSrv.URL}
	cookie := &http.Cookie{Name: "session", Value: tok}

	enableReq := httptest.NewRequest(http.MethodPost, "/api/totp", nil)
	enableReq.AddCookie(cookie)
	enableW := httptest.NewRecorder()
	s.handleEnableTOTP(enableW, enableReq)
	if enableW.Code != http.StatusOK || lastPath != "/api/totp" || lastMethod != http.MethodPost {
		t.Fatalf("enable %d %s %s", enableW.Code, lastMethod, lastPath)
	}
	var enabled struct {
		Secret    string `json:"secret"`
		QRCodeURL string `json:"qr_code_url"`
	}
	if err := json.NewDecoder(enableW.Body).Decode(&enabled); err != nil || enabled.Secret != "SECRETBASE32" {
		t.Fatalf("%#v %v", enabled, err)
	}

	verifyReq := httptest.NewRequest(http.MethodPost, "/api/totp/verify", strings.NewReader(`{"code":"123456"}`))
	verifyReq.AddCookie(cookie)
	verifyW := httptest.NewRecorder()
	s.handleVerifyTOTP(verifyW, verifyReq)
	if verifyW.Code != http.StatusOK || lastPath != "/api/totp/verify" || !strings.Contains(lastBody, "123456") {
		t.Fatalf("verify %d %s %s", verifyW.Code, lastPath, lastBody)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/totp", nil)
	delReq.AddCookie(cookie)
	delW := httptest.NewRecorder()
	s.handleDisableTOTP(delW, delReq)
	if delW.Code != http.StatusOK || lastMethod != http.MethodDelete {
		t.Fatalf("disable %d %s", delW.Code, lastMethod)
	}
}

func TestHandleVerifyTOTPRequiresCode(t *testing.T) {
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithAuth("u1", "sam", "home", []string{"user"}, "auth-local-token")
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions, authInternal: "http://127.0.0.1:1"}
	req := httptest.NewRequest(http.MethodPost, "/api/totp/verify", strings.NewReader(`{}`))
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleVerifyTOTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
}
