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
