package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlePatchLibraryPlusMonitored(t *testing.T) {
	var gotPath string
	var gotBody []byte
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		if r.Method != http.MethodPatch {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "x", "monitored": false})
	}))
	t.Cleanup(up.Close)

	s := &server{
		booksHTTP:      mustURL(up.URL),
		comicsHTTP:     mustURL(up.URL),
		audiobooksHTTP: mustURL(up.URL),
	}
	mux := http.NewServeMux()
	s.registerLibraryRoutes(mux)

	cases := []struct {
		path     string
		upstream string
	}{
		{"/api/books/a1", "/api/authors/a1"},
		{"/api/books/works/b1", "/api/books/b1"},
		{"/api/comics/s1", "/api/series/s1"},
		{"/api/comics/issues/i1", "/api/issues/i1"},
		{"/api/audiobooks/ab1", "/api/audiobooks/ab1"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodPatch, tc.path, bytes.NewBufferString(`{"monitored":false}`))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status %d body=%s", tc.path, w.Code, w.Body.String())
		}
		if gotPath != tc.upstream {
			t.Fatalf("%s upstream path %q want %q", tc.path, gotPath, tc.upstream)
		}
		if !bytes.Contains(gotBody, []byte(`"monitored":false`)) {
			t.Fatalf("%s body %s", tc.path, gotBody)
		}
		var body struct {
			Monitored bool `json:"monitored"`
		}
		if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Monitored {
			t.Fatalf("%s expected unmonitored", tc.path)
		}
	}
}

func TestHandlePatchBookAuthorUnavailable(t *testing.T) {
	s := &server{}
	req := httptest.NewRequest(http.MethodPatch, "/api/books/a1", bytes.NewBufferString(`{"monitored":true}`))
	req.SetPathValue("id", "a1")
	w := httptest.NewRecorder()
	s.handlePatchBookAuthor(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandlePatchBookAuthorRootFolder(t *testing.T) {
	var gotPath string
	var gotBody []byte
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		if r.Method != http.MethodPatch {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "a1", "path": "/data/books", "monitored": true})
	}))
	t.Cleanup(up.Close)

	s := &server{booksHTTP: mustURL(up.URL)}
	mux := http.NewServeMux()
	s.registerLibraryRoutes(mux)
	req := httptest.NewRequest(http.MethodPatch, "/api/books/a1", bytes.NewBufferString(`{"root_folder_path":"/data/books"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	if gotPath != "/api/authors/a1" {
		t.Fatalf("upstream path %q", gotPath)
	}
	if !bytes.Contains(gotBody, []byte(`"path":"/data/books"`)) {
		t.Fatalf("upstream body %s", gotBody)
	}
	var body struct {
		RootFolderPath string `json:"root_folder_path"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.RootFolderPath != "/data/books" {
		t.Fatalf("root %q", body.RootFolderPath)
	}
}

func TestHandlePatchComicSeriesRootFolder(t *testing.T) {
	var gotPath string
	var gotBody []byte
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		if r.Method != http.MethodPatch {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "s1", "path": "/data/comics", "monitored": true})
	}))
	t.Cleanup(up.Close)

	s := &server{comicsHTTP: mustURL(up.URL)}
	mux := http.NewServeMux()
	s.registerLibraryRoutes(mux)
	req := httptest.NewRequest(http.MethodPatch, "/api/comics/s1", bytes.NewBufferString(`{"root_folder_path":"/data/comics"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	if gotPath != "/api/series/s1" {
		t.Fatalf("upstream path %q", gotPath)
	}
	if !bytes.Contains(gotBody, []byte(`"path":"/data/comics"`)) {
		t.Fatalf("upstream body %s", gotBody)
	}
	var body struct {
		RootFolderPath string `json:"root_folder_path"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.RootFolderPath != "/data/comics" {
		t.Fatalf("root %q", body.RootFolderPath)
	}
}

func TestHandlePatchAudiobookRootFolder(t *testing.T) {
	var patched []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/audiobooks/ab1":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"author":    map[string]any{"id": "au1", "name": "Andy Weir"},
				"audiobook": map[string]any{"id": "ab1", "author_id": "au1", "title": "Project Hail Mary"},
			})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/authors/au1":
			patched = append(patched, r.URL.Path)
			raw, _ := io.ReadAll(r.Body)
			if !bytes.Contains(raw, []byte(`"path":"/data/audiobooks"`)) {
				http.Error(w, "missing path", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "au1", "path": "/data/audiobooks"})
		default:
			http.Error(w, r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(up.Close)

	s := &server{audiobooksHTTP: mustURL(up.URL)}
	mux := http.NewServeMux()
	s.registerLibraryRoutes(mux)
	req := httptest.NewRequest(http.MethodPatch, "/api/audiobooks/ab1", bytes.NewBufferString(`{"root_folder_path":"/data/audiobooks"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	if len(patched) != 1 || patched[0] != "/api/authors/au1" {
		t.Fatalf("patched %#v", patched)
	}
	var body struct {
		RootFolderPath string `json:"root_folder_path"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.RootFolderPath != "/data/audiobooks" {
		t.Fatalf("root %q", body.RootFolderPath)
	}
}
