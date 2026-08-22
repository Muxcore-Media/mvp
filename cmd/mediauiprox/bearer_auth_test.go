package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBearerSessionAuth(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	tok, err := s.sessions.Create("u1", "alice")
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/movies", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := s.withAuth(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/movies", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with bearer, got %d", w.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/movies", nil)
	req2.Header.Set("Authorization", "Bearer invalid")
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with bad bearer, got %d", w2.Code)
	}
}
