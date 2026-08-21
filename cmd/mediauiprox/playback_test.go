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
