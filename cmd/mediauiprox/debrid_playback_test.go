package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPlaybackResolveDebrid(t *testing.T) {
	s := &server{}
	req := httptest.NewRequest(http.MethodGet, "/api/playback/resolve?src=debrid:rd-123", nil)
	rec := httptest.NewRecorder()
	s.handlePlaybackResolve(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `/api/debrid/stream?id=rd-123`) {
		t.Fatalf("body %s", rec.Body.String())
	}
}
