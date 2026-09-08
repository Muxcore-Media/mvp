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

func TestPasskeyChallenge(t *testing.T) {
	t.Parallel()
	if got := passkeyChallenge(map[string]any{"publicKey": map[string]any{"challenge": "abc"}}); got != "abc" {
		t.Fatalf("got %q", got)
	}
}

func TestHandleListPasskeysUnauthorized(t *testing.T) {
	s := &server{authInternal: "http://127.0.0.1:1"}
	w := httptest.NewRecorder()
	s.handleListPasskeys(w, httptest.NewRequest(http.MethodGet, "/api/passkeys", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleListPasskeysSoftFail(t *testing.T) {
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithAuth("u1", "sam", "home", []string{"user"}, "auth-local-token")
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions, authInternal: "http://127.0.0.1:1"}
	req := httptest.NewRequest(http.MethodGet, "/api/passkeys", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleListPasskeys(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil || body.Available {
		t.Fatalf("%#v %v", body, err)
	}
}

func TestHandleListBeginCompleteDeletePasskeys(t *testing.T) {
	var lastPath, lastMethod, lastQuery, lastBody string
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastPath = r.URL.Path
		lastMethod = r.Method
		lastQuery = r.URL.RawQuery
		raw, _ := io.ReadAll(r.Body)
		lastBody = string(raw)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/webauthn/credentials":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"credentials": []map[string]any{{
					"id": "cred1", "credential_type": "public-key", "created_at": "2026-09-08T00:00:00Z",
				}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/webauthn/register/begin":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"publicKey": map[string]any{"challenge": "chal-1", "rp": map[string]any{"id": "localhost"}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/webauthn/register/complete":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "registered"})
		case r.Method == http.MethodDelete && r.URL.Path == "/api/webauthn/credentials/cred1":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "deleted"})
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

	listReq := httptest.NewRequest(http.MethodGet, "/api/passkeys", nil)
	listReq.AddCookie(cookie)
	listW := httptest.NewRecorder()
	s.handleListPasskeys(listW, listReq)
	if listW.Code != http.StatusOK || lastPath != "/api/webauthn/credentials" {
		t.Fatalf("list %d %s %s", listW.Code, lastPath, listW.Body.String())
	}
	var listed struct {
		Available bool `json:"available"`
		Passkeys  []struct {
			ID string `json:"id"`
		} `json:"passkeys"`
	}
	if err := json.NewDecoder(listW.Body).Decode(&listed); err != nil || !listed.Available || listed.Passkeys[0].ID != "cred1" {
		t.Fatalf("%#v %v", listed, err)
	}

	beginReq := httptest.NewRequest(http.MethodPost, "/api/passkeys/register/begin", nil)
	beginReq.AddCookie(cookie)
	beginW := httptest.NewRecorder()
	s.handleBeginPasskeyRegister(beginW, beginReq)
	if beginW.Code != http.StatusOK || lastMethod != http.MethodPost {
		t.Fatalf("begin %d %s", beginW.Code, lastMethod)
	}
	var began struct {
		Challenge string `json:"challenge"`
	}
	if err := json.NewDecoder(beginW.Body).Decode(&began); err != nil || began.Challenge != "chal-1" {
		t.Fatalf("%#v %v", began, err)
	}

	completeReq := httptest.NewRequest(http.MethodPost, "/api/passkeys/register/complete?challenge=chal-1", strings.NewReader(`{"id":"cred-new"}`))
	completeReq.AddCookie(cookie)
	completeW := httptest.NewRecorder()
	s.handleCompletePasskeyRegister(completeW, completeReq)
	if completeW.Code != http.StatusOK || lastPath != "/api/webauthn/register/complete" || lastQuery != "challenge=chal-1" || !strings.Contains(lastBody, "cred-new") {
		t.Fatalf("complete %d %s %s %s", completeW.Code, lastPath, lastQuery, lastBody)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/passkeys/cred1", nil)
	delReq.SetPathValue("id", "cred1")
	delReq.AddCookie(cookie)
	delW := httptest.NewRecorder()
	s.handleDeletePasskey(delW, delReq)
	if delW.Code != http.StatusOK || lastMethod != http.MethodDelete || lastPath != "/api/webauthn/credentials/cred1" {
		t.Fatalf("delete %d %s %s", delW.Code, lastMethod, lastPath)
	}
}
