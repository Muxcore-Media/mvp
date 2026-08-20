package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestPasswordResetAPI(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "password-resets.json")
	s := &server{passwordResets: newPasswordResetStore(path, "")}

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

	get := httptest.NewRequest(http.MethodGet, "/api/password-reset", nil)
	gw := httptest.NewRecorder()
	s.handlePasswordReset(gw, get)
	if gw.Code != http.StatusOK {
		t.Fatalf("get status %d", gw.Code)
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
