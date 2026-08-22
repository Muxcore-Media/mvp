package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestQuickConnectRegisterApproveSession(t *testing.T) {
	dir := t.TempDir()
	s := &server{
		sessions:     newSessionStore(24 * time.Hour),
		quickconnect: newQuickConnectStore(dir),
	}

	// TV registers a code.
	regBody := `{"action":"register"}`
	regW := httptest.NewRecorder()
	s.handleQuickConnect(regW, httptest.NewRequest(http.MethodPost, "/api/quickconnect", strings.NewReader(regBody)))
	if regW.Code != http.StatusOK {
		t.Fatalf("register status=%d body=%s", regW.Code, regW.Body.String())
	}
	var reg map[string]any
	if err := json.Unmarshal(regW.Body.Bytes(), &reg); err != nil {
		t.Fatal(err)
	}
	code, _ := reg["code"].(string)
	if len(code) < 4 {
		t.Fatalf("expected code, got %#v", reg)
	}

	// Poll before approval.
	pollW := httptest.NewRecorder()
	s.handleQuickConnect(pollW, httptest.NewRequest(http.MethodGet, "/api/quickconnect?code="+code, nil))
	if pollW.Code != http.StatusOK {
		t.Fatalf("poll status=%d", pollW.Code)
	}
	var poll map[string]any
	if err := json.Unmarshal(pollW.Body.Bytes(), &poll); err != nil {
		t.Fatal(err)
	}
	if poll["approved"] == true {
		t.Fatalf("expected not approved yet: %#v", poll)
	}

	// Logged-in web user approves (simulate session cookie).
	adminTok, err := s.sessions.Create("user-1", "admin")
	if err != nil {
		t.Fatal(err)
	}
	approveBody := `{"code":"` + code + `"}`
	approveReq := httptest.NewRequest(http.MethodPost, "/api/quickconnect", strings.NewReader(approveBody))
	approveReq.AddCookie(&http.Cookie{Name: "session", Value: adminTok})
	approveW := httptest.NewRecorder()
	s.handleQuickConnect(approveW, approveReq)
	if approveW.Code != http.StatusOK {
		t.Fatalf("approve status=%d body=%s", approveW.Code, approveW.Body.String())
	}

	// TV polls again and receives session token.
	poll2W := httptest.NewRecorder()
	s.handleQuickConnect(poll2W, httptest.NewRequest(http.MethodGet, "/api/quickconnect?code="+code, nil))
	if poll2W.Code != http.StatusOK {
		t.Fatalf("poll2 status=%d body=%s", poll2W.Code, poll2W.Body.String())
	}
	var poll2 map[string]any
	if err := json.Unmarshal(poll2W.Body.Bytes(), &poll2); err != nil {
		t.Fatal(err)
	}
	if poll2["approved"] != true {
		t.Fatalf("expected approved: %#v", poll2)
	}
	tok, _ := poll2["session_token"].(string)
	if tok == "" {
		t.Fatalf("expected session_token: %#v", poll2)
	}
	if !s.sessions.Valid(tok) {
		t.Fatal("session token not valid")
	}
}

func TestQuickConnectApproveRequiresLogin(t *testing.T) {
	dir := t.TempDir()
	s := &server{
		sessions:     newSessionStore(24 * time.Hour),
		quickconnect: newQuickConnectStore(dir),
	}
	regW := httptest.NewRecorder()
	s.handleQuickConnect(regW, httptest.NewRequest(http.MethodPost, "/api/quickconnect", strings.NewReader(`{"action":"register"}`)))
	var reg map[string]any
	json.Unmarshal(regW.Body.Bytes(), &reg)
	code := reg["code"].(string)

	approveW := httptest.NewRecorder()
	s.handleQuickConnect(approveW, httptest.NewRequest(http.MethodPost, "/api/quickconnect", strings.NewReader(`{"code":"`+code+`"}`)))
	if approveW.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", approveW.Code)
	}
}
