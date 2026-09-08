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

func TestHandleAddBook(t *testing.T) {
	var gotPath string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(raw), "The Dispossessed") {
			http.Error(w, "bad title", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "bk-new", "author_id": "au1", "title": "The Dispossessed", "year": 1974, "monitored": true,
		})
	}))
	t.Cleanup(up.Close)

	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions, booksHTTP: mustURL(up.URL)}
	mux := http.NewServeMux()
	s.registerLibraryRoutes(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/books/au1/books", strings.NewReader(`{"title":"The Dispossessed","year":1974}`))
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if gotPath != "/api/authors/au1/books" {
		t.Fatalf("upstream %q", gotPath)
	}
	var body struct {
		Added bool `json:"added"`
		Item  struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			Year  int32  `json:"year"`
		} `json:"item"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Added || body.Item.ID != "bk-new" || body.Item.Title != "The Dispossessed" || body.Item.Year != 1974 {
		t.Fatalf("%#v", body)
	}
}

func TestHandleAddComicIssue(t *testing.T) {
	var gotPath string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(raw), "Romance Dawn") {
			http.Error(w, "bad title", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "ci-new", "series_id": "s1", "title": "Romance Dawn", "number": "1", "year": 1997, "monitored": true,
		})
	}))
	t.Cleanup(up.Close)

	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions, comicsHTTP: mustURL(up.URL)}
	mux := http.NewServeMux()
	s.registerLibraryRoutes(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/comics/s1/issues", strings.NewReader(`{"title":"Romance Dawn","number":"1","year":1997}`))
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if gotPath != "/api/series/s1/issues" {
		t.Fatalf("upstream %q", gotPath)
	}
	var body struct {
		Added bool `json:"added"`
		Item  struct {
			ID     string `json:"id"`
			Title  string `json:"title"`
			Number string `json:"number"`
		} `json:"item"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Added || body.Item.ID != "ci-new" || body.Item.Title != "Romance Dawn" || body.Item.Number != "1" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleAddBookAuthor(t *testing.T) {
	var gotPath string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "au-new", "name": "Octavia E. Butler", "monitored": true})
	}))
	t.Cleanup(up.Close)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions, booksHTTP: mustURL(up.URL)}
	mux := http.NewServeMux()
	s.registerLibraryRoutes(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/books", strings.NewReader(`{"name":"Octavia E. Butler"}`))
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if gotPath != "/api/authors" {
		t.Fatalf("upstream %q", gotPath)
	}
}

func TestHandleAddComicSeries(t *testing.T) {
	var gotPath string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "cs-new", "title": "One Piece", "publisher": "Shueisha"})
	}))
	t.Cleanup(up.Close)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions, comicsHTTP: mustURL(up.URL)}
	mux := http.NewServeMux()
	s.registerLibraryRoutes(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/comics", strings.NewReader(`{"title":"One Piece","publisher":"Shueisha"}`))
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if gotPath != "/api/series" {
		t.Fatalf("upstream %q", gotPath)
	}
}

func TestHandleAddBookForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour), booksHTTP: mustURL("http://127.0.0.1:1")}
	req := httptest.NewRequest(http.MethodPost, "/api/books/au1/books", strings.NewReader(`{"title":"x"}`))
	req.SetPathValue("id", "au1")
	w := httptest.NewRecorder()
	s.handleAddBook(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleAddAudiobook(t *testing.T) {
	var postedAuthor, postedBook bool
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/authors":
			_ = json.NewEncoder(w).Encode([]any{})
		case r.Method == http.MethodPost && r.URL.Path == "/api/authors":
			postedAuthor = true
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "aa-new", "name": "Patrick Rothfuss"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/authors/aa-new/audiobooks":
			postedBook = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "ab-new", "author_id": "aa-new", "title": "The Name of the Wind", "year": 2007,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(up.Close)
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions, audiobooksHTTP: mustURL(up.URL)}
	mux := http.NewServeMux()
	s.registerLibraryRoutes(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/audiobooks", strings.NewReader(`{"author":"Patrick Rothfuss","title":"The Name of the Wind","year":2007}`))
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if !postedAuthor || !postedBook {
		t.Fatalf("author=%v book=%v", postedAuthor, postedBook)
	}
	var body struct {
		Added bool `json:"added"`
		Item  struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"item"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Added || body.Item.ID != "ab-new" || body.Item.Title != "The Name of the Wind" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleAddAudiobookForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour), audiobooksHTTP: mustURL("http://127.0.0.1:1")}
	req := httptest.NewRequest(http.MethodPost, "/api/audiobooks", strings.NewReader(`{"author":"x","title":"y"}`))
	w := httptest.NewRecorder()
	s.handleAddAudiobook(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}
