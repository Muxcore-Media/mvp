package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	ffprobev1 "github.com/Muxcore-Media/media-ffprobe/proto/ffprobev1"
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

func TestPlaybackChaptersFromAnalyzeEmbedded(t *testing.T) {
	out := playbackChaptersFromAnalyze(&ffprobev1.AnalyzeResponse{
		Chapters: []*ffprobev1.Chapter{
			{Index: 0, Title: "Opening", StartSeconds: 0, EndSeconds: 90},
			{Index: 1, Title: "  Act I  ", StartSeconds: 90, EndSeconds: 240},
			{Index: 2, Title: "empty", StartSeconds: 240, EndSeconds: 240},
		},
	})
	if len(out) != 2 {
		t.Fatalf("want 2 got %d", len(out))
	}
	if out[0].Source != "embedded" || out[0].Title != "Opening" || out[0].EndSeconds != 90 {
		t.Fatalf("ch0=%+v", out[0])
	}
	if out[1].Title != "Act I" || out[1].StartSeconds != 90 {
		t.Fatalf("ch1=%+v", out[1])
	}
}

func TestPlaybackChaptersFromAnalyzeNil(t *testing.T) {
	if playbackChaptersFromAnalyze(nil) != nil {
		t.Fatal("expected nil")
	}
	if playbackChaptersFromAnalyze(&ffprobev1.AnalyzeResponse{}) != nil {
		t.Fatal("expected empty analyze to yield nil")
	}
}
