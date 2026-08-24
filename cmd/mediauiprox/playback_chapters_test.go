package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlePlaybackChaptersMissingSrc(t *testing.T) {
	s := &server{}
	req := httptest.NewRequest(http.MethodGet, "/api/playback/chapters", nil)
	rec := httptest.NewRecorder()
	s.handlePlaybackChapters(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestHandlePlaybackChaptersDisabledWithoutFFprobe(t *testing.T) {
	s := &server{}
	req := httptest.NewRequest(http.MethodGet, "/api/playback/chapters?src=/stream/movies/m1", nil)
	rec := httptest.NewRecorder()
	s.handlePlaybackChapters(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	var out playbackChaptersResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Enabled || len(out.Chapters) != 0 {
		t.Fatalf("expected disabled empty: %+v", out)
	}
}

func TestHandlePlaybackChaptersMethodNotAllowed(t *testing.T) {
	s := &server{}
	req := httptest.NewRequest(http.MethodPost, "/api/playback/chapters?src=/stream/movies/m1", nil)
	rec := httptest.NewRecorder()
	s.handlePlaybackChapters(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestIntervalChaptersFallback(t *testing.T) {
	ch := intervalChaptersFallback(3600)
	if len(ch) != 6 {
		t.Fatalf("want 6 got %d", len(ch))
	}
	if ch[0].Source != "interval" {
		t.Fatalf("source=%q", ch[0].Source)
	}
}
