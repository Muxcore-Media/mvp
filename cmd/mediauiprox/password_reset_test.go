package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPasswordResetAPI(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "password-resets.json")
	s := &server{
		passwordResets: newPasswordResetStore(path, ""),
		sessions:       newSessionStore(time.Hour),
	}

	post := httptest.NewRequest(http.MethodPost, "/api/password-reset", strings.NewReader(`{"username":"alice","note":"lost phone"}`))
	post.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handlePasswordReset(w, post)
	if w.Code != http.StatusOK {
		t.Fatalf("post status %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["ok"] != true {
		t.Fatalf("expected ok, got %v", resp)
	}

	adminTok, err := s.sessions.CreateWithRoles("admin-1", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	get := httptest.NewRequest(http.MethodGet, "/api/password-reset", nil)
	get.AddCookie(&http.Cookie{Name: "session", Value: adminTok})
	gw := httptest.NewRecorder()
	s.handlePasswordReset(gw, get)
	if gw.Code != http.StatusOK {
		t.Fatalf("get status %d body=%s", gw.Code, gw.Body.String())
	}
	var list struct {
		Count    int `json:"count"`
		Requests []struct {
			Username string `json:"username"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(gw.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list.Count != 1 || list.Requests[0].Username != "alice" {
		t.Fatalf("expected alice pending, got %+v", list)
	}

	bad := httptest.NewRequest(http.MethodPost, "/api/password-reset", strings.NewReader(`{"username":""}`))
	bw := httptest.NewRecorder()
	s.handlePasswordReset(bw, bad)
	if bw.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", bw.Code)
	}
}

func TestHandleDismissPasswordReset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "password-resets.json")
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin-1", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{passwordResets: newPasswordResetStore(path, ""), sessions: sessions}
	post := httptest.NewRequest(http.MethodPost, "/api/password-reset", strings.NewReader(`{"username":"alice"}`))
	pw := httptest.NewRecorder()
	s.handlePasswordReset(pw, post)
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(pw.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatalf("create: %s", pw.Body.String())
	}

	req := httptest.NewRequest(http.MethodPost, "/api/password-reset/"+created.ID+"/dismiss", nil)
	req.SetPathValue("id", created.ID)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleDismissPasswordReset(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("dismiss %d %s", w.Code, w.Body.String())
	}
	if n := len(s.passwordResets.pending()); n != 0 {
		t.Fatalf("pending %d", n)
	}
}

func TestHandleSetPasswordReset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "password-resets.json")
	var gotPath, gotBody string
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/users" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"users": []map[string]any{{"id": "u-alice", "username": "alice"}},
			})
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/users/u-alice/password" {
			gotPath = r.URL.Path
			buf := make([]byte, 64)
			n, _ := r.Body.Read(buf)
			gotBody = string(buf[:n])
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": "u-alice"})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(authSrv.Close)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithAuth("admin-1", "admin", "", []string{"admin"}, "auth-local-token")
	if err != nil {
		t.Fatal(err)
	}
	s := &server{
		passwordResets: newPasswordResetStore(path, ""),
		sessions:       sessions,
		authInternal:   authSrv.URL,
	}
	post := httptest.NewRequest(http.MethodPost, "/api/password-reset", strings.NewReader(`{"username":"alice"}`))
	pw := httptest.NewRecorder()
	s.handlePasswordReset(pw, post)
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(pw.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/password-reset/"+created.ID+"/password", strings.NewReader(`{"password":"newpass99"}`))
	req.SetPathValue("id", created.ID)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleSetPasswordReset(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("set %d %s", w.Code, w.Body.String())
	}
	if gotPath != "/api/users/u-alice/password" || !strings.Contains(gotBody, "newpass99") {
		t.Fatalf("auth call path=%q body=%q", gotPath, gotBody)
	}
	if n := len(s.passwordResets.pending()); n != 0 {
		t.Fatalf("pending %d", n)
	}
}
