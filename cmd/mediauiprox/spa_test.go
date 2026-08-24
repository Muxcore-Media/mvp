package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSPA_ServesIndexForAppRoutes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html><body>mux-ui</body></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &server{dist: dir}
	w := httptest.NewRecorder()
	s.spa(w, httptest.NewRequest(http.MethodGet, "/movies", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "mux-ui") {
		t.Fatalf("body=%s", w.Body.String())
	}
}

func TestSPA_RejectsAPIPaths(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "index.html"), []byte("ok"), 0o644)
	s := &server{dist: dir}
	w := httptest.NewRecorder()
	s.spa(w, httptest.NewRequest(http.MethodGet, "/api/movies", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d", w.Code)
	}
}
