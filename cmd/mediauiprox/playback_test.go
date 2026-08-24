package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlaybackResolveDirect(t *testing.T) {
	dir := t.TempDir()
	policy := filepath.Join(dir, "playback.json")
	_ = os.WriteFile(policy, []byte(`{"enable_resume":true,"enable_transcode":false,"prefer_direct_play":true}`), 0o600)
	t.Setenv("ADMIN_UI_PLAYBACK_FILE", policy)

	s := &server{transcoderHTTP: nil}
	req := httptest.NewRequest(http.MethodGet, "/api/playback/resolve?src=%2Fstream%2Fmovies%2Fm1", nil)
	rec := httptest.NewRecorder()
	s.handlePlaybackResolve(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out playbackResolveResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Mode != "direct" || out.StreamURL != "/stream/movies/m1" || !out.ResumeEnabled {
		t.Fatalf("%+v", out)
	}
}

func TestPlaybackResolveTranscodeMode(t *testing.T) {
	dir := t.TempDir()
	policy := filepath.Join(dir, "playback.json")
	_ = os.WriteFile(policy, []byte(`{"enable_resume":true,"enable_transcode":true,"prefer_direct_play":false}`), 0o600)
	t.Setenv("ADMIN_UI_PLAYBACK_FILE", policy)

	s := &server{transcoderHTTP: mustURL("http://127.0.0.1:9526")}
	req := httptest.NewRequest(http.MethodGet, "/api/playback/resolve?src=%2Fstream%2Fmovies%2Fm1", nil)
	rec := httptest.NewRecorder()
	s.handlePlaybackResolve(rec, req)
	var out playbackResolveResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Mode != "transcode" || !strings.Contains(out.StreamURL, "/stream/transcode") {
		t.Fatalf("%+v", out)
	}
	if !out.TranscoderAvailable {
		t.Fatal("expected transcoder available")
	}
}

func TestPlaybackSourceURL(t *testing.T) {
	s := &server{
		moviesHTTP: mustURL("http://127.0.0.1:9430"),
		tvHTTP:     mustURL("http://127.0.0.1:9450"),
	}
	if got := s.playbackSourceURL("/stream/movies/m1"); got != "http://127.0.0.1:9430/stream/movies/m1" {
		t.Fatalf("movies: %q", got)
	}
	if got := s.playbackSourceURL("/stream/tv/e1"); got != "http://127.0.0.1:9450/stream/tv/e1" {
		t.Fatalf("tv: %q", got)
	}
}

func TestPlaybackResolveMissingSrcErrorShape(t *testing.T) {
	s := &server{}
	req := httptest.NewRequest(http.MethodGet, "/api/playback/resolve", nil)
	rec := httptest.NewRecorder()
	s.handlePlaybackResolve(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["error"] != "src required" || out["code"] != "playback.src_required" {
		t.Fatalf("unexpected body: %#v", out)
	}
}

func TestPlaybackResolveMethodNotAllowedErrorShape(t *testing.T) {
	s := &server{}
	req := httptest.NewRequest(http.MethodPost, "/api/playback/resolve?src=%2Fstream%2Fmovies%2Fm1", nil)
	rec := httptest.NewRecorder()
	s.handlePlaybackResolve(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["error"] != "method not allowed" || out["code"] != "playback.method_not_allowed" {
		t.Fatalf("unexpected body: %#v", out)
	}
}

func TestHandleTranscodeStreamMissingSrcErrorShape(t *testing.T) {
	s := &server{transcoderHTTP: mustURL("http://127.0.0.1:9526")}
	req := httptest.NewRequest(http.MethodGet, "/stream/transcode", nil)
	rec := httptest.NewRecorder()
	s.handleTranscodeStream(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["error"] != "src required" || out["code"] != "playback.src_required" {
		t.Fatalf("unexpected body: %#v", out)
	}
}

func TestHandlePlaybackSegmentsMissingMediaID(t *testing.T) {
	s := &server{}
	req := httptest.NewRequest(http.MethodGet, "/api/playback/segments", nil)
	rec := httptest.NewRecorder()
	s.handlePlaybackSegments(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandlePlaybackSegmentsDisabledWithoutIntroOutro(t *testing.T) {
	s := &server{}
	req := httptest.NewRequest(http.MethodGet, "/api/playback/segments?media_id=m1", nil)
	rec := httptest.NewRecorder()
	s.handlePlaybackSegments(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out playbackSegmentsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Enabled || len(out.Segments) != 0 || out.MediaID != "m1" {
		t.Fatalf("%+v", out)
	}
}

func TestHandlePlaybackSegmentsMethodNotAllowed(t *testing.T) {
	s := &server{}
	req := httptest.NewRequest(http.MethodPost, "/api/playback/segments?media_id=m1", nil)
	rec := httptest.NewRecorder()
	s.handlePlaybackSegments(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestHandleTrickplaySpriteMissingSrcErrorShape(t *testing.T) {
	s := &server{transcoderHTTP: mustURL("http://127.0.0.1:9526")}
	req := httptest.NewRequest(http.MethodGet, "/stream/trickplay", nil)
	rec := httptest.NewRecorder()
	s.handleTrickplaySprite(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleTrickplaySpriteUnavailableWithoutTranscoder(t *testing.T) {
	s := &server{}
	req := httptest.NewRequest(http.MethodGet, "/stream/trickplay?src=%2Fstream%2Fmovies%2Fm1&duration=120", nil)
	rec := httptest.NewRecorder()
	s.handleTrickplaySprite(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleTrickplaySpriteProxiesToTranscoder(t *testing.T) {
	var gotSrc, gotDuration, gotInterval string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stream/trickplay" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		gotSrc = r.URL.Query().Get("src")
		gotDuration = r.URL.Query().Get("duration")
		gotInterval = r.URL.Query().Get("interval")
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("jpegfake"))
	}))
	defer upstream.Close()

	s := &server{
		transcoderHTTP: mustURL(upstream.URL),
		moviesHTTP:     mustURL("http://127.0.0.1:9430"),
	}
	req := httptest.NewRequest(http.MethodGet, "/stream/trickplay?src=%2Fstream%2Fmovies%2Fm1&duration=7200&interval=15", nil)
	rec := httptest.NewRecorder()
	s.handleTrickplaySprite(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if gotSrc != "http://127.0.0.1:9430/stream/movies/m1" {
		t.Fatalf("proxied src=%q", gotSrc)
	}
	if gotDuration != "7200" || gotInterval != "15" {
		t.Fatalf("duration=%q interval=%q", gotDuration, gotInterval)
	}
}

func TestHandleTranscodeStreamProxiesToTranscoder(t *testing.T) {
	dir := t.TempDir()
	policy := filepath.Join(dir, "playback.json")
	_ = os.WriteFile(policy, []byte(`{"enable_resume":true,"enable_transcode":true,"prefer_direct_play":false}`), 0o600)
	t.Setenv("ADMIN_UI_PLAYBACK_FILE", policy)

	var gotSrc string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stream/transcode" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		gotSrc = r.URL.Query().Get("src")
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("ftypfake"))
	}))
	defer upstream.Close()

	s := &server{
		transcoderHTTP: mustURL(upstream.URL),
		moviesHTTP:     mustURL("http://127.0.0.1:9430"),
	}
	req := httptest.NewRequest(http.MethodGet, "/stream/transcode?src=%2Fstream%2Fmovies%2Fm1", nil)
	rec := httptest.NewRecorder()
	s.handleTranscodeStream(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if gotSrc != "http://127.0.0.1:9430/stream/movies/m1" {
		t.Fatalf("proxied src=%q", gotSrc)
	}
}

func TestHandleTranscodeStreamForwardsSeekStart(t *testing.T) {
	dir := t.TempDir()
	policy := filepath.Join(dir, "playback.json")
	_ = os.WriteFile(policy, []byte(`{"enable_resume":true,"enable_transcode":true,"prefer_direct_play":false}`), 0o600)
	t.Setenv("ADMIN_UI_PLAYBACK_FILE", policy)

	var gotStart string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotStart = r.URL.Query().Get("start")
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("ftypfake"))
	}))
	defer upstream.Close()

	s := &server{
		transcoderHTTP: mustURL(upstream.URL),
		moviesHTTP:     mustURL("http://127.0.0.1:9430"),
	}
	req := httptest.NewRequest(http.MethodGet, "/stream/transcode?src=%2Fstream%2Fmovies%2Fm1&start=611.5", nil)
	rec := httptest.NewRecorder()
	s.handleTranscodeStream(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if gotStart != "611.5" {
		t.Fatalf("start=%q", gotStart)
	}
}
