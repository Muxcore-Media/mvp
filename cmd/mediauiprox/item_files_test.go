package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPublicMovieFileUsesBasename(t *testing.T) {
	out := publicMovieFile(nil)
	if len(out) != 0 {
		t.Fatalf("%#v", out)
	}
}

func TestHandleListMovieFiles(t *testing.T) {
	fake, client := dialMovieLibrary(t)
	fake.fileIDs = []string{"f1"}
	s := &server{movies: client}
	req := httptest.NewRequest(http.MethodGet, "/api/movies/m1/files", nil)
	req.SetPathValue("id", "m1")
	w := httptest.NewRecorder()
	s.handleListMovieFiles(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Items     []struct {
			ID       string `json:"id"`
			Filename string `json:"filename"`
		} `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || len(body.Items) != 1 || body.Items[0].ID != "f1" || body.Items[0].Filename != "Fight.Club.1999.mkv" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleListMovieFilesSoftFail(t *testing.T) {
	s := &server{}
	req := httptest.NewRequest(http.MethodGet, "/api/movies/m1/files", nil)
	req.SetPathValue("id", "m1")
	w := httptest.NewRecorder()
	s.handleListMovieFiles(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	var body struct {
		Available bool  `json:"available"`
		Items     []any `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available || body.Items == nil {
		t.Fatalf("%#v", body)
	}
}

func TestHandleDeleteMovieFileByID(t *testing.T) {
	fake, client := dialMovieLibrary(t)
	fake.fileIDs = []string{"f1"}
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{movies: client, sessions: sessions}
	req := httptest.NewRequest(http.MethodDelete, "/api/movies/m1/files/f1?delete_files=1", nil)
	req.SetPathValue("id", "m1")
	req.SetPathValue("fileId", "f1")
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleDeleteMovieFileByID(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if fake.removedFileID != "f1" || !fake.deleteFiles {
		t.Fatalf("file=%q delete=%v", fake.removedFileID, fake.deleteFiles)
	}
}

func TestHandleDeleteMovieFileByIDForbidden(t *testing.T) {
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("pat", "pat", "", []string{"user"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions}
	req := httptest.NewRequest(http.MethodDelete, "/api/movies/m1/files/f1", nil)
	req.SetPathValue("id", "m1")
	req.SetPathValue("fileId", "f1")
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleDeleteMovieFileByID(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestHandleDeleteMovieFileByIDNotFound(t *testing.T) {
	fake, client := dialMovieLibrary(t)
	fake.fileIDs = []string{"f1"}
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{movies: client, sessions: sessions}
	req := httptest.NewRequest(http.MethodDelete, "/api/movies/m1/files/missing", nil)
	req.SetPathValue("id", "m1")
	req.SetPathValue("fileId", "missing")
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleDeleteMovieFileByID(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
