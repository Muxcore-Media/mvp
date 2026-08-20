package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type playbackPolicy struct {
	EnableResume     bool   `json:"enable_resume"`
	EnableTranscode  bool   `json:"enable_transcode"`
	PreferDirectPlay bool   `json:"prefer_direct_play"`
	TrickplayEnabled bool   `json:"trickplay_enabled"`
	MaxBitrateMbps   string `json:"max_bitrate_mbps"`
}

var (
	playbackPolicyMu sync.Mutex
)

func playbackPolicyPath() string {
	if p := strings.TrimSpace(os.Getenv("ADMIN_UI_PLAYBACK_FILE")); p != "" {
		return p
	}
	if d := strings.TrimSpace(os.Getenv("MEDIA_UI_USERDATA_DIR")); d != "" {
		return filepath.Join(d, "playback-policy.json")
	}
	return filepath.Join(os.TempDir(), "muxcore-admin-playback.json")
}

func loadPlaybackPolicy() playbackPolicy {
	playbackPolicyMu.Lock()
	defer playbackPolicyMu.Unlock()
	raw, err := os.ReadFile(playbackPolicyPath())
	if err != nil {
		return playbackPolicy{EnableResume: true, PreferDirectPlay: true, MaxBitrateMbps: "80"}
	}
	var p playbackPolicy
	if json.Unmarshal(raw, &p) != nil {
		return playbackPolicy{EnableResume: true, PreferDirectPlay: true, MaxBitrateMbps: "80"}
	}
	return p
}

type playbackResolveResponse struct {
	StreamURL          string `json:"stream_url"`
	Mode               string `json:"mode"`
	ResumeEnabled      bool   `json:"resume_enabled"`
	TranscoderEnabled  bool   `json:"transcoder_enabled"`
	PreferDirectPlay   bool   `json:"prefer_direct_play"`
	MaxBitrateMbps     string `json:"max_bitrate_mbps"`
	TrickplayEnabled   bool   `json:"trickplay_enabled"`
	TranscoderAvailable bool  `json:"transcoder_available"`
}

func (s *server) handlePlaybackResolve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	src := strings.TrimSpace(r.URL.Query().Get("src"))
	if src == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "src required", "code": "playback.src_required"})
		return
	}
	pol := loadPlaybackPolicy()
	mode := "direct"
	streamURL := src
	transcoderAvail := s.transcoderHTTP != nil && strings.TrimSpace(s.transcoderHTTP.String()) != ""
	if pol.EnableTranscode && !pol.PreferDirectPlay && transcoderAvail {
		mode = "transcode"
		streamURL = "/stream/transcode?src=" + url.QueryEscape(src)
	}
	writeJSON(w, http.StatusOK, playbackResolveResponse{
		StreamURL:           streamURL,
		Mode:                mode,
		ResumeEnabled:       pol.EnableResume,
		TranscoderEnabled:   pol.EnableTranscode,
		PreferDirectPlay:    pol.PreferDirectPlay,
		MaxBitrateMbps:      pol.MaxBitrateMbps,
		TrickplayEnabled:    pol.TrickplayEnabled,
		TranscoderAvailable: transcoderAvail,
	})
}

func (s *server) handleTranscodeStream(w http.ResponseWriter, r *http.Request) {
	src := strings.TrimSpace(r.URL.Query().Get("src"))
	if src == "" {
		http.Error(w, "src required", http.StatusBadRequest)
		return
	}
	pol := loadPlaybackPolicy()
	if !pol.EnableTranscode {
		http.Redirect(w, r, src, http.StatusTemporaryRedirect)
		return
	}
	r2 := r.Clone(r.Context())
	u := *r.URL
	u.Path = src
	u.RawQuery = ""
	r2.URL = &u
	if strings.HasPrefix(src, "/stream/movies/") {
		reverseProxy(s.moviesHTTP).ServeHTTP(w, r2)
		return
	}
	if strings.HasPrefix(src, "/stream/tv/") {
		reverseProxy(s.tvHTTP).ServeHTTP(w, r2)
		return
	}
	http.Redirect(w, r, src, http.StatusTemporaryRedirect)
}
