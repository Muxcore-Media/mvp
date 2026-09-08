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

func privilegedLibraryImport(t *testing.T, method, path, body string) (*server, *http.Request) {
	t.Helper()
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	return s, req
}

func TestHandleImportLibraryPlus(t *testing.T) {
	var gotPath string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(raw), "/library/Dune.epub") {
			http.Error(w, "bad path", http.StatusBadRequest)
			return
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/books/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "f1", "stream_url": "/api/files/f1/stream"})
		case strings.HasPrefix(r.URL.Path, "/api/issues/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "i1", "has_file": true, "path": "/library/Dune.epub"})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "af1", "stream_url": "/api/files/af1/stream"})
		}
	}))
	t.Cleanup(up.Close)

	cases := []struct {
		path     string
		upstream string
		stream   string
	}{
		{"/api/books/works/b1/import", "/api/books/b1/import", "/stream/books/f1"},
		{"/api/comics/issues/i1/import", "/api/issues/i1/import", "/stream/comics/i1"},
		{"/api/audiobooks/ab1/import", "/api/audiobooks/ab1/import", "/stream/audiobooks/af1"},
	}
	for _, tc := range cases {
		s, req := privilegedLibraryImport(t, http.MethodPost, tc.path, `{"path":"/library/Dune.epub"}`)
		s.booksHTTP = mustURL(up.URL)
		s.comicsHTTP = mustURL(up.URL)
		s.audiobooksHTTP = mustURL(up.URL)
		mux := http.NewServeMux()
		s.registerLibraryRoutes(mux)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status %d %s", tc.path, w.Code, w.Body.String())
		}
		if gotPath != tc.upstream {
			t.Fatalf("%s upstream %q want %q", tc.path, gotPath, tc.upstream)
		}
		var body struct {
			StreamURL string `json:"stream_url"`
		}
		if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.StreamURL != tc.stream {
			t.Fatalf("%s stream=%q", tc.path, body.StreamURL)
		}
	}
}

func TestHandleImportBookForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour), booksHTTP: mustURL("http://127.0.0.1:1")}
	req := httptest.NewRequest(http.MethodPost, "/api/books/works/b1/import", strings.NewReader(`{"path":"/x"}`))
	req.SetPathValue("id", "b1")
	w := httptest.NewRecorder()
	s.handleImportBook(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}
